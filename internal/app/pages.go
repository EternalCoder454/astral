package app

import (
	"fmt"
	"reflect"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/chars"
	"astral/internal/ui"
)

// The Characters, Worlds, Knowledge and Prompts pages.
//
// They were dialogs, which put the thing you had asked for in a small window
// over the one you were in. They are pages of the main window now, the way
// every destination in Atlas Monitor is: a title, a bar of commands, and the
// content, in the space the chat and the welcome screen share. What is opened
// from one of them, an editor or a lorebook, is still a dialog, because it is
// a task with a Save and a Cancel rather than somewhere to be.

// The pages' names, which are also their names in the window's stack.
const (
	pageCharacters = "characters"
	pageWorlds     = "worlds"
	pageKnowledge  = "knowledge"
	pagePrompts    = "prompts"
)

// pageNav is the sidebar row that stands for each page.
var pageNav = map[string]ui.NavPage{
	pageCharacters: ui.NavCharacters,
	pageWorlds:     ui.NavWorlds,
	pageKnowledge:  ui.NavKnowledge,
	pagePrompts:    ui.NavPrompts,
}

// pageWidth is how wide a page's content may grow before it stops and is
// centred instead.
const pageWidth = 760

// pageState is what the app remembers about the pages.
type pageState struct {
	hosts map[string]*pageHost
	// parked says a chat was on screen when a page was opened over it, and has
	// not been left since: it is still loaded, with any reply still being
	// written, and going back to it must not reload it. Home clears it, because
	// going Home stops the reply and the chat is reloaded when it is opened.
	parked bool
}

// pageHost is a page's place in the stack. It is added once, and what it holds
// is replaced each time the page is built, so there is never more than one
// child of that name, and rebuilding in place does not fade.
type pageHost struct {
	bin  *adw.Bin
	root *gtk.ScrolledWindow
	// search is the page's search entry, if it has one, so a rebuild can carry
	// what was typed into it over to the new page.
	search *gtk.SearchEntry
}

// pageView is a page being built: its title row, its command bar and the box
// its content goes in.
type pageView struct {
	title    string
	root     *gtk.ScrolledWindow
	caption  *gtk.Label
	commands *adw.WrapBox
	body     *gtk.Box
}

// newPage lays out a page after Atlas Monitor's: a scroller, a column no wider
// than pageWidth, a title with a quiet caption across from it, and a flat
// command bar under them.
func newPage(title string) *pageView {
	v := &pageView{title: title}
	v.body = gtk.NewBox(gtk.OrientationVertical, 14)
	v.body.SetMarginTop(18)
	v.body.SetMarginBottom(18)
	v.body.SetMarginStart(18)
	v.body.SetMarginEnd(18)

	lead := gtk.NewLabel(title)
	lead.AddCSSClass("page-title")
	lead.SetXAlign(0)
	v.caption = gtk.NewLabel("")
	v.caption.AddCSSClass("page-caption")
	v.caption.SetHExpand(true)
	v.caption.SetXAlign(1)
	v.caption.SetEllipsize(pango.EllipsizeEnd)
	v.caption.SetVAlign(gtk.AlignCenter)
	head := gtk.NewBox(gtk.OrientationHorizontal, 12)
	head.Append(lead)
	head.Append(v.caption)
	v.body.Append(head)

	// A wrapping box rather than a row: at the window's narrowest the commands
	// go onto a second line instead of setting the window's minimum width.
	v.commands = adw.NewWrapBox()
	v.commands.SetChildSpacing(2)
	v.commands.SetLineSpacing(2)
	v.body.Append(v.commands)

	clamp := adw.NewClamp()
	clamp.SetMaximumSize(pageWidth)
	clamp.SetTighteningThreshold(pageWidth * 3 / 4)
	clamp.SetChild(v.body)

	v.root = gtk.NewScrolledWindow()
	v.root.SetChild(clamp)
	v.root.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	v.root.SetHExpand(true)
	v.root.SetVExpand(true)
	return v
}

