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
	"os"
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
	// busy stops two generations running on one chat: the window and a phone
	// can both be in the same scene, and two replies written into it at once
	// interleave in the transcript and load the model twice for one turn.
	busy busyChats

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
	mux.Handle("GET /api/chats/{id}/portrait", s.guard(s.handlePortrait))
	mux.Handle("GET /api/characters/{id}/avatar", s.guard(s.handleAvatar))
	mux.Handle("GET /api/search", s.guard(s.handleSearch))
	mux.Handle("POST /api/chats", s.guard(s.handleNewChat))
	mux.Handle("POST /api/chats/{id}/send", s.guard(s.handleSend))
	mux.Handle("POST /api/chats/{id}/regenerate", s.guard(s.handleRegenerate))
	mux.Handle("POST /api/chats/{id}/stop", s.guard(s.handleStop))
	mux.Handle("DELETE /api/chats/{id}/messages/{mid}", s.guard(s.handleDeleteMessage))
	mux.Handle("POST /api/chats/{id}/messages/{mid}/version", s.guard(s.handleVersion))
	mux.Handle("POST /api/chats/{id}/persona", s.guard(s.handleChatPersona))
	mux.Handle("POST /api/chats/{id}/draft", s.guard(s.handleDraft))
	mux.Handle("POST /api/chats/{id}/branch", s.guard(s.handleBranch))
	mux.Handle("GET /api/chats/{id}/memory", s.guard(s.handleMemory))
	mux.Handle("POST /api/chats/{id}/memory", s.guard(s.handleSaveMemory))
	mux.Handle("POST /api/chats/{id}/messages/{mid}/pin", s.guard(s.handlePin))
	mux.Handle("POST /api/chats/{id}/messages/{mid}/rewrite", s.guard(s.handleRewriteMine))
	mux.Handle("POST /api/chats/{id}/messages/{mid}/hide", s.guard(s.handleHide))
	mux.Handle("POST /api/chats/{id}/suggest", s.guard(s.handleSuggest))
	mux.Handle("POST /api/chats/{id}/setting/suggest", s.guard(s.handleSuggestSetting))
	mux.Handle("POST /api/characters/{id}/favorite", s.guard(s.handleFavorite))
	mux.Handle("POST /api/characters/import", s.guard(s.handleImportLink))
	mux.Handle("DELETE /api/chats/{id}", s.guard(s.handleDeleteChat))
	mux.Handle("DELETE /api/characters/{id}", s.guard(s.handleDeleteCharacter))
	mux.Handle("DELETE /api/worlds/{id}", s.guard(s.handleDeleteWorld))
	mux.Handle("GET /api/settings", s.guard(s.handleSettings))
	mux.Handle("POST /api/settings", s.guard(s.handleSaveSettings))
	mux.Handle("POST /api/forget", s.guard(s.handleForget))
	mux.Handle("GET /api/app/latest", s.guard(s.handleAppLatest))
	mux.Handle("GET /api/app/download", s.guard(s.handleAppDownload))

	// The same icon set the window draws with, served so the phone can use it
	// as a CSS mask and recolour it. One set, two clients.
	mux.Handle("GET /icons/", http.StripPrefix("/icons/", newStatic(icons.FS())))

	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Printf("astral: the phone interface is missing from this build: %v", err)
		return mux
	}
	mux.Handle("GET /", newStatic(sub))
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
	name := deviceName(body.Name)
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
		// Character is who the chat is with, so the phone can show their
		// picture on the row.
		Character int64 `json:"character,omitempty"`
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
			Messages: c.MessageCount, Updated: c.UpdatedAt.Unix(), Character: c.CharacterID,
		})
	}
	if cs, err := s.store.Characters(); err == nil {
		for _, c := range cs {
			out.Characters = append(out.Characters, nameOut{ID: c.ID, Name: c.Name,
				Note: chars.Substitute(c.Description, c.Name, cfg.PersonaName), Accent: c.Accent,
				Avatar: pictureOf(c) != "", Favorite: c.Favorite})
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
	// Avatar says a character has a picture, at /api/characters/{id}/avatar.
	Avatar bool `json:"avatar,omitempty"`
	// Favorite characters are listed first and marked.
	Favorite bool `json:"favorite,omitempty"`
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
		// Versions is how many replies were written for this turn, when more
		// than one was, and Version which of them is showing.
		Versions int `json:"versions,omitempty"`
		Version  int `json:"version,omitempty"`
		// Pinned messages are kept in mind however long the scene grows.
		Pinned bool `json:"pinned,omitempty"`
		// Hidden messages are shown here and never sent to the model.
		Hidden bool `json:"hidden,omitempty"`
	}
	out := struct {
		ID       int64     `json:"id"`
		Title    string    `json:"title"`
		Who      string    `json:"who"`
		Accent   int       `json:"accent"`
		Kind     string    `json:"kind"`
		Cast     []nameOut `json:"cast,omitempty"`
		Messages []msgOut  `json:"messages"`
		// Writing says a reply is still being written into this chat, so a
		// phone that dropped its connection mid-reply knows to wait for it.
		Writing bool `json:"writing"`
		// Portrait says the chat's character has a portrait, which the phone
		// sets behind the conversation; the picture itself is at
		// /api/chats/{id}/portrait.
		Portrait bool `json:"portrait,omitempty"`
		// PersonaID and Persona are who you play as in this chat: its own
		// persona, or the one in use by default.
		PersonaID int64  `json:"persona_id,omitempty"`
		Persona   string `json:"persona,omitempty"`
		// CanDraft says Write for Me belongs here, and Remembers that the chat
		// keeps a record and pins, so the phone offers only what works.
		CanDraft  bool `json:"can_draft,omitempty"`
		Remembers bool `json:"remembers,omitempty"`
		// Character is who a one-on-one scene is with, and Favorite whether
		// they are one of your favorites, for the chat's menu.
		Character int64 `json:"character,omitempty"`
		Favorite  bool  `json:"favorite,omitempty"`
	}{ID: ch.ID, Title: ch.Title, Who: ch.CharacterName, Accent: ch.Accent, Kind: ch.Kind,
		Writing: s.busy.writing(id), Portrait: s.portraitOf(ch) != ""}
	out.CanDraft = scene.CanDraft(ch, castFor(s.castFor(ch), s.characterFor(ch)))
	out.Remembers = remembers(ch, s.characterFor(ch))
	if ch.CharacterID != 0 {
		if ca, err := s.store.Character(ch.CharacterID); err == nil {
			out.Character, out.Favorite = ca.ID, ca.Favorite
		}
	}
	if pid := ch.PersonaID; pid != 0 || s.config().ActivePersona != 0 {
		if pid == 0 {
			pid = s.config().ActivePersona
		}
		if p, err := s.store.Persona(pid); err == nil {
			out.PersonaID, out.Persona = p.ID, p.DisplayName()
		}
	}
	cast := s.castFor(ch)
	tint := make(map[int64]int, len(cast))
	for _, member := range cast {
		out.Cast = append(out.Cast, nameOut{ID: member.ID, Name: member.Name, Accent: member.Accent})
		tint[member.ID] = member.Accent
	}
	nameOf := castNames(cast)
	for _, m := range msgs {
		// A file sent from the PC with a message shows as its name, as it
		// does there.
		o := msgOut{ID: m.ID, Role: m.Role, Content: chars.HideAttachedFiles(m.Content)}
		if len(m.Versions) > 1 {
			o.Versions, o.Version = len(m.Versions), m.Version
		}
		o.Pinned, o.Hidden = m.Pinned, m.Hidden
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

// portraitOf is the file holding the portrait of a chat's character, or ""
// when it has none or the file has gone.
func (s *Server) portraitOf(ch store.Chat) string {
	if ch.CharacterID == 0 {
		return ""
	}
	c, err := s.store.Character(ch.CharacterID)
	if err != nil || c.PortraitPath == "" {
		return ""
	}
	if _, err := os.Stat(c.PortraitPath); err != nil {
		return ""
	}
	return c.PortraitPath
}

// handlePortrait sends the portrait of a chat's character. Only a path the
// database holds is ever read, never one from the request.
func (s *Server) handlePortrait(w http.ResponseWriter, r *http.Request, d store.Device) {
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
	path := s.portraitOf(ch)
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no portrait"})
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, path)
}

func (s *Server) handleNewChat(w http.ResponseWriter, r *http.Request, d store.Device) {
	var body struct {
		CharacterID int64 `json:"character_id"`
		WorldID     int64 `json:"world_id"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)

	cfg := s.config()
	title, kind := "New Chat", store.KindRoleplay
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
		title = "General Chat"
	}
	ch, err := s.store.NewChatIn(body.CharacterID, body.WorldID, title, cfg.Model, kind)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Played as whoever is in use by default, as a chat started on the PC is.
	if cfg.ActivePersona != 0 {
		if err := s.store.SetChatPersona(ch.ID, cfg.ActivePersona); err == nil {
			ch.PersonaID = cfg.ActivePersona
		}
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
		if g := chars.Greeting(ca, scene.Persona(s.playedAs(cfg, ch))); g != "" {
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

// handleDeleteMessage removes one turn from a scene.
//
// The chat is named in the path as well as the message, and the message has to
// belong to it. Without that, a device could delete any turn in any scene by
// guessing an id, which is a smaller thing than it sounds only because every
// paired device is already trusted; saying which chat you meant costs nothing
// and makes the check possible.
func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request, d store.Device) {
	chatID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a chat id"})
		return
	}
	msgID, err := strconv.ParseInt(r.PathValue("mid"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a message id"})
		return
	}
	// Not while a reply is being written into the same scene: the turn being
	// deleted may be part of the prompt that reply was built from.
	release, free := s.busy.claim(chatID)
	if !free {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this chat is writing a reply"})
		return
	}
	defer release()

	if err := s.store.DeleteMessageIn(chatID, msgID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

// handleDeleteCharacter removes a character.
//
// The scenes already played with them are kept, the same as on the desktop: a
// transcript is yours, and losing one because you tidied up the cast is not a
// trade anybody would choose.
func (s *Server) handleDeleteCharacter(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a character id"})
		return
	}
	if err := s.store.DeleteCharacter(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

// handleDeleteWorld removes a world and its lorebook. Characters that belonged to
// it are kept and lose their setting, which is what the store does.
func (s *Server) handleDeleteWorld(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a world id"})
		return
	}
	if err := s.store.DeleteWorld(id); err != nil {
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

// deviceName is the name a phone paired under, kept to sixty characters. Cut
// at a word, in characters rather than bytes: it was cut at the sixtieth byte,
// which left "the cracked screen prote" on Settings and could split a letter
// that takes several bytes, an emoji in a phone's name, in half.
func deviceName(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	const most = 60
	if len(r) <= most {
		return s
	}
	cut := string(r[:most])
	if i := strings.LastIndexByte(cut, ' '); i > most/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

// handleSearch finds chats by anything said in them, with the line that
// matched, as the window's search box does. The phone's box filtered titles
// only, so a scene could not be found by what happened in it.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, d store.Device) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	type hitOut struct {
		ID      int64  `json:"id"`
		Snippet string `json:"snippet"`
	}
	out := []hitOut{}
	if q != "" {
		hits, err := s.store.SearchChats(q, 60)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, h := range hits {
			// The markers around the matching words are for the window's
			// bold; the phone shows the line as it is.
			snip := strings.NewReplacer("\x01", "", "\x02", "", "*", "").Replace(h.Snippet)
			out = append(out, hitOut{ID: h.ChatID, Snippet: strings.TrimSpace(snip)})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleVersion shows another of the replies written for a turn, as the
// window's arrows under a reply do. Refused while a reply is being written
// into the chat, since the turn may be the one being replaced.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request, d store.Device) {
	chatID, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	msgID, err2 := strconv.ParseInt(r.PathValue("mid"), 10, 64)
	var body struct {
		At int `json:"at"`
	}
	err3 := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body)
	if err1 != nil || err2 != nil || err3 != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	if s.busy.writing(chatID) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a reply is being written"})
		return
	}
	msgs, err := s.store.Messages(chatID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return
	}
	count := 0
	for _, m := range msgs {
		if m.ID == msgID {
			count = len(m.Versions)
		}
	}
	if count < 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "that reply has one version"})
		return
	}
	v, err := s.store.SetMessageVersion(msgID, body.At)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": chars.HideAttachedFiles(v.Content), "version": body.At, "versions": count})
}

// handleChatPersona sets who you play as in one chat, as the chip beside the
// window's model button does.
func (s *Server) handleChatPersona(w http.ResponseWriter, r *http.Request, d store.Device) {
	chatID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var body struct {
		PersonaID int64 `json:"persona_id"`
	}
	if err != nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	p, err := s.store.Persona(body.PersonaID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such persona"})
		return
	}
	if _, err := s.store.Chat(chatID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such chat"})
		return
	}
	if err := s.store.SetChatPersona(chatID, p.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"persona_id": p.ID, "persona": p.DisplayName()})
}
