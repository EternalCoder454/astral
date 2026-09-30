// Package app wires the AdwApplication, the window, and the storage / model
// services together.
package app

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/serve"
	"astral/internal/store"
	"astral/internal/ui"
)

// appID is all-lowercase so the Wayland app-id matches the .desktop file's
// basename exactly, GNOME takes the dock name and icon from that match.
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

	win   *adw.ApplicationWindow
	split *adw.OverlaySplitView
	// sideBP and portraitBP collapse the sidebar and the portrait when the
	// window gets too narrow for them; their widths follow the sidebar's.
	sideBP, portraitBP *adw.Breakpoint
	toasts             *adw.ToastOverlay
	// page is the layer the welcome screen and the chats are drawn on, inside
	// the frame, and brand the name and version at the start of the title bar.
	page         *adw.Bin
	brand        *gtk.Box
	brandVersion *gtk.Label
	// tidyAt is when memory is next handed back, and tidyWaiting whether a
	// timer is set for it. See scheduleTidy.
	tidyAt      time.Time
	tidyWaiting bool
	stack       *gtk.Stack
	// phone is the server another device on this network talks to. Nil until
	// the setting is switched on; see phone.go.
	phone   *serve.Server
	title   *adw.WindowTitle
	sideBtn *gtk.ToggleButton

	// The character portrait, on the far side of the chat.
	portraitSplit *adw.OverlaySplitView
	// portraitHas says the open chat has somebody to show in the portrait
	// panel, so widening a narrow window knows whether to open it.
	portraitHas bool
	portraitBox *gtk.Box
	portraitBtn *gtk.ToggleButton
	offlineBtn  *gtk.Button

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

	// batch is the latest run of Optimize All, running or finished.
	batch *promptBatch

	// deleting is chats taken off the list whose toast still offers to put
	// them back; see deletechats.go.
	deleting map[int64]bool
	// continuing is set while a chat's story is being written for its
	// continuation, so a second press does not start another. See
	// continueChat.
	continuing bool
	// writingFirst is set while a character is being asked to write first,
	// and writeFirstHeld is the chats left alone for a while after a try
	// that failed. See writefirst.go.
	writingFirst   bool
	writeFirstHeld map[int64]time.Time

	// pendingRestore is a backup to put back once the library is closed, on
	// the way out; see restore.go.
	pendingRestore string
}

// New constructs the application without starting the main loop.
func New(assets Assets) *App {
	// Astral is single-instance: launching it again raises the window that is
	// already open. Capture runs opt out, so they neither hand off to nor
	// disturb a copy you happen to be using.
	flags := gio.ApplicationDefaultFlags
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
	// A second launch, or a click on one of Astral's notifications, activates
	// the copy that is already running. It raises the window it has. Running
	// the rest of this again opened the database a second time and built a
	// second window over the first.
	if a.win != nil {
		a.win.Present()
		return
	}
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
	// Before anything is built or sent: every prompt is read through the
	// prompts package, which has to know your versions first.
	a.loadPromptOverrides()
	// And who you are, which the persona you had before there could be
	// several becomes the first of.
	a.migratePersonas()

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

	a.announceRestore()
	if a.dbRecovered {
		a.toast("Your database was unreadable, so it was replaced and the damaged copy kept.")
	}

	// Everything below happens after the window is up, so the first frame is
	// not waiting on a network round trip to a server that may not be running.
	a.runDevSeed()
	a.refreshSidebar()
	a.refreshPersonaMenu()
	a.showWelcome()
	a.probeModels()
	a.maybeCheckForUpdate()
	if !devRun() {
		a.startWritesFirst()
	}
	if a.cfg.PhoneAccess {
		a.startPhoneAccess()
	}
	a.runDevView()
	if !devRun() {
		go keepBackingUp(a.store)
	}
}

// keepBackingUp makes the day's copy of the library, and looks again every
// hour, so a window left open for a week still makes one a day. See
// store.BackupDaily.
func keepBackingUp(st *store.Store) {
	if st == nil {
		return
	}
	for {
		if path, err := st.BackupDaily(store.BackupDir(), time.Now()); err != nil {
			log.Printf("astral: daily backup: %v", err)
		} else if path != "" {
			log.Printf("astral: backed up to %s", path)
		}
		time.Sleep(time.Hour)
	}
}

func (a *App) shutdown() {
	a.stopPhoneAccess()
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
	a.commitAllDeletes()
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			log.Printf("astral: close database: %v", err)
		}
	}
	a.finishRestore()
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
		a.cfg.WindowMaximized = a.win.IsMaximized()
	}
	// Only what you chose with room to choose it: closing the window while
	// it is narrow, with the sidebar folded away, must not close it for good.
	if a.split != nil && !a.split.Collapsed() {
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
		a.refreshProfile()
	}
	a.refreshOfflineChip()
	a.refreshWelcome()
}

// refreshOfflineChip shows or hides the header warning.
//
// The state comes from real traffic rather than from polling: the startup
// probe sets it, and every turn that fails or succeeds updates it. Polling a
// local server on a timer to light a lamp nobody asked for is work the machine
// can do without.
func (a *App) refreshOfflineChip() {
	if a.offlineBtn == nil {
		return
	}
	switch {
	case a.probeErr != nil:
		a.offlineBtn.SetTooltipText("Start Ollama at " + a.cfg.BaseURL + ", then click here to check again.")
		a.offlineBtn.SetVisible(true)
	case len(a.models) == 0:
		a.offlineBtn.SetTooltipText("Pull a model with `ollama pull qwen3:8b`, then click here to check again.")
		a.offlineBtn.SetVisible(true)
	case !ollama.HasModel(a.models, a.cfg.Model):
		a.offlineBtn.SetTooltipText(a.cfg.Model + " is no longer installed, so choose another or pull it back.")
		a.offlineBtn.SetVisible(true)
	default:
		a.offlineBtn.SetVisible(false)
	}
}

