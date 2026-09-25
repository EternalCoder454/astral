// Package app wires the AdwApplication, the window, and the storage / model
// services together.
package app

import (
	"context"
	"log"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/ui"
)

// appID is all-lowercase so the Wayland app-id matches the .desktop file's
// basename exactly — GNOME takes the dock name and icon from that match.
const appID = "io.github.astral"

// Assets are the embedded stylesheets handed in from main.
type Assets struct {
	Style string
	Dark  string
	Light string
}

// App is the top-level controller. It owns the long-lived services and the
// references the panels need to reach each other.
type App struct {
	adw    *adw.Application
	assets Assets

	cfg    store.Config
	store  *store.Store
	client *ollama.Client
	theme  *themer

	win     *adw.ApplicationWindow
	split   *adw.OverlaySplitView
	toasts  *adw.ToastOverlay
	stack   *gtk.Stack
	title   *adw.WindowTitle
	sideBtn *gtk.ToggleButton

	// The character portrait, on the far side of the chat.
	portraitSplit *adw.OverlaySplitView
	portraitBox   *gtk.Box
	portraitBtn   *gtk.ToggleButton

	sidebar    *ui.Sidebar
	chat       *ui.ChatView
	welcome    *gtk.Widget
	welcomeBox *gtk.Box

	// models is the last known result of probing Ollama. Empty means either
	// nothing is installed or the server is down; probeErr tells them apart.
	models   []ollama.Model
	probeErr error

	// fontMode is the text-rendering mode currently applied (see fonts.go).
	fontMode string

	// dbRecovered records that the database was damaged and replaced, so the
	// window can say so once it exists.
	dbRecovered bool
}

// New constructs the application without starting the main loop.
func New(assets Assets) *App {
	// Astral is single-instance: launching it again raises the window that is
	// already open. Capture runs opt out, so they neither hand off to nor
	// disturb a copy you happen to be using.
	flags := gio.ApplicationFlagsNone
	if devRun() {
		flags = gio.ApplicationNonUnique
	}
	return &App{
		adw:    adw.NewApplication(appID, flags),
		assets: assets,
	}
}

// Run initializes resources and runs the GTK main loop, returning the exit code.
func (a *App) Run(args []string) int {
	a.adw.ConnectActivate(a.activate)
	a.adw.ConnectShutdown(a.shutdown)
	return a.adw.Run(args)
}

func (a *App) activate() {
	cfg, err := store.LoadConfig()
	if err != nil {
		log.Printf("astral: load config: %v", err)
	}
	a.cfg = cfg

	st, recovered, err := store.Open(store.DefaultDBPath())
	if err != nil {
		log.Printf("astral: open database: %v", err)
	}
	a.store = st
	a.dbRecovered = recovered

	a.client = ollama.NewClient(cfg.BaseURL)
	a.client.KeepAlive = cfg.KeepAlive

	// Before the window is built: the header bar asks for these by name while
	// it is being constructed.
	installIcons()

	a.theme = newThemer(a.assets.Style, a.assets.Dark, a.assets.Light)
	a.theme.install(a.assets.Style)
	a.theme.apply(a.cfg.Theme)
	a.theme.watchSystem(func() string { return a.cfg.Theme })

	a.applyFontRendering()
	a.buildWindow()
	a.win.SetVisible(true)
	a.applyFontRendering() // again, now that the window's own display is known
	a.watchScaleChanges()

	if a.dbRecovered {
		a.toast("Your database could not be read and was replaced. The damaged copy was kept alongside it.")
	}

	// Everything below happens after the window is up, so the first frame is
	// not waiting on a network round trip to a server that may not be running.
	a.refreshSidebar()
	a.restoreLastChat()
	a.probeModels()
	a.runDevView()
}

func (a *App) shutdown() {
	a.rememberLayout()
	if a.chat != nil {
		a.chat.Stop()
		if ch := a.chat.Chat(); ch.ID != 0 {
			a.cfg.LastChat = ch.ID
		}
	}
	if err := store.SaveConfig(a.cfg); err != nil {
		log.Printf("astral: save config: %v", err)
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			log.Printf("astral: close database: %v", err)
		}
	}
}

// rememberLayout stores the window geometry so the next launch opens the way
// you left it.
func (a *App) rememberLayout() {
	if a.win == nil {
		return
	}
	// A run with a forced geometry must not write it back: ASTRAL_DEV_SIZE
	// exists to make capture runs reproducible, and it would defeat itself by
	// leaving its own size in the config for the next ordinary launch.
	if _, _, forced := devWindowSize(); !forced {
		if w, h := a.win.DefaultSize(); w > 0 && h > 0 {
			a.cfg.WindowWidth, a.cfg.WindowHeight = w, h
		}
	}
	if a.split != nil {
		a.cfg.SidebarOpen = a.split.ShowSidebar()
	}
}

