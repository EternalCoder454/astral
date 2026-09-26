package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
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
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nothing to send"})
		return
	}
	ch, err := s.store.Chat(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return
	}

	cfg := s.config()
	ca := s.characterFor(ch)
	model := ch.Model
	if model == "" {
		model = cfg.Model
	}

	if _, err := s.store.AddMessage(store.Message{
		ChatID: ch.ID, Role: ollama.RoleUser, Content: text,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The first message names the chat, the same way it does in the window.
	if strings.TrimSpace(ch.Title) == "" || ch.Title == "New chat" {
		if err := s.store.RenameChat(ch.ID, store.TitleFrom(text)); err == nil {
			ch.Title = store.TitleFrom(text)
		}
	}

	cast := s.castFor(ch)
	hist, err := s.history(ch, castNames(cast))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	msgs := scene.BuildFor(s.store, cfg, ch, castFor(cast, ca), hist)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "this server cannot stream"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(event string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	// The request's own context, so closing the app on the phone stops the
	// generation on the PC rather than leaving it running for nobody.
	ctx, cancel := context.WithTimeout(r.Context(), sendTimeout)
	defer cancel()

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
	reply, stats, err := s.client().Chat(ctx, model, msgs, scene.Options(cfg), &noThink,
		func(delta ollama.Delta) {
			if delta.Content == "" {
				return
			}
			if shown, _ := think.Next(delta.Content); shown != "" {
				pending.WriteString(shown)
			}
			flush(false)
		})
	flush(true)
	if err != nil {
		if ctx.Err() != nil {
			return // the phone went away; nothing to report to it
		}
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
		send("done", map[string]any{"beats": out, "title": ch.Title})
	} else {
		msgID, err := s.store.AddMessage(store.Message{
			ChatID: ch.ID, Role: ollama.RoleAssistant, Content: content,
			Thinking: thinking, EvalCount: stats.Tokens, TokPerSec: stats.TokPerSec,
		})
		if err != nil {
			send("error", map[string]string{"error": err.Error()})
			return
		}
		send("done", map[string]any{"id": msgID, "content": content, "title": ch.Title})
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

	cfg := s.config()
	model := cfg.HousekeepingModel
	if model == "" {
		model = cfg.Model
	}
	ch, err := s.store.Chat(chatID)
	if err != nil {
		return
	}
	p := scene.Persona(cfg)
	// The cast's budget, not one character's: a group planned as a two-hander
	// thinks it has room it does not have, and waits too long to compact. A
	// conversation with nobody in it is measured against its own framing.
	plain := ch.Kind == store.KindAssistant || len(cast) == 0 || cast[0].Name == ""
	budget := scene.GroupBudget(cfg, cast, p)
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
