package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
)

// Sidebar is the left panel: a new-chat button, the way through to the cast,
// and the conversation list grouped by when you last touched it.
type Sidebar struct {
	widget      *gtk.Box
	listBox     *gtk.Box
	profile     *gtk.Button
	profileMenu *gtk.Popover
	charsBtn    *gtk.Button

	selected int64
	rows     map[int64]*gtk.Button
	// rowMenus holds each row's menu opener, so the dev harness can trigger
	// one without synthesizing a click.
	rowMenus  map[int64]func(x, y float64)
	firstChat int64
	// lastSig is a digest of the list as last drawn, so an identical refresh
	// costs a hash instead of rebuilding every row.
	lastSig uint64

	// Callbacks, all invoked on the main thread.
	OnNewChat    func()
	OnOpenChat   func(id int64)
	OnCharacters func()
	// OnWorlds opens the list of settings.
	OnWorlds func()
	OnSettings   func()
	// OnPersona opens the persona editor from the profile menu.
	OnPersona func()
	// OnAbout opens the about dialog from the profile menu.
	OnAbout func()
	// OnStyles opens the writing styles list.
	OnStyles     func()
	OnRenameChat func(id int64)
	OnDeleteChat func(id int64)
}

// NewSidebar builds the panel.
func NewSidebar() *Sidebar {
	s := &Sidebar{rows: map[int64]*gtk.Button{}, rowMenus: map[int64]func(float64, float64){}}

	s.widget = gtk.NewBox(gtk.OrientationVertical, 0)
	s.widget.AddCSSClass("astral-sidebar")

	head := gtk.NewBox(gtk.OrientationVertical, 6)
	head.AddCSSClass("sidebar-head")

	newBtn := gtk.NewButton()
	newBtn.AddCSSClass("new-chat-button")
	newBtn.SetChild(rowContent(IconAdd, "New chat"))
	newBtn.SetTooltipText("Start a new chat (Ctrl+N)")
	newBtn.ConnectClicked(func() { fire(s.OnNewChat) })
	head.Append(newBtn)
	s.widget.Append(head)

	nav := gtk.NewBox(gtk.OrientationVertical, 1)
	nav.AddCSSClass("sidebar-nav")
	s.charsBtn = gtk.NewButton()
	s.charsBtn.AddCSSClass("sidebar-item")
	s.charsBtn.SetChild(rowContent(IconCharacters, "Characters"))
	s.charsBtn.SetTooltipText("Browse and import characters (Ctrl+K)")
	s.charsBtn.ConnectClicked(func() { fire(s.OnCharacters) })
	nav.Append(s.charsBtn)

	worldsBtn := gtk.NewButton()
	worldsBtn.AddCSSClass("sidebar-item")
	worldsBtn.SetChild(rowContent(IconWorlds, "Worlds"))
	worldsBtn.SetTooltipText("Settings your characters live in, and what they remember (Ctrl+W)")
	worldsBtn.ConnectClicked(func() { fire(s.OnWorlds) })
	nav.Append(worldsBtn)
	s.widget.Append(nav)

	// The conversation list.
	s.listBox = gtk.NewBox(gtk.OrientationVertical, 1)
	s.listBox.AddCSSClass("sidebar-nav")
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(s.listBox)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	s.widget.Append(scroll)

	// Profile row: who you are on the left, settings on the right. They were
	// one button, which meant the only way to reach settings was to click
	// something labelled with your own name.
	foot := gtk.NewBox(gtk.OrientationHorizontal, 4)
	foot.AddCSSClass("sidebar-foot")

	s.profile = gtk.NewButton()
	s.profile.AddCSSClass("profile-pill")
	s.profile.SetHExpand(true)
	s.profile.SetTooltipText("Your persona")
	s.profile.ConnectClicked(func() { s.profileMenu.Popup() })
	foot.Append(s.profile)

	s.profileMenu = s.buildProfileMenu()

	gear := gtk.NewButtonFromIconName(IconSettings)
	gear.AddCSSClass("profile-gear")
	gear.SetTooltipText("Settings (Ctrl+,)")
	gear.SetVAlign(gtk.AlignCenter)
	gear.ConnectClicked(func() { fire(s.OnSettings) })
	foot.Append(gear)

	s.widget.Append(foot)
	s.SetProfile("", "")

	return s
}

// Widget returns the panel's root widget.
func (s *Sidebar) Widget() gtk.Widgetter { return s.widget }

