package app

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
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
// copy of Astral you already have open, single-instance behaviour doing
// exactly what it should, and screenshot the wrong process.

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
// through InternObject, which assumes a GObject. GskRenderNode is not one, so
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
		if os.Getenv("ASTRAL_DEV_FRAMES") != "" {
			a.devTimeFrames()
		}
		if os.Getenv("ASTRAL_DEV_STARTUP") != "" {
			a.devTimeStartup()
		}
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
		case "knowledge":
			a.showKnowledge()
		case "study":
			a.studyTopic("SearXNG")
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
		case "character":
			if cs, err := a.store.Characters(); err == nil && len(cs) > 0 {
				// A relation, so the page is captured with something in the part
				// that is only interesting once it has content.
				if len(cs) > 1 {
					a.store.SetRelation(cs[0].ID, cs[1].ID,
						"She trained her, and neither of them mentions it.")
				}
				a.showCharacter(cs[0])
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
			if arg != "" {
				a.showSettingsPage(arg) // "you", "about"
			} else {
				a.showSettings()
			}
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
		case "drop":
			a.devDemoScene()
			a.chat.DevShowDrop(arg == "refused")
		case "prompts":
			a.showPrompts()
		case "prompt":
			a.editPrompt(arg)
		case "optimizer":
			a.startPromptOptimizer(arg)
		case "search":
			// The sidebar searching the seeded scene, so results and their
			// snippets can be looked at.
			if arg == "" {
				arg = "guild coast"
			}
			a.sidebar.DevSearch(arg)
		case "rowmenu":
			a.devRowMenu()
		case "measure":
			a.devMeasure()
		case "leaks":
			a.devLeaks(arg)
		case "cycle":
			// Open every chat in turn, rounds times, logging memory after
			// each round: what a long session of switching chats holds on to.
			rounds, _ := strconv.Atoi(arg)
			a.devCycle(max(rounds, 1))
		case "minwidth":
			// The scene, then what sets the window's narrowest width.
			if chats, err := a.store.Chats(); err == nil && len(chats) > 0 {
				_ = a.openChat(chats[0].ID)
			}
			// Again every two seconds for a while, so a capture that resizes
			// the window can read the widths at each size it tries.
			n := 0
			coreglib.TimeoutAdd(2000, func() bool {
				cur := "none"
				if bp := a.win.CurrentBreakpoint(); bp != nil {
					cur = bp.Condition().String()
				}
				log.Printf("astral: minwidth: window %d, sidebar collapsed %v, breakpoint %s, side %s",
					a.win.Width(), a.split.Collapsed(), cur, a.sideBP.Condition().String())
				a.devMinWidths(a.win.Content(), 0, 300)
				n++
				return n < 10
			})
		case "icons":
			a.devIcons()
		case "image":
			a.devImage(arg)
		case "load":
			n, _ := strconv.Atoi(arg)
			a.devLoad(n)
		case "restoretest":
			// A backup from yesterday, a chat made after it, then the restore
			// and close that Settings offers.
			chats, _ := a.store.Chats()
			log.Printf("astral: restore: %d chats before the backup", len(chats))
			path, err := a.store.BackupDaily(store.BackupDir(), time.Now().AddDate(0, 0, -1))
			if err != nil || path == "" {
				log.Printf("astral: restore: no backup made: %v", err)
				return false
			}
			a.store.NewChat(0, "Made after the backup", a.cfg.Model, store.KindAssistant)
			chats, _ = a.store.Chats()
			log.Printf("astral: restore: %d chats after adding one; restoring and closing", len(chats))
			a.pendingRestore = path
			a.adw.Quit()
		case "chatcount":
			chats, _ := a.store.Chats()
			log.Printf("astral: restore: %d chats on reopening", len(chats))
		case "open":
			// Opening a long chat and nothing else, to time its first frame.
			n, _ := strconv.Atoi(arg)
			a.devOpenLong(n)
		case "stream":
			a.devStream(arg)
		case "sidebar":
			n, _ := strconv.Atoi(arg)
			a.devSidebar(n)
		case "personas":
			// A second persona beside the one migrated from the settings, so
			// the list and the menu have something to switch between.
			if all, _ := a.store.Personas(); len(all) < 2 {
				a.store.SavePersona(chars.Profile{Name: "Brand Ashcombe", Age: "41", Gender: "man",
					Race: "human", Appearance: "Broad, grey at the temples, a smuggler's coat.",
					Background: "Runs cargo past the harbourmaster."})
				a.refreshPersonaMenu()
			}
			switch arg {
			case "edit":
				if all, _ := a.store.Personas(); len(all) > 1 {
					a.editPersona(all[1])
				}
			case "pick":
				if chats, err := a.store.Chats(); err == nil && len(chats) > 0 {
					_ = a.openChat(chats[0].ID)
					a.showPersonaPicker()
				}
			case "scene":
				if chats, err := a.store.Chats(); err == nil && len(chats) > 0 {
					_ = a.openChat(chats[0].ID)
				}
			case "creator":
				a.newPersonaDesignerChat()
			default:
				a.showPersonas()
			}
		case "editstyle":
			// A long style, as the Style Designer writes one, to scroll through.
			var lines []string
			for i := 1; i <= 60; i++ {
				lines = append(lines, fmt.Sprintf("Rule %d: keep sentences short and concrete, and never summarise what just happened.", i))
			}
			a.editStyle(chars.WritingStyle{Name: "Long Style", Instructions: strings.Join(lines, "\n")}, true)
		case "stylechat":
			// A Style Designer conversation long enough to need scrolling.
			ch, err := a.store.NewChat(0, "Designing a Style", a.cfg.Model, store.KindStyleDesigner)
			if err == nil {
				for i := 0; i < 12; i++ {
					a.store.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleUser,
						Content: fmt.Sprintf("Message %d: I want it sparse, present tense, and cold.", i)})
					a.store.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant,
						Content: fmt.Sprintf("Reply %d. %s", i, strings.Repeat("Short sentences land harder when the scene is tense, so we could keep them clipped. ", 4))})
				}
				_ = a.openChat(ch.ID)
			}
		case "attachfile":
			// A designer with a text file queued, as a drop or the attach
			// button leaves it.
			a.newDesignerChat()
			for _, p := range strings.Split(arg, ",") {
				a.chat.AttachPicked(gio.NewFileForPath(p))
			}
			if os.Getenv("ASTRAL_DEV_SEND") != "" {
				coreglib.TimeoutAdd(1500, func() bool {
					a.chat.DevSend(os.Getenv("ASTRAL_DEV_SEND"))
					return false
				})
			}
		case "toggle":
			n, _ := strconv.Atoi(arg)
			a.devToggle(n)
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
	dst, _, err := importImage(src, "devtest")
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

