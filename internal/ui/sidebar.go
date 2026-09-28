package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

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
	homeBtn     *gtk.Button

	selected int64
	rows     map[int64]*chatRow
	// order is the chats as listed, top to bottom, which is what a Shift
	// click selects a run of.
	order []int64
	// marked is the chats picked out to delete together; see sidebar_marks.go.
	marked    map[int64]bool
	anchor    int64
	markBar   *gtk.Revealer
	markLabel *gtk.Label
	// OnDeleteChats deletes the chats that are marked.
	OnDeleteChats func(ids []int64)
	// rowMenus holds each row's menu opener, so the dev harness can trigger
	// one without synthesizing a click.
	rowMenus  map[int64]func(x, y float64)
	firstChat int64
	// lastSig is a digest of the list as last drawn, so an identical refresh
	// costs a hash instead of rebuilding every row.
	lastSig uint64

	// search finds a chat by what was said in it. While it holds a query the
	// list shows what it found, and chats is the full list to go back to.
	search    *gtk.SearchEntry
	searching bool
	chats     []store.Chat
	// OnSearch asks for the chats matching a query. The app answers with
	// ShowResults.
	OnSearch func(query string)

	// Callbacks, all invoked on the main thread.
	OnNewChat    func()
	OnOpenChat   func(id int64)
	OnCharacters func()
	// OnHome returns to the welcome screen.
	OnHome func()
	// OnWorlds opens the list of settings.
	OnWorlds func()
	// OnKnowledge opens the knowledge base.
	OnKnowledge func()
	// OnPrompts opens the list of prompts and the Prompt Optimizer.
	OnPrompts  func()
	OnSettings func()
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
	s := &Sidebar{
		rows:     map[int64]*chatRow{},
		rowMenus: map[int64]func(float64, float64){},
		marked:   map[int64]bool{},
	}

	s.widget = gtk.NewBox(gtk.OrientationVertical, 0)
	s.widget.AddCSSClass("astral-sidebar")

	head := gtk.NewBox(gtk.OrientationVertical, 6)
	head.AddCSSClass("sidebar-head")

	newBtn := gtk.NewButton()
	newBtn.AddCSSClass("new-chat-button")
	newBtn.SetChild(navContent(IconAdd, "New Chat"))
	newBtn.SetTooltipText("Start a new chat (Ctrl+N)")
	newBtn.ConnectClicked(func() { fire(s.OnNewChat) })
	head.Append(newBtn)
	s.widget.Append(head)

	nav := gtk.NewBox(gtk.OrientationVertical, 1)
	nav.AddCSSClass("sidebar-nav")

	// The way back. The welcome screen holds the cast, the worlds and the
	// greeting, and until now it could only be reached by having no chat open
	//, which, once you had opened one, meant not at all.
	s.homeBtn = gtk.NewButton()
	s.homeBtn.AddCSSClass("sidebar-item")
	s.homeBtn.SetChild(navContent(IconHome, "Home"))
	s.homeBtn.SetTooltipText("Your cast and your worlds")
	s.homeBtn.ConnectClicked(func() { fire(s.OnHome) })
	nav.Append(s.homeBtn)

	s.charsBtn = gtk.NewButton()
	s.charsBtn.AddCSSClass("sidebar-item")
	s.charsBtn.SetChild(navContent(IconCharacters, "Characters"))
	s.charsBtn.SetTooltipText("Browse and import characters (Ctrl+K)")
	s.charsBtn.ConnectClicked(func() { fire(s.OnCharacters) })
	nav.Append(s.charsBtn)

	worldsBtn := gtk.NewButton()
	worldsBtn.AddCSSClass("sidebar-item")
	worldsBtn.SetChild(navContent(IconWorlds, "Worlds"))
	worldsBtn.SetTooltipText("Settings your characters live in, and what they remember (Ctrl+W)")
	worldsBtn.ConnectClicked(func() { fire(s.OnWorlds) })
	nav.Append(worldsBtn)

	knowledgeBtn := gtk.NewButton()
	knowledgeBtn.AddCSSClass("sidebar-item")
	knowledgeBtn.SetChild(navContent(IconKnowledge, "Knowledge"))
	knowledgeBtn.SetTooltipText("Notes, saved pages and studied topics that every conversation but a scene draws on")
	knowledgeBtn.ConnectClicked(func() { fire(s.OnKnowledge) })
	nav.Append(knowledgeBtn)

	promptsBtn := gtk.NewButton()
	promptsBtn.AddCSSClass("sidebar-item")
	promptsBtn.SetChild(navContent(IconDesigner, "Prompts"))
	promptsBtn.SetTooltipText("Every prompt Astral sends, and the Prompt Optimizer that improves them")
	promptsBtn.ConnectClicked(func() { fire(s.OnPrompts) })
	nav.Append(promptsBtn)
	s.widget.Append(nav)

	// Finding a chat by what was said in it. Above the list rather than in
	// the header, because it is the list it changes.
	s.search = gtk.NewSearchEntry()
	s.search.SetPlaceholderText("Search Chats")
	s.search.AddCSSClass("sidebar-search")
	s.search.ConnectSearchChanged(func() {
		q := strings.TrimSpace(s.search.Text())
		if q == "" {
			s.endSearch()
			return
		}
		s.searching = true
		fire(func() {
			if s.OnSearch != nil {
				s.OnSearch(q)
			}
		})
	})
	s.search.ConnectStopSearch(func() { s.search.SetText("") })
	s.widget.Append(s.search)

	// The conversation list.
	s.listBox = gtk.NewBox(gtk.OrientationVertical, 1)
	s.listBox.AddCSSClass("sidebar-nav")
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(s.listBox)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	s.widget.Append(scroll)
	s.watchListClicks()
	s.widget.Append(s.buildMarkBar())

	// Profile row: who you are on the left, settings on the right. They were
	// one button, which meant the only way to reach settings was to click
	// something labelled with your own name.
	foot := gtk.NewBox(gtk.OrientationHorizontal, 4)
	foot.AddCSSClass("sidebar-foot")

	s.profile = gtk.NewButton()
	s.profile.AddCSSClass("profile-pill")
	s.profile.SetHExpand(true)
	s.profile.SetTooltipText("Your Persona")
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
	add(IconEdit, "Edit Your Persona", func() { fire(s.OnPersona) })
	add(IconDesigner, "Writing Styles", func() { fire(s.OnStyles) })
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
	n.SetEllipsize(pango.EllipsizeEnd)
	n.AddCSSClass("profile-name")
	col.Append(n)
	// A subtitle only when there is something worth saying. The persona
	// description was shown here, which meant the pill read "Name: Christian
	// Appeara...", the first few words of a prose field, truncated mid-word.
	// A prompt to set one up is useful; a fragment of one is not.
	if strings.TrimSpace(subtitle) == "" {
		m := gtk.NewLabel("Set up your persona")
		m.SetXAlign(0)
		m.SetEllipsize(pango.EllipsizeEnd)
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
	l.SetEllipsize(pango.EllipsizeEnd)
	box.Append(l)
	return box
}

