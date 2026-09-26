package serve

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"astral/internal/chars"
	"astral/internal/icons"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
)

//go:embed web
var webFiles embed.FS

// Server is Astral on the network: the library on this machine, and the models
// running on it, usable from a device that could not run them itself.
type Server struct {
	store  *store.Store
	config func() store.Config
	client func() *ollama.Client
	// save writes a changed config back where the window will see it, and
	// version is what the phone shows on its about screen.
	save    func(store.Config) error
	version string

	pair pairing

	mu   sync.Mutex
	http *http.Server
	port int
}

// New builds a server. Nothing listens until Start is called.
//
// config and client are read fresh on every request rather than captured,
// because the model, the persona and the server address can all be changed in
// settings while a phone is connected, and the phone should get what the window
// would get.
func New(st *store.Store, config func() store.Config, client func() *ollama.Client,
	save func(store.Config) error, version string) *Server {
	if save == nil {
		save = func(store.Config) error { return nil }
	}
	return &Server{store: st, config: config, client: client, save: save, version: version}
}

// Start begins listening. Starting an already-running server is not an error.
func (s *Server) Start(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.http != nil {
		return nil
	}
	if port <= 0 {
		port = store.DefaultPhonePort
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler: s.routes(),
		// A generation can take minutes, so there is no write timeout, but a
		// client that opens a connection and says nothing is not allowed to
		// hold one open for ever.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	s.http, s.port = srv, port
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("astral: phone access stopped: %v", err)
		}
	}()
	return nil
}

// Stop closes the server and ends any pairing in progress.
func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.http
	s.http = nil
	s.mu.Unlock()
	s.pair.close()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

// Running reports whether the server is listening, and on what port.
func (s *Server) Running() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port, s.http != nil
}

// OpenPairing starts a pairing and returns the code to show on screen.
func (s *Server) OpenPairing() (string, error) { return s.pair.open() }

// ClosePairing ends one early.
func (s *Server) ClosePairing() { s.pair.close() }

// PairingOpen returns the code being offered and how long it has left.
func (s *Server) PairingOpen() (string, time.Duration, bool) { return s.pair.current() }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// The one route that is deliberately open, because a device with no token
	// has no other way to get one. It is rate limited by the pairing itself.
	mux.HandleFunc("POST /api/pair", s.handlePair)

	mux.Handle("GET /api/state", s.guard(s.handleState))
	mux.Handle("GET /api/chats/{id}", s.guard(s.handleChat))
	mux.Handle("POST /api/chats", s.guard(s.handleNewChat))
	mux.Handle("POST /api/chats/{id}/send", s.guard(s.handleSend))
	mux.Handle("DELETE /api/chats/{id}", s.guard(s.handleDeleteChat))
	mux.Handle("GET /api/settings", s.guard(s.handleSettings))
	mux.Handle("POST /api/settings", s.guard(s.handleSaveSettings))
	mux.Handle("POST /api/forget", s.guard(s.handleForget))
	mux.Handle("GET /api/app/latest", s.guard(s.handleAppLatest))
	mux.Handle("GET /api/app/download", s.guard(s.handleAppDownload))

	// The same icon set the window draws with, served so the phone can use it
	// as a CSS mask and recolour it. One set, two clients.
	mux.Handle("GET /icons/", http.StripPrefix("/icons/", http.FileServer(http.FS(icons.FS()))))

	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Printf("astral: the phone interface is missing from this build: %v", err)
		return mux
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	return mux
}

// guard requires a paired device.
func (s *Server) guard(h func(http.ResponseWriter, *http.Request, store.Device)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		d, ok := s.store.DeviceByToken(strings.TrimSpace(token))
		if !ok {
			// 401 and nothing else. Which part was wrong is not the caller's
			// business, and a device that has been revoked should look exactly
			// like one that was never paired.
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not paired"})
			return
		}
		h(w, r, d)
	})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	if !s.pair.claim(body.Code) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "that code is not right, or it has expired"})
		return
	}
	token, err := newToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not make a token"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if len(name) > 60 {
		name = name[:60]
	}
	if _, err := s.store.AddDevice(name, token); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// handleState is everything the home screen needs, in one round trip. A phone