// devOpenLong seeds a chat of n messages and opens it the way the sidebar
// does, so the frame timer measures what opening a long chat costs.
func (a *App) devOpenLong(n int) {
	if n <= 0 {
		n = 400
	}
	ch, err := a.store.NewChat(0, "Long chat", a.cfg.Model, store.KindRoleplay)
	if err != nil {
		return
	}
	body := strings.Repeat("*She traces a finger along the parchment, following some invisible boundary.* "+
		"\"The settlement is what's being argued.\" ", 3)
	for i := 0; i < n; i++ {
		role := ollama.RoleAssistant
		if i%2 == 0 {
			role = ollama.RoleUser
		}
		a.store.AddMessage(store.Message{ChatID: ch.ID, Role: role, Content: body})
	}
	start := time.Now()
	coreglib.TimeoutAdd(1000, func() bool {
		start = time.Now()
		_ = a.openChat(ch.ID)
		coreglib.IdleAdd(func() bool {
			built, pending := a.chat.DevRowCount()
			log.Printf("astral: open: %d rows built, %d waiting, %v to build", built, pending, time.Since(start).Round(time.Millisecond))
			return false
		})
		// And again later, after whatever scrolling a driver has done.
		for _, at := range []uint{5000, 9000} {
			coreglib.TimeoutAdd(at, func() bool {
				built, pending := a.chat.DevRowCount()
				log.Printf("astral: open: later, %d rows built, %d waiting", built, pending)
				return false
			})
		}
		return false
	})
}