// navContent is rowContent for the navigation buttons, where the icon and the
// label sit together in the middle of the button rather than pinned to its left
// edge.
//
// The difference is that a navigation button is a destination and a chat row is a
// line of text. Four destinations centred read as a set; the same four pinned left
// with a wide gap after them read as a list that has lost its right-hand column,
// which is what a resizable sidebar makes obvious.
func navContent(icon, text string) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	box.SetHAlign(gtk.AlignCenter)
	if icon != "" {
		box.Append(gtk.NewImageFromIconName(icon))
	}
	l := gtk.NewLabel(text)
	l.SetEllipsize(pango.EllipsizeEnd)
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
	s.chats = chats
	if s.searching {
		// The results stay until the search is cleared; the list is redrawn
		// from what was kept here when it is.
		return
	}
	// This is called after every message, twice a turn, and usually nothing
	// about the list has changed, the open chat was already at the top. A
	// rebuild is a few hundred widgets plus a popover and two gestures per
	// row, and gotk4 keeps every closure alive for the life of the process,
	// so the wasted ones are not collected either.
	if sig := chatSignature(chats); sig == s.lastSig {
		s.applySelection()
		return
	} else {
		s.lastSig = sig
	}

	s.clearList()
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
	s.keepMarks()
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
		writeInt(int64(c.CastSize))
		write([]byte{0})
	}
	return h
}

