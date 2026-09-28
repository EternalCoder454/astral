package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
)

// The phone's side of steering a scene: Write for Me, pins, branches and the
// scene's memory. Rewrite with a Note and a group's turn are the regenerate
// and send handlers, which take the steering in their bodies.

// remembers reports whether a chat keeps a record, and so has pins and a
// memory worth showing: a scene with somebody in it, or a general chat. The
// same answer the window gives.
func remembers(ch store.Chat, ca chars.Character) bool {
	switch ch.Kind {
	case store.KindAssistant:
		return true
	case store.KindRoleplay, "":
		return ca.Name != ""
	}
	return false
}

// handleDraft writes your next message in a scene, streamed, for the phone to
// put in its message box. Nothing is stored: the draft is yours to send or not.
func (s *Server) handleDraft(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a chat id"})
		return
	}
	var body struct {
		Idea string `json:"idea"`
	}
	if r.ContentLength != 0 {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body)
	}
	ch, err := s.store.Chat(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return
	}
	stored := s.castFor(ch)
	cast := castFor(stored, s.characterFor(ch))
	if !scene.CanDraft(ch, cast) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Write for Me is for scenes"})
		return
	}
	release, free := s.busy.claim(ch.ID)
	if !free {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this chat is already writing a reply"})
		return
	}
	defer release()

	cfg := s.playedAs(s.config(), ch)
	hist, err := s.history(ch, castNames(stored))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	msgs := scene.Draft(s.store, cfg, ch, cast, hist, body.Idea)
	model := ch.Model
	if model == "" {
		model = cfg.Model
	}
	userName := strings.TrimSpace(cfg.PersonaName)
	if userName == "" {
		userName = chars.DefaultPersonaName
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "this server cannot stream"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
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

	// Tied to the request this time: a draft nobody is waiting for is worth
	// nothing, unlike a reply, which is kept for when the phone comes back.
	ctx, cancel := context.WithTimeout(r.Context(), sendTimeout)
	defer cancel()
	stopped := s.busy.onStop(ch.ID, cancel)

	var think ollama.ThinkStream
	var sofar strings.Builder
	last := time.Now()
	shownLen := 0
	onDelta := func(delta ollama.Delta) {
		shown, _ := think.Next(delta.Content)
		if sofar.Len() < scene.DraftChars {
			sofar.WriteString(shown)
		}
		if time.Since(last) >= tokenFlush && sofar.Len() > shownLen {
			send("draft", map[string]string{"text": chars.CleanDraft(sofar.String(), userName)})
			shownLen, last = sofar.Len(), time.Now()
		}
	}
	s.client().UseForReplies(ctx, model)
	noThink := false
	draftOpts := scene.DraftOptions(cfg, ch.Kind)
	chat := func(ctx context.Context, m []ollama.Message, d func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
		return s.client().Chat(ctx, model, m, draftOpts, &noThink, d)
	}
	_, _, err = scene.Unslop(ctx, chat, msgs, onDelta)
	text := chars.CleanDraft(sofar.String(), userName)
	if err != nil && !stopped() && text == "" {
		send("error", map[string]string{"error": err.Error()})
		return
	}
	send("done", map[string]any{"text": text, "stopped": stopped()})
}

