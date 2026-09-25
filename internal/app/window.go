package app

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/store"
	"astral/internal/ui"
)

// buildWindow constructs the main window: a collapsible sidebar beside a
// header bar and the chat, laid out the way Claude Desktop is.
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
	}
	a.win.AddCSSClass("astral-window")

	a.buildSidebar()
	a.buildCenter()
	a.registerActions()

	// OverlaySplitView rather than a Paned: the sidebar is a fixed-width
	// navigation column that collapses, not a pane you drag — and on a narrow
	// window it slides over the chat instead of squeezing it.
	// The portrait sits on the far side of the chat, inside the main split so
	// hiding the navigation does not take it with it.
	a.portraitSplit = adw.NewOverlaySplitView()
	a.portraitSplit.SetSidebarPosition(gtk.PackEnd)
	a.portraitSplit.SetSidebar(a.buildPortraitPanel())
	a.portraitSplit.SetContent(a.stack)
	a.portraitSplit.SetSidebarWidthFraction(0.18)
	a.portraitSplit.SetMaxSidebarWidth(300)
	a.portraitSplit.SetMinSidebarWidth(170)
	a.portraitSplit.SetShowSidebar(false)

	a.split = adw.NewOverlaySplitView()
	a.split.SetSidebar(a.sidebar.Widget())
	a.split.SetContent(a.buildContent())
	a.split.SetSidebarWidthFraction(0.22)
	a.split.SetMaxSidebarWidth(340)
	a.split.SetMinSidebarWidth(210)
	a.split.SetShowSidebar(a.cfg.SidebarOpen)
	a.split.SetEnableShowGesture(true)
	a.split.SetEnableHideGesture(true)

	// Below this width the sidebar overlays rather than sitting beside the
	// chat, so the transcript keeps a readable column on a small window.
	breakpoint := adw.NewBreakpoint(adw.BreakpointConditionParse("max-width: 700px"))
	breakpoint.AddSetter(a.split, "collapsed", glib.NewValue(true))
	a.win.AddBreakpoint(breakpoint)

	// The portrait gives way earlier, and for a different reason. Three
	// columns fit comfortably on a wide window; below about 1100px the one in
	// the middle is the one that suffers, and the middle one is the scene. So
	// past that point the portrait floats over the chat instead of taking a
	// slice out of it.
	portraitBP := adw.NewBreakpoint(adw.BreakpointConditionParse("max-width: 1100px"))
	portraitBP.AddSetter(a.portraitSplit, "collapsed", glib.NewValue(true))
	a.win.AddBreakpoint(portraitBP)

	a.toasts = adw.NewToastOverlay()
	a.toasts.SetChild(a.split)
	a.win.SetContent(a.toasts)

	// Keep the toggle honest when the split view collapses itself.
	a.split.NotifyProperty("show-sidebar", func() {
		a.sideBtn.SetActive(a.split.ShowSidebar())
	})
}

// buildContent is everything to the right of the sidebar: header bar on top,
// the welcome screen or a chat below.
func (a *App) buildContent() *adw.ToolbarView {
	header := adw.NewHeaderBar()
	header.AddCSSClass("astral-header")
	header.SetShowTitle(true)

	a.title = adw.NewWindowTitle("Astral", "")
	header.SetTitleWidget(a.title)

	a.sideBtn = gtk.NewToggleButton()
	a.sideBtn.SetIconName(ui.IconPanelLeft)
	a.sideBtn.SetActive(a.cfg.SidebarOpen)
	a.sideBtn.SetTooltipText("Show or hide the sidebar (F9)")
	a.sideBtn.AddCSSClass("flat")
	a.sideBtn.ConnectToggled(func() { a.split.SetShowSidebar(a.sideBtn.Active()) })
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
		a.cfg.PortraitOpen = open
	})
	header.PackEnd(a.portraitBtn)

	menuBtn := gtk.NewMenuButton()
	menuBtn.SetIconName(ui.IconMenu)
	menuBtn.SetTooltipText("Main menu")
	menuBtn.AddCSSClass("flat")
	menuBtn.SetPrimary(true)
	menuBtn.SetMenuModel(a.buildMainMenu())
	header.PackEnd(menuBtn)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(a.portraitSplit)
	return tv
}

