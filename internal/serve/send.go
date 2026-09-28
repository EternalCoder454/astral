package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
	"astral/internal/websearch"
	"astral/internal/world"
)

// sendTimeout bounds one turn. A large model on a busy machine is slow, not
// broken, so this is generous; it is here so a connection that has gone away
// cannot hold a generation open for ever.
const sendTimeout = 15 * time.Minute

// tokenFlush is how often at most the phone is sent what has arrived. The
// window uses the same interval for the same reason.
const tokenFlush = 50 * time.Millisecond

// handleSend takes a message, streams the reply back, and leaves the
// conversation in the same state the desktop window would have left it in.
//
// Server-sent events rather than a websocket: the reply only travels one way,
// and SSE reconnects and passes through anything a websocket would have to be
// negotiated past.
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a chat id"})
		return
	}
	var body struct {
		Text string `json:"text"`
		// Speaker is who answers, in a group scene, when you chose.
		Speaker string `json:"speaker"`
		// Onward is a group turn with nothing from you: the cast carry on.
		Onward bool `json:"onward"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	text := strings.TrimSpace(body.Text)
	ch, err := s.store.Chat(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return
	}
	turn := scene.Turn{Speaker: body.Speaker}
	if text == "" {
		// Only a group can carry on without you: a character on their own
		// answering nothing is a character talking to themselves.
		if !body.Onward || len(s.castFor(ch)) < 2 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nothing to send"})
			return
		}
		turn.Onward = true
	}

	release, free := s.busy.claim(ch.ID)
	if !free {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this chat is already writing a reply"})
		return
	}
	defer release()

	if turn.Onward {
		s.generate(w, r, ch, 0, nil, turn)
		return
	}
	userID, err := s.store.AddMessage(store.Message{
		ChatID: ch.ID, Role: ollama.RoleUser, Content: text,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The first message names the chat, the same way it does in the window.
	if strings.TrimSpace(ch.Title) == "" || ch.Title == "New Chat" {
		if err := s.store.RenameChat(ch.ID, store.TitleFrom(text)); err == nil {
			ch.Title = store.TitleFrom(text)
		}
	}
	s.generate(w, r, ch, userID, nil, turn)
}

// handleRegenerate throws away the last reply and writes another one.
//
// The whole of the last reply: a group turn is stored as one message per
// speaker, so rewinding to the first of the trailing run of them is what
// undoes one turn rather than one voice within it.
func (s *Server) handleRegenerate(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a chat id"})
		return
	}
	ch, err := s.store.Chat(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return
	}
	// A note on what the new reply should do differently, when there is one.
	// The body is optional: a plain rewrite sends none.
	var body struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body)
	}
	release, free := s.busy.claim(ch.ID)
	if !free {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this chat is already writing a reply"})
		return
	}
	defer release()

	msgs, err := s.store.Messages(ch.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	from := int64(0)
	run := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != ollama.RoleAssistant {
			break
		}
		from = msgs[i].ID
		run++
	}
	if from == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "there is no reply to write again"})
		return
	}
	// A single reply keeps what it said, as the window's does: the new one is
	// stored beside it as another version. A group turn is rewound as before.
	var base []store.Version
	if run == 1 && len(s.castFor(ch)) <= 1 {
		last := msgs[len(msgs)-1]
		base = append(base, last.Versions...)
		if len(base) == 0 {
			base = []store.Version{{Content: last.Content, Thinking: last.Thinking}}
		}
	}
	if err := s.store.DeleteMessagesFrom(ch.ID, from); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.generate(w, r, ch, 0, base, scene.Turn{Note: strings.TrimSpace(body.Note)})
}

// generate streams one reply for a chat whose messages are already in the
// state the model should see.
//
// Shared by sending and regenerating, which differ only in what they do to the
// transcript first: one adds a turn, the other removes one. Everything after
// that, the prompt, the search, the folding of deliberation, the storing of
// the result and the housekeeping, is the same work, and was worth having in
// one place rather than two that drift.
//
// userID is the turn a send just stored, told to the phone first so the turn
// it drew can be deleted or copied without reopening the chat. Zero for a
// regenerate, which stores no turn of its own.
//
// base is the versions of the reply a regenerate is replacing. The new reply
// is stored beside them, and if it never arrives they are put back, because
// writing a reply again must never be how one is lost.
func (s *Server) generate(w http.ResponseWriter, r *http.Request, ch store.Chat, userID int64, base []store.Version, turn scene.Turn) {
	saved := false
	if len(base) > 0 {
		defer func() {
			if saved {
				return
			}
			last := base[len(base)-1]
			m := store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant,
				Content: last.Content, Thinking: last.Thinking}
			if len(base) > 1 {
				m.Versions, m.Version = base, len(base)-1
			}
			if _, err := s.store.AddMessage(m); err != nil {
				log.Printf("astral: putting back chat %d's reply: %v", ch.ID, err)
			}
		}()
	}
	cfg := s.playedAs(s.config(), ch)
	ca := s.characterFor(ch)
	model := ch.Model
	if model == "" {
		model = cfg.Model
	}

	cast := s.castFor(ch)
	hist, err := s.history(ch, castNames(cast))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	msgs := scene.BuildTurn(s.store, cfg, ch, castFor(cast, ca), hist, turn)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "this server cannot stream"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Once the phone has gone, writing to it is pointless, and the reply is
	// still being written for when it comes back.
	gone := r.Context().Done()
	send := func(event string, v any) {
		select {
		case <-gone:
			return
		default:
		}
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}
	if userID != 0 {
		send("accepted", map[string]int64{"user_id": userID})
	}

	// Not the request's context. A phone that locks its screen, or walks out
	// of Wi-Fi for a moment, drops the connection, and tying the reply to it
	// threw away every reply that took longer than someone kept looking at the
	// screen. So the reply is finished and stored on the PC regardless, and the
	// phone picks it up when it comes back. Stopping is an explicit request
	// (handleStop) rather than a side effect of the connection.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), sendTimeout)
	defer cancel()
	stopped := s.busy.onStop(ch.ID, cancel)

	// The same fold the window applies while streaming: deliberation that
	// arrives inside the reply never reaches the phone, rather than appearing
	// and being tidied away once the turn ends.
	var think ollama.ThinkStream

	// And the same coalescing. A fast model generates around a hundred tokens
	// a second, and a frame each means a JSON encode, a write and a flush a
	// hundred times a second over a home network, with the page replacing its
	// text and scrolling on every one. Twenty times a second is already more
	// often than anything can be read.
	//
	// All of it stays on this goroutine, which is the one blocked in Chat, so
	// nothing else is ever writing to the response at the same time.
	var pending strings.Builder
	last := time.Now()
	flush := func(force bool) {
		if pending.Len() == 0 {
			return
		}
		if !force && time.Since(last) < tokenFlush {
			return
		}
		send("token", map[string]string{"t": pending.String()})
		pending.Reset()
		last = time.Now()
	}

	noThink := false
	// Everything the model has written so far, kept so a reply stopped
	// halfway is stored as far as it got, as the window does.
	var sofar strings.Builder
	onDelta := func(delta ollama.Delta) {
		if delta.Content == "" {
			return
		}
		sofar.WriteString(delta.Content)
		if shown, _ := think.Next(delta.Content); shown != "" {
			pending.WriteString(shown)
		}
		flush(false)
	}

	// The same release the window does: a phone that plays a scene on another
	// model must not leave the window's model resident beside it.
	s.client().UseForReplies(ctx, model)
	// And the same knowledge, found the same way, so a question asked from the
	// phone draws on what the window saved.
	msgs = scene.WithKnowledge(ctx, s.store, s.client(), cfg, ch.Kind, msgs, hist)
	scene.RecordSent(ch, len(cast) > 1, msgs)

	var reply ollama.Message
	var stats ollama.Stats
	var rounds []websearch.Round
	// The same search the window offers, on the same conversations. Without this
	// the phone would be told it can search, by the same assembler, and then be
	// handed no tool to do it with: the model would claim to have looked something
	// up and have looked nothing up.
	// Room for the whole conversation, as on the PC; see scene.FitContext.
	opts := scene.FitContext(ctx, s.client(), model, ch.Kind, scene.OptionsFor(cfg, ch.Kind), msgs)
	if runner := scene.Runner(s.client(), cfg, s.store, ch.Kind, model, opts, &noThink); runner != nil {
		runner.KeepPage = func(r websearch.Round) { scene.KeepPage(s.store, cfg, r) }
		// The phone is told to clear what it has shown, the same as the window.
		// On this goroutine, which is the one writing the response, so nothing
		// else is mid-write.
		runner.OnDiscard = func() {
			pending.Reset()
			sofar.Reset()
			think = ollama.ThinkStream{}
			send("reset", map[string]string{})
		}
		// Said as it happens, because a tool is the one part of a turn where
		// nothing arrives for several seconds and the phone would otherwise look
		// stuck.
		runner.OnRound = func(r websearch.Round) {
			switch {
			case r.Note != "":
				send("working", map[string]string{"say": r.Note})
			case r.Opened != "":
				send("reading", map[string]string{"url": r.Opened})
			default:
				send("searching", map[string]string{"q": r.Query})
			}
		}
		reply, stats, rounds, err = runner.Run(ctx, msgs, onDelta)
	} else {
		reply, stats, err = s.client().Chat(ctx, model, msgs, opts, &noThink, onDelta)
	}
	flush(true)
	// A loop stopped by the server keeps what came before it, as the window
	// does, rather than losing the whole reply.
	if errors.Is(err, ollama.ErrRepeatLimit) && strings.TrimSpace(reply.Content) != "" {
		err = nil
	}
	if err != nil && stopped() {
		// Stopped on purpose: keep what was written, the way the window does,
		// rather than making the person who pressed Stop lose the half they
		// wanted.
		reply = ollama.Message{Role: ollama.RoleAssistant, Content: sofar.String()}
		if strings.TrimSpace(reply.Content) == "" {
			send("done", map[string]any{"stopped": true, "title": ch.Title})
			return
		}
		err = nil
	}
	if err != nil {
		send("error", map[string]string{"error": err.Error()})
		return
	}

	if tail, _ := think.Done(); tail != "" {
		send("token", map[string]string{"t": tail})
	}

	content := strings.TrimSpace(reply.Content)
	thinking := reply.Thinking
	if inline, rest := ollama.SplitThinking(content); inline != "" {
		thinking, content = strings.TrimSpace(thinking+"\n\n"+inline), rest
	}
	if notes := websearch.Notes(rounds); notes != "" {
		thinking = strings.TrimSpace(notes + "\n\n" + thinking)
	}
	if content == "" {
		send("error", map[string]string{"error": "the model returned an empty reply"})
		return
	}
	if len(cast) > 1 {
		// A group reply is several turns in one stream. It is split and stored
		// the same way the window splits it, so the same scene reads the same on
		// both screens, and the phone is told the pieces rather than the whole
		// so it can put a name on each.
		beats := chars.SplitBeats(content, chars.CastNames(cast))
		if len(beats) == 0 {
			send("error", map[string]string{"error": "the model returned an empty reply"})
			return
		}
		byName := make(map[string]chars.Character, len(cast))
		for _, member := range cast {
			byName[strings.ToLower(member.Name)] = member
		}
		type beatOut struct {
			ID      int64  `json:"id"`
			Who     string `json:"who"`
			Accent  int    `json:"accent"`
			Content string `json:"content"`
		}
		out := make([]beatOut, 0, len(beats))
		for i, b := range beats {
			who, ok := byName[strings.ToLower(b.Name)]
			if !ok {
				who = cast[0]
			}
			m := store.Message{
				ChatID: ch.ID, Role: ollama.RoleAssistant, Content: b.Text,
				CharacterID: who.ID,
			}
			if i == 0 {
				m.Thinking = thinking
			}
			if i == len(beats)-1 {
				m.EvalCount, m.TokPerSec = stats.Tokens, stats.TokPerSec
			}
			id, err := s.store.AddMessage(m)
			if err != nil {
				send("error", map[string]string{"error": err.Error()})
				return
			}
			out = append(out, beatOut{ID: id, Who: who.Name, Accent: who.Accent, Content: b.Text})
		}
		send("done", map[string]any{"beats": out, "title": ch.Title, "stopped": stopped()})
	} else {
		m := store.Message{
			ChatID: ch.ID, Role: ollama.RoleAssistant, Content: content,
			Thinking: thinking, EvalCount: stats.Tokens, TokPerSec: stats.TokPerSec,
		}
		if len(base) > 0 {
			m.Versions = append(base, store.Version{Content: content, Thinking: thinking})
			m.Version = len(m.Versions) - 1
		}
		msgID, err := s.store.AddMessage(m)
		if err != nil {
			send("error", map[string]string{"error": err.Error()})
			return
		}
		saved = true
		send("done", map[string]any{"id": msgID, "content": content, "title": ch.Title, "stopped": stopped(),
			"versions": len(m.Versions), "version": m.Version})
	}

	// The housekeeping the window does in the background. Without it a scene
	// played only from a phone would never compact and never learn, and would
	// quietly start forgetting its own beginning.
	go s.housekeep(ch.ID, castFor(cast, ca))
}

// history is the conversation as it will be sent: everything the recap does
// not already cover.
func (s *Server) history(ch store.Chat, nameOf func(int64) string) ([]ollama.Message, error) {
	msgs, err := s.store.MessagesAfter(ch.ID, ch.SummaryUpto)
	if err != nil {
		return nil, err
	}
	// Labelled and merged by internal/scene, the same call the window makes, so
	// a scene played from both produces the same prompt and keeps its cached
	// prefix when you switch between them.
	return scene.History(msgs, nameOf), nil
}

// castFor is the cast to assemble a prompt for: the scene's own cast when it has
// one, and otherwise the single character, so BuildFor takes the ordinary path.
func castFor(cast []chars.Character, ca chars.Character) []chars.Character {
	if len(cast) > 1 {
		return cast
	}
	return []chars.Character{ca}
}

// housekeep compacts a scene that has outgrown its window and teaches the
// lorebook what the last few turns established.
//
// It runs after the reply has been delivered, so it never delays one. Errors
// are logged and dropped: a scene that failed to compact this turn tries again
// next turn, and telling a phone about it would interrupt reading a reply to
// report something that fixes itself.
func (s *Server) housekeep(chatID int64, cast []chars.Character) {
	var ca chars.Character
	if len(cast) > 0 {
		ca = cast[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	ch, err := s.store.Chat(chatID)
	if err != nil {
		return
	}
	cfg := s.playedAs(s.config(), ch)
	sceneModel := ch.Model
	if sceneModel == "" {
		sceneModel = cfg.Model
	}
	model := scene.FitHousekeeping(ctx, s.client(), cfg.HousekeepingModel, sceneModel)
	p := scene.Persona(cfg)
	// The cast's budget, not one character's: a group planned as a two-hander
	// thinks it has room it does not have, and waits too long to compact. A
	// conversation with nobody in it is measured against its own framing.
	plain := ch.Kind == store.KindAssistant || len(cast) == 0 || cast[0].Name == ""
	budget := scene.GroupBudget(cfg, cast, p, scene.Relations(s.store, cast))
	if plain {
		budget = scene.PlainBudget(cfg)
	}
	opts := scene.Options(cfg)

	stored, err := s.store.MessagesAfter(chatID, ch.SummaryUpto)
	if err != nil {
		return
	}
	// Labelled, so the recap can say which of them did what.
	nameOf := castNames(cast)
	if len(cast) < 2 {
		nameOf = nil
	}
	hist := scene.History(stored, nameOf)

	if chars.NeedsCompaction(hist, budget) {
		aged, _ := chars.SplitForCompaction(hist, budget)
		if len(aged) > 0 {
			upto := stored[len(aged)-1].ID
			// A conversation and a scene need different questions asked of the
			// summariser: what was decided against who is standing where.
			var next string
			var err error
			if plain {
				next, err = chars.CompactPlain(ctx, s.client(), model, ch.Summary, aged, p, opts, budget)
			} else {
				next, err = chars.CompactFor(ctx, s.client(), model, ch.Summary, aged, cast, p, opts, budget)
			}
			if err != nil {
				log.Printf("astral: compacting %d from a phone: %v", chatID, err)
			} else if err := s.store.SetChatSummary(chatID, next, upto); err != nil {
				log.Printf("astral: saving recap for %d: %v", chatID, err)
			}
		}
	}

	if ca.WorldID == 0 {
		return
	}
	all, err := s.store.Messages(chatID)
	if err != nil {
		return
	}
	var fresh []ollama.Message
	var lastID int64
	for _, m := range all {
		if m.ID <= ch.LoreUpto {
			continue
		}
		fresh = append(fresh, ollama.Message{Role: m.Role, Content: m.Content})
		lastID = m.ID
	}
	if len(fresh) < world.LearnEveryTurns {
		return
	}
	wd, err := s.store.World(ca.WorldID)
	if err != nil {
		return
	}
	existing, err := s.store.LoreEntries(ca.WorldID)
	if err != nil {
		return
	}
	learned, err := world.Learn(ctx, s.client(), model, wd, existing, fresh, ca.Name, p.Name, opts)
	if err != nil {
		log.Printf("astral: learning lore from a phone: %v", err)
		return
	}
	for _, e := range learned {
		if _, err := s.store.SaveLoreEntry(e); err != nil {
			log.Printf("astral: saving learned lore: %v", err)
		}
	}
	s.store.SetChatLoreUpto(chatID, lastID)
}

// busyChats is the set of chats currently having a reply written into them.
//
// The window and a phone can be in the same scene at the same time, and a
// phone that loses its connection mid-turn will happily send again. Two
// generations on one chat interleave their turns in the transcript and ask the
// model for two replies to answer one message, so the second is refused rather
// than queued: the caller is a person waiting, and telling them now is better
// than making them wait twice as long for a reply to a prompt that has since
// changed underneath it.
type busyChats struct {
	mu sync.Mutex
	on map[int64]*busyChat
}

type busyChat struct {
	stop    context.CancelFunc
	stopped bool
}

// claim marks a chat as busy. The returned function frees it, and free says
// whether the claim succeeded; a refused claim still returns a usable no-op so
// a caller cannot deadlock on the difference.
func (b *busyChats) claim(id int64) (release func(), free bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.on[id] != nil {
		return func() {}, false
	}
	if b.on == nil {
		b.on = make(map[int64]*busyChat)
	}
	b.on[id] = &busyChat{}
	return func() {
		b.mu.Lock()
		delete(b.on, id)
		b.mu.Unlock()
	}, true
}

// onStop records how to stop the reply being written into a claimed chat, and
// returns a function saying whether it was stopped that way.
func (b *busyChats) onStop(id int64, stop context.CancelFunc) (stopped func() bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.on[id]
	if c == nil {
		return func() bool { return false }
	}
	c.stop = stop
	return func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return c.stopped
	}
}

// stop ends the reply being written into a chat, if there is one.
func (b *busyChats) stop(id int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.on[id]
	if c == nil || c.stop == nil {
		return false
	}
	c.stopped = true
	c.stop()
	return true
}

// writing reports whether a reply is being written into a chat now.
func (b *busyChats) writing(id int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.on[id] != nil
}

// handleStop stops the reply being written into a chat. What was written so
// far is kept.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a chat id"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": s.busy.stop(id)})
}

// playedAs is the settings with the chat's own persona in place of the one in
// use by default, so a scene started as someone reads as them from the phone
// too.
func (s *Server) playedAs(cfg store.Config, ch store.Chat) store.Config {
	if ch.PersonaID != 0 {
		if p, err := s.store.Persona(ch.PersonaID); err == nil {
			cfg.PersonaName, cfg.PersonaDescription = p.Name, p.Description()
		}
	}
	return cfg
}