// noteTurnFailed re-checks the server after a turn could not be sent, so the
// header catches up with what just happened rather than waiting for a restart.
func (a *App) noteTurnFailed(msg string) {
	if strings.Contains(msg, "Ollama isn't running") || strings.Contains(msg, "cannot reach Ollama") {
		a.probeModels()
	}
}

// ready reports whether a turn can actually be sent right now.
//
// The model being *configured* is not enough, it has to still be installed.
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
	chats, err := a.visibleChats()
	if err != nil {
		log.Printf("astral: list chats: %v", err)
		return
	}
	a.sidebar.SetChats(chats)
	// The open chat is marked only while it is what the window shows. Home, or
	// a page, is marked instead then, and the chat left behind is not to be
	// marked again by the next rename or archive of some other chat.
	if a.chat != nil && a.chatShowing() {
		a.sidebar.Select(a.chat.Chat().ID)
	}
	a.refreshProfile()
	a.refreshNavCounts()
}

// refreshNavCounts puts the number of characters and worlds beside their
// rows in the sidebar.
func (a *App) refreshNavCounts() {
	if a.store == nil || a.sidebar == nil {
		return
	}
	// A count that cannot be read is passed on as -1, which leaves the row as it
	// was rather than showing a library as empty because one query failed.
	characters, err := a.store.CountCharacters()
	if err != nil {
		log.Printf("astral: counting characters: %v", err)
		characters = -1
	}
	worlds, err := a.store.CountWorlds()
	if err != nil {
		log.Printf("astral: counting worlds: %v", err)
		worlds = -1
	}
	a.sidebar.SetCounts(characters, worlds)
}

// Astral opens on Home, always.
//
// It used to reopen whatever you were reading when you closed it, which sounds
// helpful and is not: you are dropped into the middle of a scene with no idea
// how you got there, and the one thing you cannot do from a transcript is
// decide what to do next. LastChat is still recorded, because the sidebar uses
// it to mark where you were.

// openChat loads a conversation into the centre panel.
func (a *App) openChat(id int64) error {
	ch, err := a.store.Chat(id)
	if err != nil {
		return err
	}
	var ca chars.Character
	switch {
	case ch.CharacterID != 0:
		// A character deleted out from under an old chat is not an error: the
		// transcript is still readable, it just has no persona to continue with.
		ca, _ = a.store.Character(ch.CharacterID)
	case ch.WorldID != 0:
		// A scene in a world with nobody in particular. Its narrator is not
		// stored, because it is not a character anyone wrote: it is rebuilt
		// from the world every time the chat is opened, so editing the world
		// changes the scenes already running in it.
		if w, err := a.store.World(ch.WorldID); err == nil {
			ca = narratorFor(w)
		}
	}
	msgs, err := a.store.Messages(id)
	if err != nil {
		return err
	}
	// Whoever else is in it. A scene with one character has no cast rows, so
	// this is empty for almost every conversation and the ordinary path runs
	// unchanged.
	cast, err := a.store.Cast(id)
	if err != nil {
		log.Printf("astral: reading the cast of chat %d: %v", id, err)
	}
	// A scene in a world is played by the place as well as by anyone in it, so
	// the narrator leads the cast. It is not in the stored rows because it is not
	// a character anyone wrote.
	if ch.WorldID != 0 && len(cast) > 0 && ca.Name != "" {
		cast = append([]chars.Character{ca}, cast...)
	}
	if len(cast) > 1 {
		a.chat.LoadScene(ch, cast, msgs)
		ca = cast[0]
	} else {
		a.chat.LoadChat(ch, ca, msgs)
	}
	a.showPortraitFor(ca)
	a.refreshAttachAvailability()
	a.showChat()
	a.sidebar.Select(id)
	a.setTitle(ch, ca)
	a.cfg.LastChat = id
	a.scheduleTidy()
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
		GlobalInstructions: a.cfg.RulesText(),
		Style:              a.cfg.Style(),
	}
}

// ollamaClientFor builds a client for a base URL. A helper rather than a bare
// constructor call so settings and startup cannot drift apart.
func ollamaClientFor(baseURL string) *ollama.Client { return ollama.NewClient(baseURL) }

// toastAction shows a transient message with a button, for something just
// done that the person may want to go and look at.
func (a *App) toastAction(msg, label string, onClick func()) {
	if a.toasts == nil {
		log.Printf("astral: %s", msg)
		return
	}
	t := adw.NewToast(msg)
	t.SetUseMarkup(false) // a chat's title or an error's text, not markup
	t.SetTimeout(6)
	t.SetButtonLabel(label)
	t.ConnectButtonClicked(onClick)
	a.toasts.AddToast(t)
}

// toast shows a transient message.
func (a *App) toast(msg string) {
	if a.toasts == nil {
		log.Printf("astral: %s", msg)
		return
	}
	t := adw.NewToast(msg)
	t.SetUseMarkup(false) // a chat's title or an error's text, not markup
	t.SetTimeout(6)
	a.toasts.AddToast(t)
}