// devStream opens the most recent chat and sends a message into it, logging how
// long the reply took to arrive and finish on screen. With ASTRAL's model
// pointed at a fake server that streams at a known rate, it is the workload for
// profiling a reply being written, which is the part of the app people watch.
func (a *App) devStream(text string) {
	if text == "" {
		text = "Go on."
	}
	chats, err := a.store.Chats()
	if err != nil || len(chats) == 0 {
		log.Printf("astral: stream: no chat to send into")
		return
	}
	if err := a.openChat(chats[0].ID); err != nil {
		log.Printf("astral: stream: %v", err)
		return
	}
	prev := a.chat.OnReplyDone
	start := time.Now()
	a.chat.OnReplyDone = func(title, reply string) {
		log.Printf("astral: stream: reply of %d characters done in %v",
			len(reply), time.Since(start).Round(time.Millisecond))
		if prev != nil {
			prev(title, reply)
		}
	}
	a.chat.DevSend(text)
}

// devSidebar rebuilds the chat list n times and reports how long a rebuild
// takes, which is what happens every time a message reorders the list.
func (a *App) devSidebar(n int) {
	if n <= 0 {
		n = 30
	}
	chats, err := a.store.Chats()
	if err != nil {
		return
	}
	var total time.Duration
	for i := 0; i < n; i++ {
		a.sidebar.DevForgetSignature()
		start := time.Now()
		a.sidebar.SetChats(chats)
		total += time.Since(start)
	}
	log.Printf("astral: sidebar: %d chats, rebuilt %d times, %v each",
		len(chats), n, (total / time.Duration(n)).Round(10*time.Microsecond))
}

// devToggle opens a long chat and shows and hides the sidebar a few times,
// logging how smoothly each slide ran: how many frames it drew and the longest
// gap between two of them, which is the stutter a person sees.
func (a *App) devToggle(n int) {
	if n <= 0 {
		n = 200
	}
	a.devLoad(n)
	clock := gdk.BaseFrameClock(gtk.BaseWidget(a.win).FrameClock())
	var last time.Time
	var frames int
	var worst time.Duration
	clock.ConnectAfterPaint(func() {
		now := time.Now()
		if !last.IsZero() {
			if d := now.Sub(last); d > worst {
				worst = d
			}
		}
		last = now
		frames++
	})
	step := 0
	coreglib.TimeoutAdd(1500, func() bool {
		if step > 0 {
			log.Printf("astral: toggle: %d: %d frames, longest gap %v, column %dpx",
				step, frames, worst.Round(time.Millisecond), a.chat.DevColumnWidth())
		}
		if step == 8 {
			return false
		}
		step++
		frames, worst, last = 0, 0, time.Time{}
		a.sideBtn.SetActive(!a.sideBtn.Active())
		return true
	})
}

// devTimeFrames logs every frame that takes longer than a sixtieth of a second
// to lay out and draw, and a summary every few seconds. A frame is what the
// person sees stall, so this is the number a profile has to be read against:
// CPU spent where no frame was waiting on it is CPU nobody notices.
// processStart is when this process began, near enough: package variables are
// set before main runs. For timing a dev run's startup.
var processStart = time.Now()

