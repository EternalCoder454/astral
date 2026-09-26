package app

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/imageconv"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/ui"
)

// Astral ships a small harness for screenshots and manual inspection. None of
// it runs unless an environment variable asks for it.
//
// The important part is devRun: without it a capture run would hand off to the
// copy of Astral you already have open — single-instance behaviour doing
// exactly what it should — and screenshot the wrong process.

const devCaptureTitle = "Astral Dev Capture"

var (
	// fixedWindowTitle pins the window title so an external capture tool can
	// match on it, and cannot match your real window by accident.
	fixedWindowTitle = os.Getenv("ASTRAL_DEV_TITLE") != ""
	devView          = os.Getenv("ASTRAL_DEV_VIEW")
	devSize          = os.Getenv("ASTRAL_DEV_SIZE")
)

// Astral cannot screenshot itself, and the reason is worth recording so it is
// not attempted again: rendering a widget's snapshot to a texture needs
// gsk_renderer_render_texture, and the gotk4 binding passes the GskRenderNode
// through InternObject — which assumes a GObject. GskRenderNode is not one, so
// GSK receives a bogus pointer and returns NULL for any node at all, including
// a trivially valid one. Nothing in this package can work around that.
//
// So captures are taken from outside: ASTRAL_DEV_TITLE pins a matchable
// window title, ASTRAL_DEV_SIZE fixes the geometry so runs are reproducible,
// and ASTRAL_DEV_VIEW opens a particular surface. Any screenshot tool that can
// target a window by title does the rest.

// devRun reports whether this process is a development run, and so should opt
// out of single-instance behaviour.
func devRun() bool {
	if fixedWindowTitle || devView != "" || devSize != "" {
		return true
	}
	return os.Getenv("ASTRAL_DEV") != ""
}

// devWindowSize returns the size from ASTRAL_DEV_SIZE ("1400x900"), so capture
// runs are reproducible without depending on whatever geometry your config
// happens to hold.
func devWindowSize() (int, int, bool) {
	w, h, ok := strings.Cut(devSize, "x")
	if !ok {
		return 0, 0, false
	}
	wi, err1 := strconv.Atoi(strings.TrimSpace(w))
	hi, err2 := strconv.Atoi(strings.TrimSpace(h))
	if err1 != nil || err2 != nil || wi < 320 || hi < 240 {
		return 0, 0, false
	}
	return wi, hi, true
}

// runDevView opens a named surface shortly after startup, so a screenshot can
// be taken of something other than the window's opening state.
func (a *App) runDevView() {
	if devView == "" {
		return
	}
	// A delay rather than an idle callback: some of these surfaces are dialogs
	// presented over the window, and they need the window mapped first.
	coreglib.TimeoutAdd(400, func() bool {
		// Split first, then lowercase only the name: the argument can be a
		// filesystem path, and lowercasing the whole value turned one into a
		// path that does not exist.
		name, arg, _ := strings.Cut(devView, "=")
		name = strings.ToLower(name)
		switch name {
		case "welcome":
			a.showWelcome()
		case "characters":
			a.showCharacters()
		case "newchat":
			a.showNewChat()
		case "designer":
			a.newDesignerChat()
		case "styles":
			a.showStyles()
		case "worlds":
			a.showWorlds()
		case "world":
			if ws, err := a.store.Worlds(); err == nil && len(ws) > 0 {
				a.showWorld(ws[0])
			}
		case "lorebook":
			if ws, err := a.store.Worlds(); err == nil && len(ws) > 0 {
				a.showLorebook(ws[0])
			}
		case "editchar":
			if cs, err := a.store.Characters(); err == nil && len(cs) > 0 {
				a.editCharacter(cs[0])
			}
		case "portrait":
			if cs, err := a.store.Characters(); err == nil && len(cs) > 0 {
				for _, c := range cs {
					if c.PortraitPath != "" {
						a.newChat(c)
						a.cfg.PortraitOpen = true
						a.showPortraitFor(c)
						break
					}
				}
			}
		case "styledesigner":
			a.newStyleDesignerChat()
		case "assistant":
			a.newAssistantChat()
		case "settings":
			a.showSettings()
		case "shortcuts":
			a.showShortcuts()
		case "about":
			a.showAbout()
		case "model":
			a.showModelPicker()
		case "chat":
			if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
				_ = a.openChat(id)
			}
		case "scene":
			// The most recent chat, whatever its id. Relying on the saved
			// last_chat instead silently opens the welcome screen whenever
			// that id belongs to a different database, which is exactly what
			// a seeded capture is.
			if chats, err := a.store.Chats(); err == nil && len(chats) > 0 {
				if err := a.openChat(chats[0].ID); err != nil {
					log.Printf("astral: scene: %v", err)
				}
			}
		case "demo":
			a.devDemoScene()
		case "rowmenu":
			a.devRowMenu()
		case "measure":
			a.devMeasure()
		case "icons":
			a.devIcons()
		case "image":
			a.devImage(arg)
		case "load":
			n, _ := strconv.Atoi(arg)
			a.devLoad(n)
		}
		return false
	})
}

