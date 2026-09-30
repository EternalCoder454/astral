package app

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
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
// leaving it undid is put back, and nothing is read or rebuilt.
func (a *App) resumeChat() {
	ch := a.chat.Chat()
	var ca chars.Character
	if cast := a.chat.Cast(); len(cast) > 0 {
		ca = cast[0]
	}
	a.showPortraitFor(ca)
	a.showChat()
	a.sidebar.Select(ch.ID)
	a.setTitle(ch, ca)
	a.cfg.LastChat = ch.ID
}
