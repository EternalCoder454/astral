package serve

import (
	"encoding/json"
	"net/http"
	"strings"

	"astral/internal/store"
)

// Settings on a phone.
//
// Not all of them. The window is where a character gets written and a world
// gets built, and reproducing those on a four inch screen would be a worse
// version of both. What is here is what you change while playing and cannot
// otherwise reach: which model answers, who you are, how it writes, and how
// long a reply runs.
//
// They are the same settings, not a copy: this reads and writes the one config
// file the window uses, so changing the model here changes it there.

type settingsOut struct {
	Model       string   `json:"model"`
	Models      []string `json:"models"`
	Persona     string   `json:"persona"`
	PersonaNote string   `json:"persona_note"`
	Style       string   `json:"style"`
	Styles      []string `json:"styles"`
	Temperature float64  `json:"temperature"`
	NumPredict  int      `json:"num_predict"`
	NumCtx      int      `json:"num_ctx"`
	Device      string   `json:"device"`
	Version     string   `json:"version"`
	Update      string   `json:"update"`
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, d store.Device) {
	cfg := s.config()
	out := settingsOut{
		Model:       cfg.Model,
		Persona:     cfg.PersonaName,
		PersonaNote: cfg.PersonaDescription,
		Style:       cfg.Style().Name,
		Temperature: cfg.Temperature,
		NumPredict:  cfg.NumPredict,
		NumCtx:      cfg.NumCtx,
		Device:      d.Name,
		Version:     s.version,
	}
	for _, st := range cfg.Styles() {
		out.Styles = append(out.Styles, st.Name)
	}
	// Asking the model server what it has is a network call, so a phone with
	// no answer gets the list it can still act on: the model in use.
	if models, err := s.client().Probe(r.Context()); err == nil {
		for _, m := range models {
			out.Models = append(out.Models, m.Name)
		}
	} else if cfg.Model != "" {
		out.Models = []string{cfg.Model}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSaveSettings writes back only what the phone is allowed to change.
//
// A field that is absent is left alone rather than zeroed, because a phone on
// an older version of this interface must not be able to blank a setting it
// has never heard of.
func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request, d store.Device) {
	var body struct {
		Model       *string  `json:"model"`
		Persona     *string  `json:"persona"`
		PersonaNote *string  `json:"persona_note"`
		Style       *string  `json:"style"`
		Temperature *float64 `json:"temperature"`
		NumPredict  *int     `json:"num_predict"`
		NumCtx      *int     `json:"num_ctx"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	cfg := s.config()
	if body.Model != nil {
		cfg.Model = strings.TrimSpace(*body.Model)
	}
	if body.Persona != nil {
		cfg.PersonaName = strings.TrimSpace(*body.Persona)
	}
	if body.PersonaNote != nil {
		cfg.PersonaDescription = strings.TrimSpace(*body.PersonaNote)
	}
	if body.Style != nil {
		cfg.ActiveStyle = strings.TrimSpace(*body.Style)
	}
	if body.Temperature != nil {
		cfg.Temperature = *body.Temperature
	}
	if body.NumPredict != nil {
		cfg.NumPredict = *body.NumPredict
	}
	if body.NumCtx != nil {
		cfg.NumCtx = *body.NumCtx
	}
	if err := s.save(cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.handleSettings(w, r, d)
}

// handleForget revokes the device that asks. Unpairing from the phone rather
// than from the PC, for the phone that is being given away or sold.
func (s *Server) handleForget(w http.ResponseWriter, r *http.Request, d store.Device) {
	if err := s.store.DeleteDevice(d.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "forgotten"})
}
