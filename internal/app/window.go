package app

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/store"
	"astral/internal/ui"
)

// buildWindow constructs the main window, laid out as a frame and a page.
//
// The title bar and the sidebar are one surface, and the page, the welcome
// screen or a chat, sits on a layer of its own inside it, its top corners
// rounded where it meets the frame and a hairline along the edges that face
// it. It is how Atlas Monitor draws itself, after Windows 11's Task Manager:
// the frame is where you go, and the page is what you are reading.
func (a *App) buildWindow() {
	a.win = adw.NewApplicationWindow(&a.adw.Application)
	if fixedWindowTitle {
		a.win.SetTitle(devCaptureTitle)
	} else {
		a.win.SetTitle("Astral")
	}
	// A capture run takes its size from the environment, so screenshots are
	// reproducible instead of depending on whatever geometry your config holds.
	if w, h, ok := devWindowSize(); ok {
		a.win.SetDefaultSize(w, h)
	} else {
		a.win.SetDefaultSize(a.cfg.WindowWidth, a.cfg.WindowHeight)
		if a.cfg.WindowMaximized {
			a.win.Maximize()
		}
	}
	a.win.AddCSSClass("astral-window")
	// The narrowest the window may go, stated. Without it libadwaita takes the
	// content's own minimum, which with the sidebar open is wider than the
	// point where the sidebar is meant to fold away: the breakpoint could never
	// be reached, and a window dragged narrower anyway drew the part that did
	// not fit as a black strip down the side, which stayed when it was
	// widened again. With a minimum here, the breakpoints do the folding.
	a.win.SetSizeRequest(windowMinWidth, windowMinHeight)

	a.buildSidebar()
	a.buildCenter()
	a.registerActions()
	header := a.buildHeader()

	// OverlaySplitView rather than a Paned: the sidebar is a fixed-width
	// navigation column that collapses, not a pane you drag, and on a narrow
	// window it slides over the chat instead of squeezing it.
	// The portrait sits on the far side of the chat, inside the main split so
	// hiding the navigation does not take it with it.
	a.portraitSplit = adw.NewOverlaySplitView()
	a.portraitSplit.AddCSSClass("astral-shell")
	a.portraitSplit.SetSidebarPosition(gtk.PackEnd)
	a.portraitSplit.SetSidebar(a.buildPortraitPanel())
	a.portraitSplit.SetContent(a.buildPage())
	a.portraitSplit.SetSidebarWidthFraction(0.18)
	a.portraitSplit.SetMaxSidebarWidth(300)
	a.portraitSplit.SetMinSidebarWidth(170)
	a.portraitSplit.SetShowSidebar(false)

	a.split = adw.NewOverlaySplitView()
	a.split.AddCSSClass("astral-shell")
	a.split.SetSidebar(a.sidebarWithGrip())
	a.split.SetContent(a.portraitSplit)
	a.split.SetShowSidebar(a.cfg.SidebarOpen)
	// The width you left it at, which is the point of being able to drag it.
	a.applySidebarWidth(a.cfg.SidebarWidth)
	a.split.SetEnableShowGesture(true)
	a.split.SetEnableHideGesture(true)

	// A window applies one breakpoint at a time, and when two match, the one
	// added last wins. So they are added widest first, and each narrower one
	// repeats what the wider ones set. The sidebar's used to come first and
	// the portrait's second, and on every narrow window the portrait's won:
	// the sidebar never folded away, the chat was squeezed past its minimum,
	// and the window drew what did not fit as a black strip down the side.

	// The portrait gives way first, and for a different reason. Three
	// columns fit comfortably on a wide window; below about 1100px the one in
	// the middle is the one that suffers, and the middle one is the scene. So
	// past that point the portrait floats over the chat instead of taking a
	// slice out of it.
	a.portraitBP = adw.NewBreakpoint(adw.BreakpointConditionParse(portraitBreakpoint(a.cfg.SidebarWidth)))
	a.portraitBP.AddSetter(a.portraitSplit, "collapsed", glib.NewValue(true))
	a.win.AddBreakpoint(a.portraitBP)

	// Below this width the sidebar overlays rather than sitting beside the
	// chat, so the transcript keeps a readable column on a small window, and
	// the portrait stays floating as it already was.
	//
	// Where that is depends on how wide you dragged the sidebar, so the
	// condition is set by applySidebarWidth rather than fixed here.
	a.sideBP = adw.NewBreakpoint(adw.BreakpointConditionParse(sidebarBreakpoint(a.cfg.SidebarWidth)))
	a.sideBP.AddSetter(a.split, "collapsed", glib.NewValue(true))
	a.sideBP.AddSetter(a.portraitSplit, "collapsed", glib.NewValue(true))
	// And the version goes, so the title bar's buttons and the chat's name
	// still fit on one line.
	a.sideBP.AddSetter(a.brandVersion, "visible", glib.NewValue(false))
	a.win.AddBreakpoint(a.sideBP)

	// Narrowest of all, the name goes as well: at the window's smallest a
	// scene's title bar, its buttons and the window's own, needed more room
	// than there was, and pushed the close button off the edge. Added last,
	// so it wins, and repeating what the one above sets.
	narrow := adw.NewBreakpoint(adw.BreakpointConditionParse("max-width: 520sp"))
	narrow.AddSetter(a.split, "collapsed", glib.NewValue(true))
	narrow.AddSetter(a.portraitSplit, "collapsed", glib.NewValue(true))
	narrow.AddSetter(a.brandVersion, "visible", glib.NewValue(false))
	narrow.AddSetter(a.brand, "visible", glib.NewValue(false))
	a.win.AddBreakpoint(narrow)

	// The title bar spans the window, over the sidebar as well as the page,
	// which is what makes the two one frame.
	frame := adw.NewToolbarView()
	frame.AddCSSClass("astral-frame")
	frame.AddTopBar(header)
	frame.SetContent(a.split)

	a.toasts = adw.NewToastOverlay()
	a.toasts.SetChild(frame)
	a.win.SetContent(a.toasts)

	// Folding for width, done here rather than by the split views.
	//
	// Left to themselves they hide their side panel when the window narrows
	// and show it again, whatever it holds, when it widens. For the portrait
	// that meant an empty dark column down the right of every chat without
	// one, and of Home, the moment a narrowed window was widened again; for
	// the sidebar it meant one you had closed coming back. Pinned, they only
	// show what they are told to.
	a.split.SetPINSidebar(true)
	a.portraitSplit.SetPINSidebar(true)
	a.split.NotifyProperty("collapsed", func() {
		if a.split.Collapsed() {
			a.split.SetShowSidebar(false)
		} else {
			a.split.SetShowSidebar(a.cfg.SidebarOpen)
		}
		a.fitPage()
	})
	a.portraitSplit.NotifyProperty("collapsed", func() {
		if a.portraitSplit.Collapsed() {
			a.portraitSplit.SetShowSidebar(false)
		} else {
			a.portraitSplit.SetShowSidebar(a.portraitHas && a.cfg.PortraitOpen)
		}
		a.fitPage()
	})

	// Keep the toggles honest when a split view is shown or hidden by
	// anything but them.
	a.split.NotifyProperty("show-sidebar", func() {
		a.sideBtn.SetActive(a.split.ShowSidebar())
		a.fitPage()
	})
	a.portraitSplit.NotifyProperty("show-sidebar", func() {
		a.portraitBtn.SetActive(a.portraitSplit.ShowSidebar())
		a.fitPage()
	})
	a.fitPage()
}

