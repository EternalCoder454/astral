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

	hist, err := s.history(ch)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	msgs := scene.Build(s.store, cfg, ch, ca, hist)

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

	noThink := false
	reply, stats, err := s.client().Chat(ctx, model, msgs, scene.Options(cfg), &noThink,
		func(delta ollama.Delta) {
			if delta.Content != "" {
				send("token", map[string]string{"t": delta.Content})
			}
		})
	if err != nil {
		if ctx.Err() != nil {
			return // the phone went away; nothing to report to it
		}
		send("error", map[string]string{"error": err.Error()})
		return
	}

	content := strings.TrimSpace(reply.Content)
	thinking := reply.Thinking
	if inline, rest := splitLeadingThink(content); inline != "" {
		thinking, content = strings.TrimSpace(thinking+"\n\n"+inline), rest
	}
	if content == "" {
		send("error", map[string]string{"error": "the model returned an empty reply"})
		return
	}
	msgID, err := s.store.AddMessage(store.Message{
		ChatID: ch.ID, Role: ollama.RoleAssistant, Content: content,
		Thinking: thinking, EvalCount: stats.Tokens, TokPerSec: stats.TokPerSec,
	})
	if err != nil {
		send("error", map[string]string{"error": err.Error()})
		return
	}
	send("done", map[string]any{"id": msgID, "content": content, "title": ch.Title})

	// The housekeeping the window does in the background. Without it a scene
	// played only from a phone would never compact and never learn, and would
	// quietly start forgetting its own beginning.
	go s.housekeep(ch.ID, ca)
}

// history is the conversation as it will be sent: everything the recap does
// not already cover.
func (s *Server) history(ch store.Chat) ([]ollama.Message, error) {
	msgs, err := s.store.MessagesAfter(ch.ID, ch.SummaryUpto)
	if err != nil {
		return nil, err
	}
	out := make([]ollama.Message, 0, len(msgs))
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		out = append(out, ollama.Message{Role: m.Role, Content: m.Content})
	}
	return out, nil
}

// housekeep compacts a scene that has outgrown its window and teaches the
// lorebook what the last few turns established.
//
// It runs after the reply has been delivered, so it never delays one. Errors
// are logged and dropped: a scene that failed to compact this turn tries again
// next turn, and telling a phone about it would interrupt reading a reply to
// report something that fixes itself.
func (s *Server) housekeep(chatID int64, ca chars.Character) {
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
	budget := scene.Budget(cfg, ca, p)
	opts := scene.Options(cfg)

	stored, err := s.store.MessagesAfter(chatID, ch.SummaryUpto)
	if err != nil {
		return
	}
	hist := make([]ollama.Message, 0, len(stored))
	for _, m := range stored {
		hist = append(hist, ollama.Message{Role: m.Role, Content: m.Content})
	}

	if chars.NeedsCompaction(hist, budget) {
		aged, _ := chars.SplitForCompaction(hist, budget)
		if len(aged) > 0 {
			upto := stored[len(aged)-1].ID
			next, err := chars.Compact(ctx, s.client(), model, ch.Summary, aged, ca, p, opts, budget)
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

// splitLeadingThink is the server's copy of the rule the window applies: a
// model that writes its deliberation into the reply gets it moved out, so the
// transcript holds the reply rather than the model talking to itself about its
// instructions.
func splitLeadingThink(s string) (thinking, reply string) {
	for _, pair := range [][2]string{
		{"<think>", "</think>"},
		{"<thinking>", "</thinking>"},
		{"<reasoning>", "</reasoning>"},
		{"[think]", "[/think]"},
		{"[thinking]", "[/thinking]"},
	} {
		trimmed := strings.TrimLeft(s, " \t\r\n")
		if !strings.HasPrefix(strings.ToLower(trimmed[:min(len(trimmed), len(pair[0]))]), pair[0]) {
			continue
		}
		rest := trimmed[len(pair[0]):]
		end := strings.Index(strings.ToLower(rest), pair[1])
		if end < 0 {
			return strings.TrimSpace(rest), ""
		}
		return strings.TrimSpace(rest[:end]), strings.TrimSpace(rest[end+len(pair[1]):])
	}
	return "", s
}