// devCycle opens every chat in turn, a few rounds, and logs what the process
// holds after each. Growth that does not level off is a leak.
func (a *App) devCycle(rounds int) {
	chats, err := a.store.Chats()
	if err != nil || len(chats) == 0 {
		return
	}
	round, i, settle := 0, 0, 0
	ui.DevRows = &struct{ Built, Freed atomic.Int64 }{}
	// gotk4 logs its object lifecycle at debug level, with GOTK4_DEBUG set.
	if os.Getenv("ASTRAL_DEV_SLOG") != "" {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
	log.Printf("astral: cycle: before, %s", rss())
	coreglib.TimeoutAdd(40, func() bool {
		// Between rounds, time for what was let go to be freed: a widget tree
		// can take several garbage collections to come apart.
		if settle > 0 {
			runtime.GC()
			settle--
			if settle > 0 {
				return true
			}
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			log.Printf("astral: cycle: round %d of %d chats, %s, Go heap %d MB, %d Go objects, rows %d built %d freed", round, len(chats), rss(),
				ms.HeapInuse>>20, ms.HeapObjects, ui.DevRows.Built.Load(), ui.DevRows.Freed.Load())
			if p := pprof.Lookup("gotk4-object-box"); p != nil {
				log.Printf("astral: cycle: gotk4 holds %d objects", p.Count())
			}
			if path := os.Getenv("ASTRAL_DEV_HEAP"); path != "" && round == rounds {
				if f, err := os.Create(path); err == nil {
					_ = pprof.WriteHeapProfile(f)
					f.Close()
				}
			}
			return round < rounds
		}
		_ = a.openChat(chats[i].ID)
		i++
		if i < len(chats) {
			return true
		}
		i = 0
		round++
		settle = 25
		return true
	})
}

// devLeaks opens and closes each of the main dialogs a few times, logging
// how many objects gotk4 still holds after each, so a dialog that is never
// freed shows as a count that climbs. Run with GOTK4_DEBUG=profile-objects.
func (a *App) devLeaks(only string) {
	chats, _ := a.store.Chats()
	if len(chats) > 0 {
		_ = a.openChat(chats[0].ID)
	}
	type step struct {
		name string
		open func()
	}
	steps := []step{
		{"settings", a.showSettings},
		{"characters", a.showCharacters},
		{"memory", a.editMemory},
		{"prompts", a.showPrompts},
		{"styles", a.showStyles},
		{"worlds", a.showWorlds},
		{"newchat", a.showNewChat},
		{"model", a.showModelPicker},
		{"shortcuts", a.showShortcuts},
		{"editchar", func() {
			if cs, err := a.store.Characters(); err == nil && len(cs) > 0 {
				a.editCharacter(cs[0])
			}
		}},
		{"character", func() {
			if cs, err := a.store.Characters(); err == nil && len(cs) > 0 {
				a.showCharacter(cs[0])
			}
		}},
		{"sidebar", func() {
			for range 5 {
				a.refreshSidebar()
			}
		}},
		{"welcome", a.showWelcome},
		{"portrait", func() {
			if cs, err := a.store.Characters(); err == nil && len(cs) > 0 {
				a.refreshPortrait(cs[0])
			}
		}},
		{"openchat", func() {
			if len(chats) > 1 {
				_ = a.openChat(chats[1].ID)
				_ = a.openChat(chats[0].ID)
			}
		}},
	}
	held := func() int {
		if p := pprof.Lookup("gotk4-object-box"); p != nil {
			return p.Count()
		}
		return -1
	}
	const rounds = 4
	si, round, phase, settle := 0, 0, 0, 0
	var first int
	coreglib.TimeoutAdd(60, func() bool {
		for si < len(steps) && only != "" && steps[si].name != only {
			si++
		}
		if si >= len(steps) {
			log.Printf("astral: leaks: done, %s", rss())
			return false
		}
		st := steps[si]
		switch phase {
		case 0:
			st.open()
			phase, settle = 1, 8
		case 1: // let it show
			if settle--; settle > 0 {
				return true
			}
			if d := a.win.VisibleDialog(); d != nil {
				d.ForceClose()
			}
			phase, settle = 2, 20
		case 2:
			runtime.GC()
			if settle--; settle > 0 {
				return true
			}
			n := held()
			round++
			if round == 1 {
				first = n
			}
			if round == rounds {
				log.Printf("astral: leaks: %-10s %4d objects kept per open, %s", st.name, (n-first)/(rounds-1), rss())
				si, round = si+1, 0
			}
			phase = 0
		}
		return true
	})
}

// devTimeStartup logs how long after the process began the window drew its
// first frame, and what the process holds in memory then, when timing a
// capture run (ASTRAL_DEV_STARTUP).
func (a *App) devTimeStartup() {
	clock := gdk.BaseFrameClock(gtk.BaseWidget(a.win).FrameClock())
	if clock == nil {
		return
	}
	var handle glib.SignalHandle
	handle = clock.ConnectAfterPaint(func() {
		clock.HandlerDisconnect(handle)
		log.Printf("astral: startup: first frame %v after the process began, %s", time.Since(processStart).Round(time.Millisecond), rss())
		coreglib.TimeoutAdd(3000, func() bool {
			log.Printf("astral: startup: settled, %s", rss())
			return false
		})
	})
}

// rss is the process's resident memory, from /proc.
func rss() string {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "memory unknown"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			return "resident " + strings.TrimSpace(strings.TrimPrefix(line, "VmRSS:"))
		}
	}
	return "memory unknown"
}