// buildPage is the layer the welcome screen and the chats are drawn on.
func (a *App) buildPage() *adw.Bin {
	a.page = adw.NewBin()
	a.page.AddCSSClass("astral-page")
	// Clipped, so what is drawn on it keeps to its rounded corners.
	a.page.SetOverflow(gtk.OverflowHidden)
	a.page.SetChild(a.stack)
	return a.page
}

// fitPage rounds the page's corners only where it meets the frame: beside the
// sidebar, and beside the portrait. Where it runs to the window's edge, a
// corner would be a notch cut out of the window. A panel laid over the page
// on a narrow window does not count; the page is under it.
func (a *App) fitPage() {
	if a.page == nil {
		return
	}
	beside := func(v *adw.OverlaySplitView) bool { return v.ShowSidebar() && !v.Collapsed() }
	fit := func(class string, on bool) {
		if on {
			a.page.AddCSSClass(class)
		} else {
			a.page.RemoveCSSClass(class)
		}
	}
	fit("after-sidebar", beside(a.split))
	fit("before-portrait", beside(a.portraitSplit))
}

// The window's smallest size: the header bar's buttons and title in one row,
// and a composer with a line of transcript above it.
const (
	windowMinWidth  = 400
	windowMinHeight = 420
)

// buildHeader is the title bar: the app's name, the sidebar and a new chat
// at the start, what is open in the middle, and the portrait and the menu at
// the end.
func (a *App) buildHeader() *adw.HeaderBar {
	header := adw.NewHeaderBar()
	header.AddCSSClass("astral-header")
	header.SetShowTitle(true)

	// The name and the version at the start, where Atlas Monitor has them,
	// rather than in the middle, which is the open chat's.
	a.brand = gtk.NewBox(gtk.OrientationHorizontal, 6)
	a.brand.AddCSSClass("astral-brand")
	a.brand.SetVAlign(gtk.AlignCenter)
	name := gtk.NewLabel("Astral")
	name.AddCSSClass("astral-brand-name")
	a.brand.Append(name)
	a.brandVersion = gtk.NewLabel(version)
	a.brandVersion.AddCSSClass("astral-brand-version")
	a.brand.Append(a.brandVersion)
	header.PackStart(a.brand)

	a.title = adw.NewWindowTitle("", "")
	header.SetTitleWidget(a.title)

	a.sideBtn = gtk.NewToggleButton()
	a.sideBtn.SetIconName(ui.IconPanelLeft)
	a.sideBtn.SetActive(a.cfg.SidebarOpen)
	a.sideBtn.SetTooltipText("Show or hide the sidebar (F9)")
	a.sideBtn.AddCSSClass("flat")
	a.sideBtn.ConnectToggled(func() {
		a.split.SetShowSidebar(a.sideBtn.Active())
		// Your choice is kept only when the sidebar sits beside the chat.
		// Opening it over a narrow window, or its folding away there, says
		// nothing about whether you want it on a wide one.
		if !a.split.Collapsed() {
			a.cfg.SidebarOpen = a.sideBtn.Active()
		}
	})
	header.PackStart(a.sideBtn)

	newBtn := gtk.NewButtonFromIconName(ui.IconEdit)
	newBtn.SetTooltipText("New chat (Ctrl+N)")
	newBtn.AddCSSClass("flat")
	newBtn.ConnectClicked(a.actionNewChat)
	header.PackStart(newBtn)

	// Shown only when the model server is not answering. A green light that is
	// always on is decoration; what someone needs is to be told before they
	// type a paragraph into a window that cannot answer, and the welcome
	// screen's setup card is no help once a scene is open.
	a.offlineBtn = gtk.NewButton()
	a.offlineBtn.SetIconName(ui.IconInfo)
	a.offlineBtn.AddCSSClass("offline-chip")
	a.offlineBtn.SetVisible(false)
	a.offlineBtn.ConnectClicked(func() {
		a.toast("Checking for Ollama…")
		a.probeModels()
	})
	header.PackEnd(a.offlineBtn)

	a.portraitBtn = gtk.NewToggleButton()
	a.portraitBtn.SetIconName(ui.IconCharacters)
	a.portraitBtn.SetTooltipText("Show or hide the character portrait")
	a.portraitBtn.AddCSSClass("flat")
	a.portraitBtn.SetVisible(false)
	a.portraitBtn.ConnectToggled(func() {
		open := a.portraitBtn.Active()
		a.portraitSplit.SetShowSidebar(open)
		if !a.portraitSplit.Collapsed() {
			a.cfg.PortraitOpen = open
		}
	})
	header.PackEnd(a.portraitBtn)

	menuBtn := gtk.NewMenuButton()
	menuBtn.SetIconName(ui.IconMenu)
	menuBtn.SetTooltipText("Main menu")
	menuBtn.AddCSSClass("flat")
	menuBtn.SetPrimary(true)
	menuBtn.SetMenuModel(a.buildMainMenu())
	header.PackEnd(menuBtn)
	return header
}

