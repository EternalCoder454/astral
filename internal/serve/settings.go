package serve

import (
	"encoding/json"
	"net/http"
	"strings"

	"astral/internal/chars"
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
	// Personas are the people you play as, and ActivePersona the one new
	// chats are played as; the phone switches between them.
	Personas      []personaOut `json:"personas,omitempty"`
	ActivePersona int64        `json:"active_persona"`
	Style         string       `json:"style"`
	Styles        []string     `json:"styles"`
	Temperature   float64      `json:"temperature"`
	NumPredict    int          `json:"num_predict"`
	NumCtx        int          `json:"num_ctx"`
	Device        string       `json:"device"`
	Version       string       `json:"version"`
	Update        string       `json:"update"`
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, d store.Device) {
	cfg := s.config()
	out := settingsOut{
		Model:         cfg.Model,
		Persona:       cfg.PersonaName,
		PersonaNote:   s.personaNote(cfg),
		ActivePersona: cfg.ActivePersona,
		Style:         cfg.Style().Name,
		Temperature:   cfg.Temperature,
		NumPredict:    cfg.NumPredict,
		NumCtx:        cfg.NumCtx,
		Device:        d.Name,
		Version:       s.version,
	}
	for _, st := range cfg.Styles() {
		out.Styles = append(out.Styles, st.Name)
	}
	if all, err := s.store.Personas(); err == nil {
		for _, p := range all {
			out.Personas = append(out.Personas, personaOut{ID: p.ID, Name: p.DisplayName(), Note: p.Details, Facts: p.Facts()})
		}
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
		Model         *string  `json:"model"`
		Persona       *string  `json:"persona"`
		PersonaNote   *string  `json:"persona_note"`
		ActivePersona *int64   `json:"active_persona"`
		Style         *string  `json:"style"`
		Temperature   *float64 `json:"temperature"`
		NumPredict    *int     `json:"num_predict"`
		NumCtx        *int     `json:"num_ctx"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	cfg := s.config()
	if body.Model != nil {
		cfg.Model = strings.TrimSpace(*body.Model)
	}
	// Who is in use first, so a name and note sent with it are that
	// persona's.
	if body.ActivePersona != nil && *body.ActivePersona != cfg.ActivePersona {
		if p, err := s.store.Persona(*body.ActivePersona); err == nil {
			cfg.ActivePersona = p.ID
			cfg.PersonaName, cfg.PersonaDescription = p.Name, p.Description()
		}
	}
	if body.Persona != nil || body.PersonaNote != nil {
		if err := s.editPersona(&cfg, body.Persona, body.PersonaNote); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
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

// The phone edits the persona in use by default as a name and a note. The note
// is the persona's free field, Other Details on the PC; its age, race and the
// rest are edited there, where there is room for them.

// personaNote is what the phone shows as the note.
func (s *Server) personaNote(cfg store.Config) string {
	if cfg.ActivePersona != 0 {
		if p, err := s.store.Persona(cfg.ActivePersona); err == nil {
			return p.Details
		}
	}
	return cfg.PersonaDescription
}

// editPersona writes the phone's name and note into the persona in use by
// default, making one if there is none yet, and brings the settings' copy of
// it up to date.
func (s *Server) editPersona(cfg *store.Config, name, note *string) error {
	var p chars.Profile
	if cfg.ActivePersona != 0 {
		if found, err := s.store.Persona(cfg.ActivePersona); err == nil {
			p = found
		}
	}
	if name != nil {
		p.Name = strings.TrimSpace(*name)
	}
	if note != nil {
		p.Details = strings.TrimSpace(*note)
	}
	id, err := s.store.SavePersona(p)
	if err != nil {
		return err
	}
	cfg.ActivePersona = id
	cfg.PersonaName, cfg.PersonaDescription = p.Name, p.Description()
	return nil
}

// personaOut is one of your personas as the phone lists them.
type personaOut struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Note  string `json:"note"`
	Facts string `json:"facts,omitempty"`
}