// probeModels asks Ollama what it has, on a goroutine, and updates the picker.
func (a *App) probeModels() {
	client := a.client
	go func() {
		models, err := client.Probe(context.Background())
		coreglib.IdleAdd(func() bool {
			a.models, a.probeErr = models, err
			a.onModelsChanged()
			return false
		})
	}()
}

// onModelsChanged reconciles the configured model with what is installed.
func (a *App) onModelsChanged() {
	// Picking the first installed model when none is configured means a fresh
	// install is usable immediately, instead of showing an empty picker and
	// asking you to know the name of something you have pulled.
	if a.cfg.Model == "" && len(a.models) > 0 {
		a.cfg.Model = a.models[0].Name
		_ = store.SaveConfig(a.cfg)
	}
	if a.chat != nil {
		a.chat.SetConfig(a.cfg)
	}
	if a.sidebar != nil {
		a.sidebar.SetProfile(a.cfg.PersonaName, a.cfg.PersonaDescription)
	}
	a.refreshWelcome()
}

// ready reports whether a turn can actually be sent right now.
//
// The model being *configured* is not enough — it has to still be installed.
// A model pulled once and later removed leaves a name in the config that looks
// fine everywhere until a send fails with a 404 from the server, which is not
// a message anyone can act on.
func (a *App) ready() bool {
	return a.probeErr == nil && len(a.models) > 0 && ollama.HasModel(a.models, a.cfg.Model)
}

// refreshSidebar reloads the conversation list.
func (a *App) refreshSidebar() {
	if a.store == nil || a.sidebar == nil {
		return
	}
	chats, err := a.store.Chats()
	if err != nil {
		log.Printf("astral: list chats: %v", err)
		return
	}
	a.sidebar.SetChats(chats)
	if a.chat != nil {
		a.sidebar.Select(a.chat.Chat().ID)
	}
	a.sidebar.SetProfile(a.cfg.PersonaName, a.cfg.PersonaDescription)
}

// restoreLastChat reopens whatever you were reading when you closed the app.
func (a *App) restoreLastChat() {
	if a.store == nil || a.cfg.LastChat == 0 {
		a.showWelcome()
		return
	}
	if err := a.openChat(a.cfg.LastChat); err != nil {
		// The chat was deleted since last launch, which is not worth a message.
		a.showWelcome()
	}
}

// openChat loads a conversation into the centre panel.
func (a *App) openChat(id int64) error {
	ch, err := a.store.Chat(id)
	if err != nil {
		return err
	}
	var ca chars.Character
	if ch.CharacterID != 0 {
		// A character deleted out from under an old chat is not an error: the
		// transcript is still readable, it just has no persona to continue with.
		ca, _ = a.store.Character(ch.CharacterID)
	}
	msgs, err := a.store.Messages(id)
	if err != nil {
		return err
	}
	a.chat.LoadChat(ch, ca, msgs)
	a.showPortraitFor(ca)
	a.refreshAttachAvailability()
	a.showChat()
	a.sidebar.Select(id)
	a.setTitle(ch, ca)
	a.cfg.LastChat = id
	return nil
}

// newChat clears the centre panel for a fresh conversation with a character.
// Nothing is written until the first message is sent.
func (a *App) newChat(ca chars.Character) {
	a.chat.Clear()
	a.chat.LoadChat(store.Chat{Model: a.cfg.Model, CharacterID: ca.ID}, ca, nil)
	a.showPortraitFor(ca)
	if g := chars.Greeting(ca, a.persona()); g != "" {
		a.chat.ShowGreeting(g)
	}
	a.showChat()
	a.sidebar.Select(0)
	a.setTitle(store.Chat{}, ca)
	a.chat.FocusComposer()
}

func (a *App) persona() chars.Persona {
	return chars.Persona{
		Name:               a.cfg.PersonaName,
		Description:        a.cfg.PersonaDescription,
		GlobalInstructions: a.cfg.GlobalInstructions,
		Style:              a.cfg.Style(),
	}
}

// ollamaClientFor builds a client for a base URL. A helper rather than a bare
// constructor call so settings and startup cannot drift apart.
func ollamaClientFor(baseURL string) *ollama.Client { return ollama.NewClient(baseURL) }

// toast shows a transient message.
func (a *App) toast(msg string) {
	if a.toasts == nil {
		log.Printf("astral: %s", msg)
		return
	}
	t := adw.NewToast(msg)
	t.SetTimeout(6)
	a.toasts.AddToast(t)
}