// buildSidebar constructs the left panel and wires its callbacks.
func (a *App) buildSidebar() {
	a.sidebar = ui.NewSidebar()
	a.sidebar.OnNewChat = a.actionNewChat
	a.sidebar.OnHome = a.goHome
	a.sidebar.OnCharacters = a.showCharacters
	a.sidebar.OnWorlds = a.showWorlds
	a.sidebar.OnKnowledge = a.showKnowledge
	a.sidebar.OnPrompts = a.showPrompts
	a.sidebar.OnSettings = a.showSettings
	a.sidebar.OnPersona = a.showPersonas
	a.sidebar.OnCreatePersona = a.newPersonaDesignerChat
	a.sidebar.OnUsePersona = a.usePersonaByID
	a.sidebar.OnStyles = a.showStyles
	a.sidebar.OnAbout = a.showAbout
	a.sidebar.OnOpenChat = func(id int64) {
		if err := a.openChat(id); err != nil {
			a.toast("Could not open that chat: " + err.Error())
		}
	}
	a.sidebar.OnSearch = a.searchChats
	a.sidebar.OnDeleteChats = a.deleteChats
	a.sidebar.OnArchiveChats = a.archiveChats
	a.sidebar.SetGroupByCharacter(a.cfg.GroupChatsByCharacter)
	a.sidebar.OnGroupByCharacter = func(on bool) {
		a.cfg.GroupChatsByCharacter = on
		_ = store.SaveConfig(a.cfg)
	}
}

