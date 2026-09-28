package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
)

// The phone's side of favorites, hidden messages, suggested replies, the
// scene's setting, and importing a character from a link.

// handleFavorite marks a character as a favorite, or not.
func (s *Server) handleFavorite(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a character id"})
		return
	}
	var body struct {
		Favorite bool `json:"favorite"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	if _, err := s.store.Character(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such character"})
		return
	}
	if err := s.store.SetFavorite(id, body.Favorite); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"favorite": body.Favorite})
}

// handleHide hides a message from the model, or shows it again.
func (s *Server) handleHide(w http.ResponseWriter, r *http.Request, d store.Device) {
	chatID, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	mid, err2 := strconv.ParseInt(r.PathValue("mid"), 10, 64)
	if err1 != nil || err2 != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a message id"})
		return
	}
	var body struct {
		Hidden bool `json:"hidden"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	if !s.messageIn(chatID, mid) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such message"})
		return
	}
	if err := s.store.SetMessageHidden(mid, body.Hidden); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"hidden": body.Hidden})
}

// sceneFor is what a request about the next turn needs: the chat, who is in
// it, the settings it is played with, the transcript and the model.
func (s *Server) sceneFor(w http.ResponseWriter, r *http.Request) (store.Chat, []chars.Character, store.Config, []ollama.Message, string, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a chat id"})
		return store.Chat{}, nil, store.Config{}, nil, "", false
	}
	ch, err := s.store.Chat(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return store.Chat{}, nil, store.Config{}, nil, "", false
	}
	stored := s.castFor(ch)
	cast := castFor(stored, s.characterFor(ch))
	if !scene.CanDraft(ch, cast) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that is for scenes"})
		return store.Chat{}, nil, store.Config{}, nil, "", false
	}
	cfg := s.playedAs(s.config(), ch)
	hist, err := s.history(ch, castNames(stored))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return store.Chat{}, nil, store.Config{}, nil, "", false
	}
	model := ch.Model
	if model == "" {
		model = cfg.Model
	}
	return ch, cast, cfg, hist, model, true
}

// handleSuggest offers three things you could say next.
func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request, d store.Device) {
	ch, cast, cfg, hist, model, ok := s.sceneFor(w, r)
	if !ok {
		return
	}
	release, free := s.busy.claim(ch.ID)
	if !free {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this chat is already writing a reply"})
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), sendTimeout)
	defer cancel()
	s.busy.onStop(ch.ID, cancel)
	s.client().UseForReplies(ctx, model)
	raw, _, err := s.client().Structured(ctx, model, scene.Suggest(s.store, cfg, ch, cast, hist),
		scene.SuggestOptions(cfg, ch.Kind), chars.SuggestSchema)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	options := chars.ParseSuggestions(raw, userName(cfg))
	if len(options) == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the model offered nothing usable, so try again"})
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"options": options})
}

// handleSuggestSetting asks where and when the scene is now. Nothing is
// stored: the phone shows it in the box to keep or change.
func (s *Server) handleSuggestSetting(w http.ResponseWriter, r *http.Request, d store.Device) {
	ch, cast, cfg, hist, model, ok := s.sceneFor(w, r)
	if !ok {
		return
	}
	release, free := s.busy.claim(ch.ID)
	if !free {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this chat is already writing a reply"})
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), sendTimeout)
	defer cancel()
	s.client().UseForReplies(ctx, model)
	noThink := false
	opts := scene.DraftOptions(cfg, ch.Kind)
	msg, _, err := s.client().Chat(ctx, model, scene.SuggestSetting(s.store, cfg, ch, cast, hist), opts, &noThink, nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	_, text := ollama.SplitThinking(msg.Content)
	writeJSON(w, http.StatusOK, map[string]string{"setting": chars.CleanSetting(text)})
}

// handleImportLink imports a character from a Chub page or a card's own
// address.
func (s *Server) handleImportLink(w http.ResponseWriter, r *http.Request, d store.Device) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	c, avatar, err := chars.FetchCard(r.Context(), body.URL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.Accent = chars.AccentFor(c.Name, chars.AccentCount)
	if len(avatar) > 0 {
		if p, err := store.SaveAvatar(c.Name, avatar); err == nil {
			c.AvatarPath = p
		}
	}
	id, err := s.store.SaveCharacter(c)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": c.Name})
}

// userName is who you are in a chat's settings, or the default.
func userName(cfg store.Config) string {
	if n := strings.TrimSpace(cfg.PersonaName); n != "" {
		return n
	}
	return chars.DefaultPersonaName
}

// handleWarm gets the model and a chat's prompt ready while a message is typed
// on the phone, as the window does at its first keystroke. It answers at once;
// the warm-up runs on after it. See scene.WarmForTyping.
func (s *Server) handleWarm(w http.ResponseWriter, r *http.Request, d store.Device) {
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
	if s.busy.writing(id) {
		writeJSON(w, http.StatusAccepted, map[string]bool{"warming": false})
		return
	}
	cfg := s.playedAs(s.config(), ch)
	stored := s.castFor(ch)
	cast := castFor(stored, s.characterFor(ch))
	hist, err := s.history(ch, castNames(stored))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	msgs := scene.BuildFor(s.store, cfg, ch, cast, hist)
	model := ch.Model
	if model == "" {
		model = cfg.Model
	}
	client := s.client()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		opts := scene.FitContext(ctx, client, model, ch.Kind, scene.OptionsFor(cfg, ch.Kind), msgs)
		cancel()
		scene.WarmForTyping(client, model, strconv.FormatInt(ch.ID, 10), msgs, opts)
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"warming": true})
}