// on a home network is not slow, but it is a network, and five requests to draw
// one screen is five chances to be waiting.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request, d store.Device) {
	go s.store.TouchDevice(d.ID)
	cfg := s.config()

	type chatOut struct {
		ID       int64  `json:"id"`
		Title    string `json:"title"`
		Who      string `json:"who"`
		Accent   int    `json:"accent"`
		Messages int    `json:"messages"`
		Updated  int64  `json:"updated"`
	}
	out := struct {
		Persona    string        `json:"persona"`
		Model      string        `json:"model"`
		Chats      []chatOut     `json:"chats"`
		Characters []nameOut     `json:"characters"`
		Worlds     []nameOut     `json:"worlds"`
		Device     string        `json:"device"`
		Since      time.Duration `json:"-"`
	}{Persona: cfg.PersonaName, Model: cfg.Model, Device: d.Name}

	chats, err := s.store.Chats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, c := range chats {
		out.Chats = append(out.Chats, chatOut{
			ID: c.ID, Title: c.Title, Who: c.CharacterName, Accent: c.Accent,
			Messages: c.MessageCount, Updated: c.UpdatedAt.Unix(),
		})
	}
	if cs, err := s.store.Characters(); err == nil {
		for _, c := range cs {
			out.Characters = append(out.Characters, nameOut{ID: c.ID, Name: c.Name, Note: c.Description, Accent: c.Accent})
		}
	}
	if ws, err := s.store.Worlds(); err == nil {
		for _, wd := range ws {
			out.Worlds = append(out.Worlds, nameOut{ID: wd.ID, Name: wd.Name, Note: wd.Description})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type nameOut struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Note   string `json:"note"`
	Accent int    `json:"accent"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request, d store.Device) {
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
	msgs, err := s.store.Messages(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type msgOut struct {
		ID      int64  `json:"id"`
		Role    string `json:"role"`
		Content string `json:"content"`
		// Who is the speaker's name, on a scene with more than one character in
		// it. Empty everywhere else, so the page shows one name at the top as it
		// always did.
		Who    string `json:"who,omitempty"`
		Accent int    `json:"accent,omitempty"`
	}
	out := struct {
		ID       int64     `json:"id"`
		Title    string    `json:"title"`
		Who      string    `json:"who"`
		Accent   int       `json:"accent"`
		Kind     string    `json:"kind"`
		Cast     []nameOut `json:"cast,omitempty"`
		Messages []msgOut  `json:"messages"`
	}{ID: ch.ID, Title: ch.Title, Who: ch.CharacterName, Accent: ch.Accent, Kind: ch.Kind}
	cast := s.castFor(ch)
	tint := make(map[int64]int, len(cast))
	for _, member := range cast {
		out.Cast = append(out.Cast, nameOut{ID: member.ID, Name: member.Name, Accent: member.Accent})
		tint[member.ID] = member.Accent
	}
	nameOf := castNames(cast)
	for _, m := range msgs {
		o := msgOut{ID: m.ID, Role: m.Role, Content: m.Content}
		if nameOf != nil && m.Role == ollama.RoleAssistant {
			id := m.CharacterID
			if id == 0 && len(cast) > 0 {
				// An unattributed beat in a group is the first member, the same
				// answer the window gives: somebody said it, and that is who the
				// scene is named after.
				id = cast[0].ID
			}
			o.Who, o.Accent = nameOf(id), tint[id]
		}
		out.Messages = append(out.Messages, o)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleNewChat(w http.ResponseWriter, r *http.Request, d store.Device) {
	var body struct {
		CharacterID int64 `json:"character_id"`
		WorldID     int64 `json:"world_id"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)

	cfg := s.config()
	title, kind := "New chat", store.KindRoleplay
	switch {
	case body.CharacterID != 0:
		if ca, err := s.store.Character(body.CharacterID); err == nil {
			title = ca.Name
		}
	case body.WorldID != 0:
		if wd, err := s.store.World(body.WorldID); err == nil {
			title = wd.Name
		}
	default:
		kind = store.KindAssistant
		title = "General chat"
	}
	ch, err := s.store.NewChatIn(body.CharacterID, body.WorldID, title, cfg.Model, kind)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// The opening message, which the window shows and this did not: a phone
	// opened a character onto an empty screen, and the first reply had to
	// start a scene from nothing rather than answer one already begun.
	//
	// Saved straight away rather than held unsaved as the window does, because
	// the row already exists by this point: a phone asks for a chat and then
	// asks for its messages, and there is nowhere for an unsaved greeting to
	// live in between.
	if ca := s.characterFor(ch); ca.Name != "" {
		if g := chars.Greeting(ca, scene.Persona(cfg)); g != "" {
			if _, err := s.store.AddMessage(store.Message{
				ChatID: ch.ID, Role: ollama.RoleAssistant, Content: g,
			}); err != nil {
				log.Printf("astral: saving the greeting for chat %d: %v", ch.ID, err)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": ch.ID})
}

func (s *Server) handleDeleteChat(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a chat id"})
		return
	}
	if err := s.store.DeleteChat(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

// characterFor resolves who a chat is with: a character, the narrator of a
// world, or nobody.
// castFor is everyone in a scene, for a scene with more than one character in
// it. Empty otherwise, which is almost every scene: a two-hander records no cast
// and must keep behaving exactly as it did.
func (s *Server) castFor(ch store.Chat) []chars.Character {
	if ch.ID == 0 {
		return nil
	}
	cast, err := s.store.Cast(ch.ID)
	if err != nil {
		log.Printf("astral: reading the cast of chat %d: %v", ch.ID, err)
		return nil
	}
	if len(cast) < 2 {
		return nil
	}
	return cast
}

// castNames maps a speaker id to a name, for labelling a transcript on its way
// to the model and for showing who spoke on the phone.
func castNames(cast []chars.Character) func(int64) string {
	if len(cast) == 0 {
		return nil
	}
	byID := make(map[int64]string, len(cast))
	for _, c := range cast {
		byID[c.ID] = c.Name
	}
	return func(id int64) string { return byID[id] }
}

func (s *Server) characterFor(ch store.Chat) chars.Character {
	switch {
	case ch.CharacterID != 0:
		ca, _ := s.store.Character(ch.CharacterID)
		return ca
	case ch.WorldID != 0:
		if wd, err := s.store.World(ch.WorldID); err == nil {
			return scene.Narrator(wd)
		}
	}
	return chars.Character{}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Addresses lists the addresses this machine can be reached on, for showing
// next to the pairing code. Loopback is left out: it is the one address a
// phone cannot use.
func Addresses(port int) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			out = append(out, fmt.Sprintf("http://%s:%d", ipnet.IP.String(), port))
		}
	}
	return out
}