// searchChats answers the sidebar's search. On this thread: it is one indexed
// query, a few milliseconds even across years of chats.
func (a *App) searchChats(query string) {
	if a.store == nil {
		return
	}
	hits, err := a.store.SearchChats(query, 60)
	if err != nil {
		a.toast("Could not search: " + err.Error())
		return
	}
	chats, err := a.visibleChats()
	if err != nil {
		return
	}
	byID := make(map[int64]store.Chat, len(chats))
	for _, ch := range chats {
		byID[ch.ID] = ch
	}
	results := make([]ui.SearchResult, 0, len(hits))
	for _, h := range hits {
		if ch, ok := byID[h.ChatID]; ok {
			results = append(results, ui.SearchResult{Chat: ch, Snippet: h.Snippet})
		}
	}
	a.sidebar.ShowResults(query, results)
}

// buildCenter constructs the stack holding the welcome screen and the chat.
func (a *App) buildCenter() {
	a.chat = ui.NewChatView(a.client, a.store, a.cfg)
	a.chat.OnChatChanged = a.refreshSidebar
	a.chat.OnEditCast = a.showCastEditor
	a.chat.OnError = func(msg string) {
		a.toast(msg)
		a.noteTurnFailed(msg)
	}
	a.chat.OnPickModel = func() {
		a.probeModels() // in case one was pulled since the window opened
		a.showModelPicker()
	}
	a.chat.OnBuildCharacter = a.buildCharacterFromChat
	a.chat.OnEditDirection = a.editDirection
	a.chat.OnEditMemory = a.editMemory
	a.chat.OnBranch = a.branchChat
	a.chat.OnNotice = a.toast
	a.chat.OnBuildStyle = a.buildStyleFromChat
	a.chat.OnBuildWorld = a.buildWorldFromChat
	a.chat.OnBuildPersona = a.buildPersonaFromChat
	a.chat.OnPickPersona = a.showPersonaPicker
	a.chat.PersonaFor = func(id int64) (chars.Profile, bool) {
		if a.store == nil {
			return chars.Profile{}, false
		}
		p, err := a.store.Persona(id)
		return p, err == nil
	}
	a.chat.OnSavePrompt = a.savePromptFromChat
	a.chat.OnSaveToKnowledge = a.saveReplyToKnowledge
	a.chat.OnLoreLearned = func(applied, held int) {
		// Worth saying, because the lorebook changed without being asked and
		// anything held back needs a decision. Kept to one line.
		switch {
		case held > 0 && applied > 0:
			a.toast(fmt.Sprintf("Learned %d things about this world, and %d need a look.", applied+held, held))
		case held > 0:
			a.toast(fmt.Sprintf("%d possible lore entries need a look.", held))
		default:
			a.toast(fmt.Sprintf("Learned %d things about this world.", applied))
		}
	}
	a.chat.OnAttachImage = a.pickAttachment
	// Dropped on the chat, or pasted into it. Both go through the same importer
	// the file chooser uses, so a dropped HEIC is converted and a truncated one
	// is refused here rather than three steps later.
	// A reply that finishes while the window is in the background says so,
	// so a slow model can be left to write while you do something else.
	a.chat.OnReplyDone = a.notifyReply
	a.chat.OnImageFile = func(path string) {
		a.importImageAsync(path, nil, "reference", a.chat.AttachImage)
	}
	a.chat.OnImageBytes = func(data []byte) {
		a.importImageAsync("", data, "reference", a.chat.AttachImage)
	}

	a.stack = gtk.NewStack()
	a.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	a.stack.SetTransitionDuration(120)
	a.stack.AddNamed(a.buildWelcome(), "welcome")
	a.stack.AddNamed(a.chat.Widget(), "chat")
}