// setCaption is the quiet line across from the title, empty for nothing.
func (v *pageView) setCaption(text string) { v.caption.SetText(text) }

// count phrases a number of things for a caption, and says nothing for none.
func count(n int, one, many string) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d %s", n, plural(n, one, many))
}

// commandFace is the icon and the word a command shows.
func commandFace(icon, label string) *adw.ButtonContent {
	c := adw.NewButtonContent()
	c.SetIconName(icon)
	c.SetLabel(label)
	return c
}

// commandButton is a flat command that does something once, as Task Manager's
// command bar has them: a small glyph and then the words.
func commandButton(icon, label string, fn func()) *gtk.Button {
	b := gtk.NewButton()
	b.SetChild(commandFace(icon, label))
	b.AddCSSClass("flat")
	b.AddCSSClass("page-command")
	b.ConnectClicked(fn)
	return b
}

// commandMenu is a command that opens a menu.
func commandMenu(icon, label string, pop *gtk.Popover) *gtk.MenuButton {
	b := gtk.NewMenuButton()
	b.SetChild(commandFace(icon, label))
	b.SetPopover(pop)
	b.AddCSSClass("flat")
	b.AddCSSClass("page-command")
	return b
}

// pageShowing is the page the window is on, or empty when it is on Home or a
// chat.
func (a *App) pageShowing() string {
	if a.stack == nil {
		return ""
	}
	if name := a.stack.VisibleChildName(); pageNav[name] != ui.NavNone {
		return name
	}
	return ""
}

// installPage puts a freshly built page in place of the last one of its name
// and shows it.
//
// The old page is let go of once it is out of sight: its buttons hold handlers
// that reach back into it, a circle that goes through GTK where Go's collector
// cannot follow, so, as with a dialog, letting go of its root breaks it.
func (a *App) installPage(name string, v *pageView) {
	if a.pages.hosts == nil {
		a.pages.hosts = map[string]*pageHost{}
	}
	h := a.pages.hosts[name]
	if h == nil {
		h = &pageHost{bin: adw.NewBin()}
		a.pages.hosts[name] = h
		a.stack.AddNamed(h.bin, name)
	}
	// Rebuilt while it is showing, which is a refresh after an edit: it should
	// stay where the reader had scrolled to.
	keep := 0.0
	if h.root != nil && a.stack.VisibleChildName() == name {
		keep = h.root.VAdjustment().Value()
	}
	if a.chatShowing() {
		a.pages.parked = true
	}
	old := h.root
	h.bin.SetChild(v.root)
	h.root = v.root
	if old != nil {
		coreglib.TimeoutAdd(500, func() bool {
			coreglib.Destroy(old)
			return false
		})
	}
	if keep > 0 {
		adj := v.root.VAdjustment()
		var handle coreglib.SignalHandle
		handle = adj.ConnectChanged(func() {
			// Until the page has been laid out there is nothing to scroll.
			if adj.PageSize() == 0 {
				return
			}
			adj.HandlerDisconnect(handle)
			adj.SetValue(keep)
		})
	}

	a.stack.SetVisibleChildName(name)
	a.sidebar.SetActivePage(pageNav[name])
	// Like Home, a page has no character to show.
	a.showPortraitFor(chars.Character{})
	a.setPageTitle(v.title)
}

// carriedQuery is what is typed in the named page's search entry, and whether
// the page is showing, which makes the build that follows a refresh rather than
// a first opening. A rebuild makes a new entry, so without this it would lose
// the query and, on Knowledge, take the focus again.
func (a *App) carriedQuery(name string) (query string, refreshing bool) {
	if a.pageShowing() != name {
		return "", false
	}
	if h := a.pages.hosts[name]; h != nil && h.search != nil {
		return h.search.Text(), true
	}
	return "", true
}