// devImage runs a file through the import path and reports what came out, so
// the conversion can be checked end to end rather than only in a unit test.
func (a *App) devImage(src string) {
	if src == "" {
		log.Printf("astral: image: no path given")
		return
	}
	before, err := os.ReadFile(src)
	if err != nil {
		log.Printf("astral: image: %v", err)
		return
	}
	dst, err := a.importImage(src, "devtest")
	if err != nil {
		log.Printf("astral: image: import failed: %v", err)
		return
	}
	after, err := os.ReadFile(dst)
	if err != nil {
		log.Printf("astral: image: could not read result: %v", err)
		return
	}
	log.Printf("astral: image: %s (%s, %d bytes) -> %s (%s, %d bytes)",
		filepath.Base(src), imageconv.Format(before), len(before),
		filepath.Base(dst), imageconv.Format(after), len(after))

	// And it has to actually render, which is the point of converting it.
	if thumb := ui.NewImageThumb(dst, 64); thumb != nil {
		log.Printf("astral: image: renders as a thumbnail")
	} else {
		log.Printf("astral: image: FAILED to render")
	}
}

// devLoad seeds a chat with n messages and times opening it. Building the
// transcript is the one thing in the app whose cost grows with how much you
// have written, so it is the one worth knowing the real number for. Point
// XDG_DATA_HOME at a temporary directory before running it.
func (a *App) devLoad(n int) {
	if n <= 0 {
		n = 200
	}
	ch, err := a.store.NewChat(0, "Load test", a.cfg.Model, store.KindRoleplay)
	if err != nil {
		log.Printf("astral: devLoad: %v", err)
		return
	}
	body := strings.Repeat("*She traces a finger along the parchment, following some invisible boundary.* "+
		"\"The settlement is what's being argued.\" ", 3)
	seedStart := time.Now()
	for i := 0; i < n; i++ {
		role := ollama.RoleAssistant
		if i%2 == 0 {
			role = ollama.RoleUser
		}
		if _, err := a.store.AddMessage(store.Message{ChatID: ch.ID, Role: role, Content: body}); err != nil {
			log.Printf("astral: devLoad seed: %v", err)
			return
		}
	}
	log.Printf("astral: load: seeded %d messages in %v", n, time.Since(seedStart).Round(time.Millisecond))

	readStart := time.Now()
	msgs, err := a.store.Messages(ch.ID)
	if err != nil {
		log.Printf("astral: devLoad read: %v", err)
		return
	}
	read := time.Since(readStart)

	buildStart := time.Now()
	a.chat.LoadChat(ch, chars.Character{Name: "Vesper"}, msgs)
	build := time.Since(buildStart)

	a.showChat()
	log.Printf("astral: load: %d messages, read %v, build %v, total %v",
		n, read.Round(time.Microsecond), build.Round(time.Millisecond),
		(read + build).Round(time.Millisecond))

	built, pending := a.chat.DevRowCount()
	log.Printf("astral: load: %d rows built, %d waiting behind the button", built, pending)

	// The hover buttons are built on first hover, which a run like this cannot
	// do, so they are asked for directly.
	firstN, lastN := a.chat.DevActionCounts()
	log.Printf("astral: load: hover buttons build on demand: %d on the first row, %d on the last",
		firstN, lastN)

	// Exercise the "show earlier" path, which otherwise needs a click.
	for i := 0; pending > 0 && i < 3; i++ {
		start := time.Now()
		a.chat.DevLoadEarlier()
		built, pending = a.chat.DevRowCount()
		log.Printf("astral: load: batch %d in %v -> %d built, %d waiting",
			i+1, time.Since(start).Round(time.Millisecond), built, pending)
	}
	if built+pending != n {
		log.Printf("astral: load: MISMATCH built+waiting=%d, seeded %d", built+pending, n)
	} else {
		log.Printf("astral: load: OK every message accounted for")
	}
}