// buildSidebar constructs the left panel and wires its callbacks.
func (a *App) buildSidebar() {
	a.sidebar = ui.NewSidebar()
	a.sidebar.OnNewChat = a.actionNewChat
	a.sidebar.OnCharacters = a.showCharacters
	a.sidebar.OnWorlds = a.showWorlds
	a.sidebar.OnSettings = a.showSettings
	a.sidebar.OnPersona = func() { a.showSettingsPage("persona") }
	a.sidebar.OnStyles = a.showStyles
	a.sidebar.OnAbout = a.showAbout
	a.sidebar.OnOpenChat = func(id int64) {
		if err := a.openChat(id); err != nil {
			a.toast("Could not open that chat: " + err.Error())
		}
	}
}

// buildCenter constructs the stack holding the welcome screen and the chat.
func (a *App) buildCenter() {
	a.chat = ui.NewChatView(a.client, a.store, a.cfg)
	a.chat.OnChatChanged = a.refreshSidebar
	a.chat.OnError = func(msg string) {
		a.toast(msg)
		a.noteTurnFailed(msg)
	}
	a.chat.OnPickModel = a.showModelPicker
	a.chat.OnBuildCharacter = a.buildCharacterFromChat
	a.chat.OnEditDirection = a.editDirection
	a.chat.OnBuildStyle = a.buildStyleFromChat
	a.chat.OnLoreLearned = func(applied, held int) {
		// Worth saying, because the lorebook changed without being asked and
		// anything held back needs a decision. Kept to one line.
		switch {
		case held > 0 && applied > 0:
			a.toast(fmt.Sprintf("Learned %d things about this world. %d need a look.", applied+held, held))
		case held > 0:
			a.toast(fmt.Sprintf("%d possible lore entries need a look.", held))
		default:
			a.toast(fmt.Sprintf("Learned %d things about this world.", applied))
		}
	}
	a.chat.OnAttachImage = func() {
		a.pickImage("Attach a reference image", "reference", func(path string) {
			a.chat.AttachImage(path)
		})
	}

	a.stack = gtk.NewStack()
	a.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	a.stack.SetTransitionDuration(120)
	a.stack.AddNamed(a.buildWelcome(), "welcome")
	a.stack.AddNamed(a.chat.Widget(), "chat")
}

func (a *App) showChat() {
	if a.stack != nil {
		a.stack.SetVisibleChildName("chat")
	}
}

func (a *App) showWelcome() {
	a.refreshWelcome()
	if a.stack != nil {
		a.stack.SetVisibleChildName("welcome")
	}
	a.setTitle(store.Chat{}, chars.Character{})
	if a.sidebar != nil {
		a.sidebar.Select(0)
	}
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
		a.title.SetSubtitle("New scene" + where)
	case ch.Title != "":
		a.title.SetTitle(ch.Title)
		a.title.SetSubtitle("")
	default:
		a.title.SetTitle("Astral")
		a.title.SetSubtitle("")
	}
	if fixedWindowTitle {
		return
	}
	if t := a.title.Title(); t != "" && t != "Astral" {
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
	add("import-character", a.actionImportCharacter)
	add("settings", a.showSettings)
	add("model", a.showModelPicker)
	add("shortcuts", a.showShortcuts)
	add("about", a.showAbout)
	add("toggle-sidebar", func() { a.sideBtn.SetActive(!a.sideBtn.Active()) })
	add("focus-composer", func() { a.chat.FocusComposer() })
	addInt("rename-chat", a.actionRenameChat)
	addInt("delete-chat", a.actionDeleteChat)

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
	a.promptText("Rename chat", "Name", ch.Title, func(name string) {
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

func (a *App) actionDeleteChat(id int64) {
	ch, err := a.store.Chat(id)
	if err != nil {
		return
	}
	title := ch.Title
	if title == "" {
		title = "this chat"
	}
	a.confirm("Delete chat?",
		fmt.Sprintf("“%s” and everything in it will be deleted. This cannot be undone.", title),
		"Delete", func() {
			if err := a.store.DeleteChat(id); err != nil {
				a.toast("Could not delete: " + err.Error())
				return
			}
			if a.chat.Chat().ID == id {
				a.chat.Clear()
				a.showWelcome()
			}
			a.refreshSidebar()
		})
}