// rememberSearch records the entry of the page just installed, for
// carriedQuery.
func (a *App) rememberSearch(name string, search *gtk.SearchEntry) {
	if h := a.pages.hosts[name]; h != nil {
		h.search = search
	}
	// A search entry keeps Escape for itself, so the window never hears it
	// while the entry has the focus, which on Knowledge is from the moment it
	// opens. Escape there clears what was typed, and in an empty entry goes
	// back, as it does anywhere else on a page. The entry is found through
	// the host rather than held by the handler, which would be a cycle.
	search.ConnectStopSearch(func() {
		h := a.pages.hosts[name]
		if h == nil || h.search == nil {
			return
		}
		if h.search.Text() != "" {
			h.search.SetText("")
			return
		}
		if a.win.VisibleDialog() == nil {
			a.leavePage()
		}
	})
}

// refreshPage rebuilds the named page if it is the one showing, after
// something changed what it lists. A page that is not showing is built fresh
// when it is next opened, so there is nothing to do for it.
func (a *App) refreshPage(name string) {
	if a.pageShowing() != name {
		return
	}
	switch name {
	case pageCharacters:
		a.showCharacters()
	case pageWorlds:
		a.showWorlds()
	case pageKnowledge:
		a.showKnowledge()
	case pagePrompts:
		a.showPrompts()
	}
}

// resumeChat brings back the chat a page was opened over, as it was: still
// loaded, still writing if it was. Everything openChat sets around a chat that
// leaving it undid is put back, and nothing is rebuilt.
//
// The chat may have changed while it was parked, its character edited, its cast
// changed, or the chat renamed, so what is shown is read from the store again.
// The loaded chat view cannot take a new character or cast without reloading, so
// when they differ it says so and openChat loads the chat afresh. A reply being
// written is worth more than that, so it keeps the chat as it is and only the
// title and portrait are refreshed.
func (a *App) resumeChat() bool {
	loaded := a.chat.Chat()
	ch, ca, cast, _, err := a.chatParts(loaded.ID, false)
	if err != nil {
		// Not readable now: show what is loaded rather than fail to go back.
		ch, ca, cast = loaded, chars.Character{}, a.chat.Cast()
		if len(cast) > 0 {
			ca = cast[0]
		}
	} else if !a.chat.Busy() && !sameCast(a.chat.Cast(), leadCast(ca, cast)) {
		return false
	}
	a.showPortraitFor(ca)
	a.showChat()
	a.sidebar.Select(ch.ID)
	a.setTitle(ch, ca)
	a.cfg.LastChat = ch.ID
	return true
}

// leadCast is the cast a chat view is loaded with: everyone when there is more
// than one, else just the character.
func leadCast(ca chars.Character, cast []chars.Character) []chars.Character {
	if len(cast) > 1 {
		return cast
	}
	return []chars.Character{ca}
}

// sameCast says whether two casts are the same people as they are written now,
// which is what decides whether a loaded chat can be shown as it is.
func sameCast(a, b []chars.Character) bool {
	return reflect.DeepEqual(a, b)
}

// installPageEscape makes Escape on a page go back to where you were: the chat
// the page was opened over, else Home. The dialogs it replaced closed on Escape.
//
// It listens after the focused widget has had its say, so a search entry
// clearing itself, a popover closing or a dialog over the page keeps its own
// Escape. The window's visible dialog is checked as well, because a dialog's
// keys also travel up to the window.
func (a *App) installPageEscape() {
	key := gtk.NewEventControllerKey()
	key.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if keyval != gdk.KEY_Escape || state&gdk.ModifierType(gtk.AcceleratorGetDefaultModMask()) != 0 {
			return false
		}
		if a.pageShowing() == "" || a.win.VisibleDialog() != nil {
			return false
		}
		a.leavePage()
		return true
	})
	a.win.AddController(key)
}

// leavePage goes back from a page to the parked chat, or to Home when none is
// parked.
func (a *App) leavePage() {
	if a.pages.parked && a.chat != nil && a.chat.Chat().ID != 0 {
		if err := a.openChat(a.chat.Chat().ID); err == nil {
			return
		}
	}
	a.goHome()
}