// devIcons reports which bundled icons the theme can actually resolve. A name
// that does not resolve renders as a blank button rather than an error, so
// without this the only way to find a broken icon is to notice the gap.
func (a *App) devIcons() {
	missing := 0
	for _, name := range ui.AllIcons {
		if !hasIcon(name) {
			log.Printf("astral: icons: MISSING %s", name)
			missing++
		}
	}
	if missing == 0 {
		log.Printf("astral: icons: all %d bundled icons resolve", len(ui.AllIcons))
	} else {
		log.Printf("astral: icons: %d of %d missing", missing, len(ui.AllIcons))
	}
}

// devRowMenu opens a chat row's context menu, which is the one path that
// needs a right-click to reach and therefore the one that shipped broken.
// Point XDG_DATA_HOME at a temporary directory to keep the seeded chat out of
// the real database.
func (a *App) devRowMenu() {
	id := a.sidebar.FirstChatID()
	if id == 0 {
		ch, err := a.store.NewChat(0, "Seeded for the row menu", a.cfg.Model, store.KindAssistant)
		if err != nil {
			log.Printf("astral: devRowMenu: %v", err)
			return
		}
		a.refreshSidebar()
		id = ch.ID
	}
	if !a.sidebar.OpenRowMenu(id) {
		log.Printf("astral: devRowMenu: no row for chat %d", id)
		return
	}
	log.Printf("astral: devRowMenu: opened the menu for chat %d without crashing", id)

	// Opening the menu is only half of it. The items carry an int64 target,
	// and a mismatch between that and the action's declared type makes GTK
	// refuse to activate them — silently, which is how Rename and Delete
	// shipped doing nothing. Firing them here proves the wiring end to end.
	for _, name := range []string{"rename-chat", "delete-chat"} {
		ok := gtk.BaseWidget(a.win).ActivateAction("win."+name, glib.NewVariantInt64(id))
		log.Printf("astral: devRowMenu: activate %s with an int64 target -> %v", name, ok)
	}
}

// devMeasure loads a demo scene and reports how it laid out. It is the
// substitute for looking at the window: if every bubble fits inside the view,
// the text wrapped rather than overflowing.
func (a *App) devMeasure() {
	a.devDemoScene()
	// One more turn of the loop, so GTK has actually allocated what was just
	// built — widths are all zero before that.
	coreglib.TimeoutAdd(600, func() bool {
		view, column, bubbles := a.chat.DevMeasure()
		log.Printf("astral: measure: view=%dpx column=%dpx", view, column)
		overflow := 0
		for i, b := range bubbles {
			flag := ""
			if b > view {
				flag = "  <-- OVERFLOWS THE VIEW"
				overflow++
			}
			log.Printf("astral: measure:   bubble %d = %dpx%s", i, b, flag)
		}
		if overflow == 0 {
			log.Printf("astral: measure: OK, every bubble fits inside the view")
		} else {
			log.Printf("astral: measure: FAIL, %d bubble(s) wider than the view", overflow)
		}
		return false
	})
}

// devDemoScene fabricates a scene for a screenshot, so a capture does not
// depend on what happens to be in the database — or on a model being installed
// at all. It writes nothing: the rows go straight onto the transcript.
func (a *App) devDemoScene() {
	c := chars.Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet.",
		Accent:      ui.AccentFor("Vesper Quill"),
	}
	a.chat.Clear()
	a.chat.LoadChat(store.Chat{Model: a.cfg.Model, CharacterID: 0}, c, []store.Message{
		{Role: ollama.RoleAssistant, Content: "*The map room smells of cold iron and older paper.* Vesper does not look up as you enter, she is busy pinning a coastline that will not exist for another two hundred years.\n\n\"You're late,\" *she says, without accusation.* \"The tide came in early. It does that, when someone's been reading ahead.\""},
		{Role: ollama.RoleUser, Content: "I set the lantern down on the edge of the table. \"You said the coastline was settled.\""},
		{Role: ollama.RoleAssistant, Content: "*She finally turns, and there is chalk dust on her knuckles.*\n\n\"I said it was **drawn**. Those aren't the same thing, and you knew that when you asked.\" *A pin goes into the table rather than the map, a small, deliberate violence.* \"Something is redrawing it from the other end. I'd like to know what, before it reaches the part we're standing on.\""},
	})
	a.showChat()
	a.setTitle(store.Chat{Title: "The tide came in early"}, c)
	// Leave a row mid-stream, so the typing indicator is on screen too.
	a.chat.DevShowTyping()
}