// handlePin pins or unpins one message.
func (s *Server) handlePin(w http.ResponseWriter, r *http.Request, d store.Device) {
	chatID, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	mid, err2 := strconv.ParseInt(r.PathValue("mid"), 10, 64)
	if err1 != nil || err2 != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a message id"})
		return
	}
	var body struct {
		Pinned bool `json:"pinned"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	if !s.messageIn(chatID, mid) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such message"})
		return
	}
	if err := s.store.SetMessagePinned(mid, body.Pinned); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"pinned": body.Pinned})
}

// messageIn reports whether a message belongs to a chat, so a request naming
// one chat cannot change another's messages.
func (s *Server) messageIn(chatID, mid int64) bool {
	msgs, err := s.store.Messages(chatID)
	if err != nil {
		return false
	}
	for _, m := range msgs {
		if m.ID == mid {
			return true
		}
	}
	return false
}

// handleBranch makes a new chat that is this one up to a message.
func (s *Server) handleBranch(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a chat id"})
		return
	}
	var body struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	ch, err := s.store.Chat(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return
	}
	if s.busy.writing(id) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "wait for the reply to finish"})
		return
	}
	b, err := s.store.BranchChat(id, body.MessageID, store.BranchTitle(ch.Title))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": b.ID, "title": b.Title})
}

// pinOut is one pinned message, as the phone lists it.
type pinOut struct {
	ID   int64  `json:"id"`
	Who  string `json:"who"`
	Text string `json:"text"`
}

// handleMemory is a chat's record and pins.
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request, d store.Device) {
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
	pins, err := s.store.Pinned(id, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	cfg := s.playedAs(s.config(), ch)
	you := strings.TrimSpace(cfg.PersonaName)
	if you == "" {
		you = chars.DefaultPersonaName
	}
	cast := s.castFor(ch)
	nameOf := castNames(cast)
	ca := s.characterFor(ch)
	out := struct {
		Recap string `json:"recap"`
		// Covers says a record exists: until a scene outgrows the model's
		// memory there is nothing to correct.
		Covers  bool     `json:"covers"`
		Pins    []pinOut `json:"pins"`
		Setting string   `json:"setting"`
		// SettingAuto says Astral keeps the setting up to date.
		SettingAuto bool `json:"setting_auto"`
		// Usage is how full the model's memory is on the next turn.
		Usage *scene.Usage `json:"usage,omitempty"`
	}{Recap: ch.Summary, Covers: ch.SummaryUpto != 0, Pins: []pinOut{}, Setting: ch.Setting, SettingAuto: ch.SettingAuto}
	if hist, err := s.history(ch, castNames(cast)); err == nil {
		u := scene.MeasureUsage(s.store, cfg, ch, castFor(cast, ca), hist)
		out.Usage = &u
	}
	for _, p := range pins {
		who := you
		if p.Role == ollama.RoleAssistant {
			who = ca.Name
			if nameOf != nil && p.CharacterID != 0 {
				if n := nameOf(p.CharacterID); n != "" {
					who = n
				}
			}
		}
		out.Pins = append(out.Pins, pinOut{ID: p.ID, Who: who, Text: chars.HideAttachedFiles(p.Content)})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSaveMemory stores a corrected record.
func (s *Server) handleSaveMemory(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a chat id"})
		return
	}
	// Either or both: a scene too young for a record still has a setting.
	var body struct {
		Recap       *string `json:"recap"`
		Setting     *string `json:"setting"`
		SettingAuto *bool   `json:"setting_auto"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	ch, err := s.store.Chat(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return
	}
	if body.Recap != nil && ch.SummaryUpto == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this chat has no record yet"})
		return
	}
	if s.busy.writing(id) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "wait for the reply to finish"})
		return
	}
	if body.Recap != nil {
		if err := s.store.SetChatSummary(id, strings.TrimSpace(*body.Recap), ch.SummaryUpto); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if body.Setting != nil {
		if err := s.store.SetChatSetting(id, *body.Setting); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if body.SettingAuto != nil {
		if err := s.store.SetChatSettingAuto(id, *body.SettingAuto); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// handleRewriteMine rewrites a message you sent, better, and stores it. The
// phone then has your last message answered again, as the window does.
func (s *Server) handleRewriteMine(w http.ResponseWriter, r *http.Request, d store.Device) {
	chatID, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	mid, err2 := strconv.ParseInt(r.PathValue("mid"), 10, 64)
	if err1 != nil || err2 != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a message id"})
		return
	}
	ch, err := s.store.Chat(chatID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return
	}
	var mine *store.Message
	msgs, err := s.store.Messages(chatID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for i := range msgs {
		if msgs[i].ID == mid && msgs[i].Role == ollama.RoleUser {
			mine = &msgs[i]
		}
	}
	if mine == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such message of yours"})
		return
	}
	stored := s.castFor(ch)
	cast := castFor(stored, s.characterFor(ch))
	if !scene.CanDraft(ch, cast) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rewriting your message is for scenes"})
		return
	}
	release, free := s.busy.claim(ch.ID)
	if !free {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this chat is already writing a reply"})
		return
	}
	defer release()

	cfg := s.playedAs(s.config(), ch)
	hist, err := scene.HistoryBefore(s.store, ch, castNames(stored), mid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	model := ch.Model
	if model == "" {
		model = cfg.Model
	}
	userName := strings.TrimSpace(cfg.PersonaName)
	if userName == "" {
		userName = chars.DefaultPersonaName
	}
	ctx, cancel := context.WithTimeout(r.Context(), sendTimeout)
	defer cancel()
	stopped := s.busy.onStop(ch.ID, cancel)
	s.client().UseForReplies(ctx, model)
	noThink := false
	var think ollama.ThinkStream
	var sofar strings.Builder
	mineOpts := scene.DraftOptions(cfg, ch.Kind)
	chat := func(ctx context.Context, m []ollama.Message, d func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
		return s.client().Chat(ctx, model, m, mineOpts, &noThink, d)
	}
	_, _, err = scene.Unslop(ctx, chat, scene.Draft(s.store, cfg, ch, cast, hist, mine.Content), func(d ollama.Delta) {
			shown, _ := think.Next(d.Content)
			if sofar.Len() < scene.DraftChars*2 {
				sofar.WriteString(shown)
			}
		})
	text := chars.CleanDraft(sofar.String(), userName)
	if text == "" {
		if err == nil || stopped() {
			writeJSON(w, http.StatusOK, map[string]any{"content": mine.Content, "changed": false})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.SetMessageContent(mid, text); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": chars.HideAttachedFiles(text), "changed": true})
}