// notifyReply says a reply has been written, when nobody is looking at the
// window.
func (a *App) notifyReply(title, text string) {
	if !a.cfg.NotifyReplies || a.win == nil || a.win.IsActive() {
		return
	}
	if title == "" {
		title = "Astral"
	}
	n := gio.NewNotification(title)
	n.SetBody(notificationPreview(text))
	// One at a time: a new reply replaces the last notice rather than
	// stacking a pile of them in the tray.
	a.adw.SendNotification("reply", n)
}

func (a *App) showChat() {
	if a.stack != nil {
		a.stack.SetVisibleChildName("chat")
	}
	if a.sidebar != nil {
		a.sidebar.SetActivePage(ui.NavNone)
	}
}

// chatShowing reports whether the window is on the open chat, rather than on
// Home or one of the pages.
func (a *App) chatShowing() bool {
	return a.stack != nil && a.stack.VisibleChildName() == "chat"
}

func (a *App) showWelcome() {
	a.refreshWelcome()
	if a.stack != nil {
		a.stack.SetVisibleChildName("welcome")
	}
	a.setTitle(store.Chat{}, chars.Character{})
	if a.sidebar != nil {
		a.sidebar.Select(0)
		a.sidebar.SetActivePage(ui.NavHome)
	}
}

// goHome leaves the open scene for the welcome screen.
//
// The chat is not closed, only left: it stays in the sidebar and reopens where
// it was. What does have to happen is that the portrait panel goes with it,
// since it belongs to a character nobody is playing at the moment.
func (a *App) goHome() {
	if a.chat != nil {
		a.chat.Stop()
	}
	a.showPortraitFor(chars.Character{})
	a.cfg.LastChat = 0
	a.showWelcome()
}

// setTitle puts the open scene in the header bar.
func (a *App) setTitle(ch store.Chat, ca chars.Character) {
	if a.title == nil {
		return
	}
	where := ""
	if ca.WorldID != 0 {
		if w, err := a.store.World(ca.WorldID); err == nil {
			where = " in " + w.Name
		}
	}
	switch {
	case ca.Name != "" && ch.Title != "":
		a.title.SetTitle(ch.Title)
		a.title.SetSubtitle("with " + ca.Name + where)
	case ca.Name != "":
		a.title.SetTitle(ca.Name)
		a.title.SetSubtitle("New Scene" + where)
	case ch.Title != "":
		a.title.SetTitle(ch.Title)
		a.title.SetSubtitle("")
	default:
		// Nothing open: the name is already at the start of the bar.
		a.title.SetTitle("")
		a.title.SetSubtitle("")
	}
	if fixedWindowTitle {
		return
	}
	if t := a.title.Title(); t != "" {
		a.win.SetTitle(t + " · Astral")
	} else {
		a.win.SetTitle("Astral")
	}
}

// buildMainMenu is the hamburger menu: everything the app can do that is not a
// one-click button, with its shortcut beside it.
func (a *App) buildMainMenu() *gio.Menu {
	menu := gio.NewMenu()

	section := gio.NewMenu()
	section.Append("New Chat", "win.new-chat")
	section.Append("Design a Character", "win.design-character")
	section.Append("Characters", "win.characters")
	section.Append("Writing Styles", "win.styles")
	section.Append("Worlds", "win.worlds")
	section.Append("Knowledge", "win.knowledge")
	section.Append("Study a Topic…", "win.study")
	section.Append("Import Character…", "win.import-character")
	menu.AppendSection("", section)

	view := gio.NewMenu()
	view.Append("Toggle Sidebar", "win.toggle-sidebar")
	view.Append("Choose Model…", "win.model")
	menu.AppendSection("", view)

	app := gio.NewMenu()
	app.Append("Settings", "win.settings")
	app.Append("Keyboard Shortcuts", "win.shortcuts")
	app.Append("About Astral", "win.about")
	menu.AppendSection("", app)

	return menu
}