// chatRow builds one conversation entry.
// DevForgetSignature makes the next SetChats rebuild the list, for the dev
// harness's timing of it.
func (s *Sidebar) DevForgetSignature() { s.lastSig = 0 }

// DevSearch types a query into the search box, for the dev harness.
func (s *Sidebar) DevSearch(q string) { s.search.SetText(q) }

// FocusSearch puts the cursor in the search box.
func (s *Sidebar) FocusSearch() { s.search.GrabFocus() }

// endSearch goes back to the ordinary list.
func (s *Sidebar) endSearch() {
	if !s.searching {
		return
	}
	s.searching = false
	s.lastSig = 0 // draw it again, whatever it was before
	s.SetChats(s.chats)
}

// SearchResult is a chat a search found, and the line in it that matched.
type SearchResult struct {
	Chat    store.Chat
	Snippet string
}

// ShowResults replaces the list with what a search found.
func (s *Sidebar) ShowResults(query string, results []SearchResult) {
	if !s.searching || strings.TrimSpace(s.search.Text()) != query {
		return // a later query has already been asked, or it was cleared
	}
	s.clearList()
	if len(results) == 0 {
		empty := gtk.NewLabel("Nothing found.")
		empty.SetXAlign(0)
		empty.AddCSSClass("sidebar-empty")
		s.listBox.Append(empty)
		return
	}
	for _, r := range results {
		s.listBox.Append(s.chatRowWith(r.Chat, r.Snippet))
	}
	s.applySelection()
	s.keepMarks()
}

// clearList empties the list and forgets its rows.
func (s *Sidebar) clearList() {
	for {
		child := s.listBox.FirstChild()
		if child == nil {
			break
		}
		s.listBox.Remove(child)
	}
	s.rows = map[int64]*chatRow{}
	s.rowMenus = map[int64]func(float64, float64){}
	s.order = s.order[:0]
}

// snippetMarkup renders a search snippet, whose matching words are marked with
// \x01 and \x02, as Pango markup with those words in bold. Each piece is
// escaped on its own, so nothing in a message can become markup.
func snippetMarkup(snippet string) string {
	// The asterisks that mark narration mean nothing out of the bubble.
	snippet = strings.ReplaceAll(snippet, "*", "")
	var b strings.Builder
	bold := false
	start := 0
	for i, r := range snippet {
		if r != 1 && r != 2 {
			continue
		}
		b.WriteString(glib.MarkupEscapeText(snippet[start:i]))
		if r == 1 && !bold {
			b.WriteString("<b>")
			bold = true
		} else if r == 2 && bold {
			b.WriteString("</b>")
			bold = false
		}
		start = i + 1
	}
	b.WriteString(glib.MarkupEscapeText(snippet[start:]))
	if bold {
		b.WriteString("</b>")
	}
	return b.String()
}

func (s *Sidebar) chatRow(ch store.Chat) *gtk.Button { return s.chatRowWith(ch, "") }