// buildProfileMenu is the popover the profile pill opens. It is about you,
// not about the app: the app's own settings are the gear beside it.
func (s *Sidebar) buildProfileMenu() *gtk.Popover {
	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.SetMarginTop(4)
	box.SetMarginBottom(4)
	box.SetMarginStart(4)
	box.SetMarginEnd(4)

	add := func(icon, label string, fn func()) {
		b := gtk.NewButton()
		b.AddCSSClass("sidebar-item")
		b.SetChild(rowContent(icon, label))
		b.ConnectClicked(func() {
			s.profileMenu.Popdown()
			fire(fn)
		})
		box.Append(b)
	}
	add(IconEdit, "Edit your persona", func() { fire(s.OnPersona) })
	add(IconDesigner, "Writing styles", func() { fire(s.OnStyles) })
	add(IconInfo, "About Astral", func() { fire(s.OnAbout) })

	pop := gtk.NewPopover()
	pop.SetChild(box)
	pop.SetParent(s.profile)
	pop.SetPosition(gtk.PosTop)
	pop.SetHasArrow(true)
	return pop
}

// SetProfile fills the bottom pill with who you are playing as.
//
// The model used to be shown here too, which meant it was on screen twice:
// once under your name, where it has nothing to do with you, and again on the
// composer where it is actually actionable. This one is gone.
func (s *Sidebar) SetProfile(name, subtitle string) {
	if name == "" {
		name = "You"
	}

	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.Append(NewUserAvatar(firstLetter(name), 26))
	col := gtk.NewBox(gtk.OrientationVertical, 0)
	col.SetVAlign(gtk.AlignCenter)
	col.SetHExpand(true)
	n := gtk.NewLabel(name)
	n.SetXAlign(0)
	n.SetEllipsize(3)
	n.AddCSSClass("profile-name")
	col.Append(n)
	// A subtitle only when there is something worth saying. The persona
	// description was shown here, which meant the pill read "Name: Christian
	// Appeara..." — the first few words of a prose field, truncated mid-word.
	// A prompt to set one up is useful; a fragment of one is not.
	if strings.TrimSpace(subtitle) == "" {
		m := gtk.NewLabel("Set up your persona")
		m.SetXAlign(0)
		m.SetEllipsize(3)
		m.AddCSSClass("profile-sub")
		col.Append(m)
	} else {
		n.SetVAlign(gtk.AlignCenter)
	}
	box.Append(col)
	s.profile.SetChild(box)
}

// rowContent is the icon-plus-label pairing every sidebar entry uses.
func rowContent(icon, text string) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	if icon != "" {
		box.Append(gtk.NewImageFromIconName(icon))
	}
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.SetHExpand(true)
	l.SetEllipsize(3) // PANGO_ELLIPSIZE_END
	box.Append(l)
	return box
}

// SetChats rebuilds the conversation list.
//
// Rebuilding wholesale rather than diffing is deliberate: the list is at most
// a few hundred rows, it changes only when a chat is added, renamed or
// reordered, and a diff would have to reproduce the grouping logic to know
// which rows moved between sections. The simpler code is also the correct one
// here.
func (s *Sidebar) SetChats(chats []store.Chat) {
	// This is called after every message, twice a turn, and usually nothing
	// about the list has changed — the open chat was already at the top. A
	// rebuild is a few hundred widgets plus a popover and two gestures per
	// row, and gotk4 keeps every closure alive for the life of the process,
	// so the wasted ones are not collected either.
	if sig := chatSignature(chats); sig == s.lastSig {
		s.applySelection()
		return
	} else {
		s.lastSig = sig
	}

	for {
		child := s.listBox.FirstChild()
		if child == nil {
			break
		}
		s.listBox.Remove(child)
	}
	s.rows = map[int64]*gtk.Button{}
	s.rowMenus = map[int64]func(float64, float64){}
	s.firstChat = 0
	if len(chats) > 0 {
		s.firstChat = chats[0].ID
	}

	if len(chats) == 0 {
		empty := gtk.NewLabel("No chats yet.\nStart one to see it here.")
		empty.SetXAlign(0)
		empty.AddCSSClass("sidebar-empty")
		empty.SetWrap(true)
		s.listBox.Append(empty)
		return
	}

	lastSection := ""
	for _, ch := range chats {
		if sec := sectionFor(ch.UpdatedAt); sec != lastSection {
			lastSection = sec
			head := gtk.NewLabel(sec)
			head.SetXAlign(0)
			head.AddCSSClass("sidebar-section")
			s.listBox.Append(head)
		}
		s.listBox.Append(s.chatRow(ch))
	}
	s.applySelection()
}

// chatSignature digests what the list actually draws: order, identity, title
// and tint. Anything else about a chat can change without the sidebar looking
// any different.
func chatSignature(chats []store.Chat) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	write := func(b []byte) {
		for _, c := range b {
			h ^= uint64(c)
			h *= prime
		}
	}
	var num [8]byte
	writeInt := func(v int64) {
		for i := 0; i < 8; i++ {
			num[i] = byte(v >> (8 * i))
		}
		write(num[:])
	}
	for _, c := range chats {
		writeInt(c.ID)
		writeInt(int64(c.Accent))
		write([]byte(c.Title))
		write([]byte(c.CharacterName))
		write([]byte{0})
	}
	return h
}