// registerActions installs the window actions and their accelerators. They are
// window-scoped rather than app-scoped because the row menus address them by
// name with a parameter, which needs a target that exists per window.
func (a *App) registerActions() {
	add := func(name string, fn func()) {
		act := gio.NewSimpleAction(name, nil)
		act.ConnectActivate(func(_ *glib.Variant) { fn() })
		a.win.AddAction(act)
	}
	addInt := func(name string, fn func(int64)) {
		act := gio.NewSimpleAction(name, glib.NewVariantType("x"))
		act.ConnectActivate(func(p *glib.Variant) {
			if p != nil {
				fn(p.Int64())
			}
		})
		a.win.AddAction(act)
	}

	add("new-chat", a.actionNewChat)
	add("characters", a.showCharacters)
	add("design-character", a.newDesignerChat)
	add("styles", a.showStyles)
	add("worlds", a.showWorlds)
	add("knowledge", a.showKnowledge)
	add("study", func() { a.studyTopic("") })
	add("import-character", a.actionImportCharacter)
	add("settings", a.showSettings)
	add("model", a.showModelPicker)
	add("shortcuts", a.showShortcuts)
	add("search-chats", func() {
		if a.sidebar != nil {
			a.sidebar.FocusSearch()
		}
	})
	add("about", a.showAbout)
	add("toggle-sidebar", func() { a.sideBtn.SetActive(!a.sideBtn.Active()) })
	add("focus-composer", func() { a.chat.FocusComposer() })
	addInt("rename-chat", a.actionRenameChat)
	addInt("export-chat", a.actionExportChat)
	addInt("delete-chat", a.actionDeleteChat)
	addInt("select-chat", func(id int64) { a.sidebar.Mark(id) })
	addInt("continue-chat", a.continueChat)
	addInt("archive-chat", func(id int64) { a.archiveChats([]int64{id}, true) })
	addInt("unarchive-chat", func(id int64) { a.archiveChats([]int64{id}, false) })

	for accel, action := range map[string]string{
		"<Control>n":     "win.new-chat",
		"<Control>k":     "win.characters",
		"<Control>j":     "win.styles",
		"<Control>w":     "win.worlds",
		"<Control>comma": "win.settings",
		"<Control>m":     "win.model",
		"F9":             "win.toggle-sidebar",
		"<Control>l":     "win.focus-composer",
		"<Control>slash": "win.shortcuts",
		"<Control>f":     "win.search-chats",
	} {
		a.adw.SetAccelsForAction(action, []string{accel})
	}
}

func (a *App) actionNewChat() { a.showNewChat() }

func (a *App) actionRenameChat(id int64) {
	ch, err := a.store.Chat(id)
	if err != nil {
		return
	}
	a.promptText("Rename Chat", "Name", ch.Title, func(name string) {
		if name == "" {
			return
		}
		if err := a.store.RenameChat(id, name); err != nil {
			a.toast("Could not rename: " + err.Error())
			return
		}
		a.refreshSidebar()
		if a.chat.Chat().ID == id {
			cur := a.chat.Chat()
			cur.Title = name
			ca, _ := a.store.Character(cur.CharacterID)
			a.setTitle(cur, ca)
		}
	})
}

func (a *App) actionDeleteChat(id int64) { a.deleteChats([]int64{id}) }

// notificationPreview is the start of a reply, as a notification shows it: one
// paragraph, without the asterisks that mark narration, cut at a word.
func notificationPreview(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	text = strings.ReplaceAll(text, "*", "")
	const most = 140
	r := []rune(text)
	if len(r) <= most {
		return text
	}
	cut := string(r[:most])
	if i := strings.LastIndexByte(cut, ' '); i > most/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}