// chatRowWith is a chat's row, with the line a search matched under the title
// when there is one.
func (s *Sidebar) chatRowWith(ch store.Chat, snippet string) *gtk.Button {
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
		title = "New Chat"
	}
	l := gtk.NewLabel(title)
	l.SetXAlign(0)
	l.SetHExpand(true)
	l.SetEllipsize(pango.EllipsizeEnd)
	l.AddCSSClass("chat-row-title")
	box.Append(l)

	// How many people are in it. Without this a scene with a cast is
	// indistinguishable from a two-hander in the list, and which one you are
	// opening is the thing you most want to know before you open it.
	if ch.CastSize > 1 {
		n := gtk.NewLabel(strconv.Itoa(ch.CastSize))
		n.AddCSSClass("chat-row-cast")
		n.SetVAlign(gtk.AlignCenter)
		n.SetTooltipText(fmt.Sprintf("%d characters in this scene", ch.CastSize))
		box.Append(n)
	}

	// The way to the row's menu for anyone who does not think to right-click
	// it. Always there and faded out until the row is pointed at, so the
	// title does not shift when it appears; a click on it is caught by the
	// list, which opens the menu, rather than by a button of its own.
	more := gtk.NewImageFromIconName(IconMore)
	more.SetName(rowMoreName)
	more.AddCSSClass("chat-row-more")
	more.SetTooltipText("Rename, export, select or delete")
	box.Append(more)
	if snippet != "" {
		col := gtk.NewBox(gtk.OrientationVertical, 2)
		col.Append(box)
		line := gtk.NewLabel("")
		line.SetMarkup(snippetMarkup(snippet))
		line.SetXAlign(0)
		line.SetWrap(true)
		line.SetLines(2)
		line.SetEllipsize(pango.EllipsizeEnd)
		line.AddCSSClass("chat-row-snippet")
		col.Append(line)
		btn.SetChild(col)
	} else {
		btn.SetChild(box)
	}

	tip := title
	switch {
	case ch.CastSize > 1 && ch.CharacterName != "":
		tip = fmt.Sprintf("%s, with %s and %d others", title, ch.CharacterName, ch.CastSize-1)
	case ch.CharacterName != "":
		tip = fmt.Sprintf("%s, with %s", title, ch.CharacterName)
	}
	btn.SetTooltipText(tip)

	id := ch.ID
	btn.SetName(rowName(id))
	btn.ConnectClicked(func() {
		if s.OnOpenChat != nil {
			s.OnOpenChat(id)
		}
	})
	s.attachRowMenu(btn, id)
	s.rows[id] = &chatRow{btn: btn, box: box, dot: dot}
	s.order = append(s.order, id)
	return btn
}

// attachRowMenu wires the right-click menu on a conversation row.
func (s *Sidebar) attachRowMenu(btn *gtk.Button, id int64) {
	// Built on the first right-click rather than with the row. A popover menu
	// registers itself with the window's actions as it is made, and doing
	// that for every chat was nearly half of what drawing the list cost:
	// measured with 400 chats, a quarter of a second at startup and again
	// whenever a message reordered the list.
	var pop *gtk.PopoverMenu
	show := func(x, y float64) {
		if pop == nil {
			pop = rowPopover(btn, id)
		}
		// The menu opens at the pointer. The rectangle must be built with
		// gdk.NewRectangle: a &gdk.Rectangle{} is a Go struct with no native
		// backing behind it, and gotk4 dereferences that straight into a
		// segfault, which is exactly how this crashed the app the first time
		// someone right-clicked a chat.
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

// rowPopover builds a chat row's context menu.
func rowPopover(btn *gtk.Button, id int64) *gtk.PopoverMenu {
	// The target is attached as a real GVariant rather than encoded into a
	// detailed action string. "win.rename-chat(7)" looks right but parses its
	// target as an int32, while the action is declared to take an int64, the
	// types do not match, so GTK quietly refuses to activate the item and the
	// menu entry does nothing at all when clicked.
	menu := gio.NewMenu()
	for _, it := range []struct {
		label  string
		action string
	}{
		{"Rename…", "win.rename-chat"},
		{"Export…", "win.export-chat"},
		{"Select", "win.select-chat"},
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
	return pop
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
	for id, row := range s.rows {
		if id == s.selected {
			row.btn.AddCSSClass("selected")
		} else {
			row.btn.RemoveCSSClass("selected")
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