// chatRow builds one conversation entry.
func (s *Sidebar) chatRow(ch store.Chat) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("sidebar-item")

	box := gtk.NewBox(gtk.OrientationHorizontal, 9)

	// A tinted dot rather than a full avatar: at this size an avatar is an
	// unreadable letter, while the dot still says which character a scene
	// belongs to at a glance down the list.
	dot := gtk.NewBox(gtk.OrientationHorizontal, 0)
	dot.AddCSSClass("chat-dot")
	dot.SetSizeRequest(7, 7)
	dot.SetVAlign(gtk.AlignCenter)
	if ch.CharacterName != "" {
		SetAccent(dot, ch.Accent)
	}
	box.Append(dot)

	title := ch.Title
	if title == "" {
		title = "New chat"
	}
	l := gtk.NewLabel(title)
	l.SetXAlign(0)
	l.SetHExpand(true)
	l.SetEllipsize(3)
	l.AddCSSClass("chat-row-title")
	box.Append(l)
	btn.SetChild(box)

	tip := title
	if ch.CharacterName != "" {
		tip = fmt.Sprintf("%s, with %s", title, ch.CharacterName)
	}
	btn.SetTooltipText(tip)

	id := ch.ID
	btn.ConnectClicked(func() {
		if s.OnOpenChat != nil {
			s.OnOpenChat(id)
		}
	})
	s.attachRowMenu(btn, id)
	s.rows[id] = btn
	return btn
}

// attachRowMenu wires the right-click menu on a conversation row.
func (s *Sidebar) attachRowMenu(btn *gtk.Button, id int64) {
	// The target is attached as a real GVariant rather than encoded into a
	// detailed action string. "win.rename-chat(7)" looks right but parses its
	// target as an int32, while the action is declared to take an int64 — the
	// types do not match, so GTK quietly refuses to activate the item and the
	// menu entry does nothing at all when clicked.
	menu := gio.NewMenu()
	for _, it := range []struct {
		label  string
		action string
	}{
		{"Rename…", "win.rename-chat"},
		{"Delete", "win.delete-chat"},
	} {
		item := gio.NewMenuItem(it.label, "")
		item.SetActionAndTargetValue(it.action, glib.NewVariantInt64(id))
		menu.AppendItem(item)
	}

	pop := gtk.NewPopoverMenuFromModel(menu)
	pop.SetParent(btn)
	pop.SetHasArrow(false)
	pop.SetHAlign(gtk.AlignStart)

	// The menu opens at the pointer. The rectangle must be built with
	// gdk.NewRectangle: a &gdk.Rectangle{} is a Go struct with no native
	// backing behind it, and gotk4 dereferences that straight into a segfault
	// — which is exactly how this crashed the app the first time someone
	// right-clicked a chat.
	show := func(x, y float64) {
		at := gdk.NewRectangle(int(x), int(y), 1, 1)
		pop.SetPointingTo(&at)
		pop.Popup()
	}
	s.rowMenus[id] = show

	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_SECONDARY)
	click.ConnectPressed(func(nPress int, x, y float64) { show(x, y) })
	btn.AddController(click)

	// A long press reaches the same menu on a touchscreen, where there is no
	// second mouse button to press.
	long := gtk.NewGestureLongPress()
	long.ConnectPressed(show)
	btn.AddController(long)
}

// OpenRowMenu opens a chat row's context menu programmatically. It exists so
// the dev harness can exercise the path a right-click takes without a
// right-click, because "nobody clicked it" is how the crash above shipped.
// It reports whether there was a row to open.
func (s *Sidebar) OpenRowMenu(id int64) bool {
	show, ok := s.rowMenus[id]
	if !ok {
		return false
	}
	show(8, 8)
	return true
}

// FirstChatID returns the topmost conversation in the list, or 0.
func (s *Sidebar) FirstChatID() int64 { return s.firstChat }

// Select marks a conversation as the open one.
func (s *Sidebar) Select(id int64) {
	s.selected = id
	s.applySelection()
}

func (s *Sidebar) applySelection() {
	for id, btn := range s.rows {
		if id == s.selected {
			btn.AddCSSClass("selected")
		} else {
			btn.RemoveCSSClass("selected")
		}
	}
}

// sectionFor buckets a chat by age, the way a person thinks about when they
// last did something rather than by date.
func sectionFor(t time.Time) string {
	if t.IsZero() {
		return "Earlier"
	}
	now := time.Now()
	// Compared by calendar day, not by elapsed hours: something from 11pm last
	// night is "Yesterday" at 9am, not "Today".
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(startOfToday):
		return "Today"
	case !t.Before(startOfToday.AddDate(0, 0, -1)):
		return "Yesterday"
	case !t.Before(startOfToday.AddDate(0, 0, -7)):
		return "Previous 7 days"
	case !t.Before(startOfToday.AddDate(0, 0, -30)):
		return "Previous 30 days"
	default:
		return "Earlier"
	}
}

func fire(fn func()) {
	if fn != nil {
		fn()
	}
}