func (a *App) devTimeFrames() {
	clock := gdk.BaseFrameClock(gtk.BaseWidget(a.win).FrameClock())
	if clock == nil {
		log.Printf("astral: frames: no frame clock yet")
		return
	}
	var start time.Time
	var frames, slow int
	var worst, total time.Duration
	clock.ConnectUpdate(func() { start = time.Now() })
	clock.ConnectAfterPaint(func() {
		if start.IsZero() {
			return
		}
		d := time.Since(start)
		frames++
		total += d
		if d > worst {
			worst = d
		}
		if d > 16*time.Millisecond {
			slow++
			log.Printf("astral: frames: a frame took %v", d.Round(time.Millisecond))
		}
	})
	coreglib.TimeoutAdd(3000, func() bool {
		if frames > 0 {
			log.Printf("astral: frames: %d frames, %d over 16ms, worst %v, mean %v",
				frames, slow, worst.Round(time.Millisecond), (total / time.Duration(frames)).Round(100*time.Microsecond))
		}
		frames, slow, worst, total = 0, 0, 0, 0
		return true
	})
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
	// refuse to activate them, silently, which is how Rename and Delete
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
	// built, widths are all zero before that.
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

// devMinWidths logs every widget at least floor pixels wide at its narrowest,
// indented by depth, so what stops the window getting narrow can be read off.
// A window cannot be narrower than its content allows, and a breakpoint that
// collapses the sidebar cannot help when the chat alone is too wide.
func (a *App) devMinWidths(w gtk.Widgetter, depth, floor int) {
	if w == nil {
		return
	}
	base := gtk.BaseWidget(w)
	if !base.Visible() {
		return
	}
	min, nat, _, _ := base.Measure(gtk.OrientationHorizontal, -1)
	if min >= floor {
		what := base.CSSName() + " " + strings.Join(base.CSSClasses(), ".")
		if l, ok := w.(*gtk.Label); ok {
			what += " \"" + ui.Snippet(l.Text(), 60) + "\""
		} else if b, ok := w.(*gtk.Button); ok && b.Label() != "" {
			what += " \"" + ui.Snippet(b.Label(), 60) + "\""
		}
		log.Printf("astral: minwidth: %s%d (natural %d) %s", strings.Repeat("  ", depth), min, nat, what)
	}
	for c := base.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		a.devMinWidths(c, depth+1, floor)
	}
}

// devDemoScene fabricates a scene for a screenshot, so a capture does not
// depend on what happens to be in the database, or on a model being installed
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
