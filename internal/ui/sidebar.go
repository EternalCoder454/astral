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

	"astral/internal/chars"
	"astral/internal/scene"
	"astral/internal/store"
)

// NavPage is one of the navigation rows at the top of the sidebar, each of them
// somewhere the window can be.
type NavPage int

const (
	// NavNone marks no navigation row: the window is showing a chat, whose own
	// row is marked instead.
	NavNone NavPage = iota
	NavHome
	NavCharacters
	NavWorlds
	NavKnowledge
	NavPrompts
)

// Sidebar is the left panel: a new-chat button, the way through to the cast,
// and the conversation list grouped by when you last touched it.
type Sidebar struct {
	widget      *gtk.Box
	listBox     *gtk.Box
	profile     *gtk.Button
	profileMenu *gtk.Popover

	// selected is the chat that is open, and page which of the navigation rows
	// stands for what the window is showing, NavNone while it is a chat. A chat
	// row is marked only while page is NavNone, so the row that says where you
	// are is always one row, whichever of the two the window was last told.
	selected int64
	page     NavPage
	navBtns  map[NavPage]*gtk.Button

	// charsCount and worldsCount are the counts at the end of their rows.
	charsCount, worldsCount *gtk.Label

	rows map[int64]*chatRow
	// order is the chats as listed, top to bottom, which is what a Shift
	// click selects a run of.
	order []int64
	// marked is the chats picked out to delete together; see sidebar_marks.go.
	marked     map[int64]bool
	anchor     int64
	markBar    *gtk.Revealer
	markLabel  *gtk.Label
	archiveBtn *gtk.Button
	// OnDeleteChats deletes the chats that are marked.
	OnDeleteChats func(ids []int64)
	// rowMenus holds each row's menu opener, so the dev harness can trigger
	// one without synthesizing a click.
	rowMenus  map[int64]func(x, y float64)
	firstChat int64
	// lastSig is a digest of the list as last drawn, so an identical refresh
	// costs a hash instead of rebuilding every row.
	lastSig uint64
	// byCharacter groups the list under characters rather than days, and
	// archivedOpen is whether the archive at its end is showing its chats.
	byCharacter  bool
	archivedOpen bool
	groupBtn     *gtk.ToggleButton
	// OnGroupByCharacter is told when the grouping is switched, so the choice
	// is kept for next time.
	OnGroupByCharacter func(on bool)
	// OnArchiveChats puts the chats away, or brings them back.
	OnArchiveChats func(ids []int64, archived bool)

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
	// OnPersona opens the list of your personas from the profile menu, and
	// OnCreatePersona the Persona Designer. OnUsePersona switches which one
	// new chats are played as.
	OnPersona       func()
	OnCreatePersona func()
	OnUsePersona    func(id int64)
	personaBox      *gtk.Box
	profileShown    string
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
		navBtns:  map[NavPage]*gtk.Button{},
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

	// addNav builds one destination's row and remembers its button, so that
	// SetActivePage can mark it. The callback is looked up when the row is
	// clicked, not when it is built: the app assigns them after NewSidebar.
	addNav := func(page NavPage, icon, text, tip string, target func() func()) *gtk.Label {
		btn := gtk.NewButton()
		btn.AddCSSClass("sidebar-item")
		btn.AddCSSClass("nav-row")
		row, count := navRow(icon, text)
		btn.SetChild(row)
		btn.SetTooltipText(tip)
		btn.ConnectClicked(func() { fire(target()) })
		nav.Append(btn)
		s.navBtns[page] = btn
		return count
	}

	// The way back. The welcome screen holds the cast, the worlds and the
	// greeting, and until there was this it could only be reached by having no
	// chat open, which, once you had opened one, meant not at all.
	addNav(NavHome, IconHome, "Home", "Your cast and your worlds",
		func() func() { return s.OnHome })
	s.charsCount = addNav(NavCharacters, IconCharacters, "Characters", "Browse and import characters (Ctrl+K)",
		func() func() { return s.OnCharacters })
	s.worldsCount = addNav(NavWorlds, IconWorlds, "Worlds", "Settings your characters live in (Ctrl+W)",
		func() func() { return s.OnWorlds })
	addNav(NavKnowledge, IconKnowledge, "Knowledge", "Notes and pages your chats draw on",
		func() func() { return s.OnKnowledge })
	addNav(NavPrompts, IconDesigner, "Prompts", "Read, edit and optimize every prompt Astral sends",
		func() func() { return s.OnPrompts })
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

	// The grouping toggle sits beside the search because both are about how
	// the list below is read.
	s.groupBtn = gtk.NewToggleButton()
	s.groupBtn.SetIconName(IconCharacters)
	s.groupBtn.AddCSSClass("flat")
	s.groupBtn.AddCSSClass("group-toggle")
	s.groupBtn.SetVAlign(gtk.AlignCenter)
	s.groupBtn.SetTooltipText("Group chats by character")
	s.groupBtn.ConnectToggled(func() {
		on := s.groupBtn.Active()
		if on == s.byCharacter {
			return // set from the saved choice, which is not news
		}
		s.byCharacter = on
		s.lastSig = 0
		s.SetChats(s.chats)
		if s.OnGroupByCharacter != nil {
			s.OnGroupByCharacter(on)
		}
	})
	s.search.SetHExpand(true)
	find := gtk.NewBox(gtk.OrientationHorizontal, 4)
	find.AddCSSClass("sidebar-find")
	find.Append(s.search)
	find.Append(s.groupBtn)
	s.widget.Append(find)

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
	s.SetProfile("", "", "")

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
	// Your personas first, to switch between with one click; filled by
	// SetPersonas.
	s.personaBox = gtk.NewBox(gtk.OrientationVertical, 2)
	box.Append(s.personaBox)
	add(IconEdit, "Personas", func() { fire(s.OnPersona) })
	add(IconAdd, "Create a Persona", func() { fire(s.OnCreatePersona) })
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
func (s *Sidebar) SetProfile(name, subtitle, picture string) {
	// Called whenever the list is refreshed, twice a turn, and the picture is
	// read and scaled each time it is drawn: unchanged, it is left alone.
	if shown := name + "\x00" + subtitle + "\x00" + picture; shown == s.profileShown {
		return
	} else {
		s.profileShown = shown
	}
	unnamed := name == ""
	if unnamed {
		name = "You"
	}

	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.Append(NewPersonaAvatar(chars.Profile{Name: name, AvatarPath: picture}, 26))
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
	// A prompt to set one up is useful; a fragment of one is not, so for a
	// persona the line under the name is left empty.
	if strings.TrimSpace(subtitle) != "" {
		m := gtk.NewLabel(subtitle)
		m.SetXAlign(0)
		m.SetEllipsize(pango.EllipsizeEnd)
		m.AddCSSClass("profile-sub")
		col.Append(m)
	} else if unnamed {
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

// navRow is a navigation row's content, the way Atlas Monitor lays its pages
// out: the icon and the name from the start, and at the end a count, empty
// until SetCounts fills it in.
func navRow(icon, text string) (*gtk.Box, *gtk.Label) {
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	if icon != "" {
		box.Append(gtk.NewImageFromIconName(icon))
	}
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.SetHExpand(true)
	l.SetEllipsize(pango.EllipsizeEnd)
	box.Append(l)
	count := gtk.NewLabel("")
	count.AddCSSClass("sidebar-count")
	count.SetVisible(false)
	box.Append(count)
	return box, count
}

// SetCounts shows how many characters and worlds there are beside their
// rows, and nothing for none. A negative count means it could not be read, and
// leaves what the row shows as it was: a failed query is not a library that has
// emptied.
func (s *Sidebar) SetCounts(characters, worlds int) {
	for _, c := range []struct {
		l *gtk.Label
		n int
	}{{s.charsCount, characters}, {s.worldsCount, worlds}} {
		if c.l == nil || c.n < 0 {
			continue
		}
		c.l.SetVisible(c.n > 0)
		c.l.SetText(fmt.Sprint(c.n))
	}
}

// SetActivePage marks the navigation row for what the window is showing, and
// clears the mark from every other row, chats included. Pass NavNone while a
// chat is showing: that chat's own row is marked then, as Select says.
func (s *Sidebar) SetActivePage(p NavPage) {
	s.page = p
	for page, btn := range s.navBtns {
		if page == p {
			btn.AddCSSClass("selected")
		} else {
			btn.RemoveCSSClass("selected")
		}
	}
	s.applySelection()
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

	if len(chats) == 0 {
		empty := gtk.NewLabel("No chats yet.")
		empty.SetXAlign(0)
		empty.AddCSSClass("sidebar-empty")
		empty.SetWrap(true)
		s.listBox.Append(empty)
		return
	}

	live, archived := splitArchived(chats)
	for _, sec := range chatSections(live, s.byCharacter) {
		head := gtk.NewLabel(sec.Title)
		head.SetXAlign(0)
		head.SetEllipsize(pango.EllipsizeEnd)
		head.AddCSSClass("sidebar-section")
		s.listBox.Append(head)
		for _, ch := range sec.Chats {
			if s.firstChat == 0 {
				s.firstChat = ch.ID
			}
			s.listBox.Append(s.chatRow(ch))
		}
	}
	// The archive is last in either grouping.
	if len(archived) > 0 {
		s.listBox.Append(s.archivedHeader(len(archived)))
		if s.archivedOpen {
			for _, ch := range archived {
				s.listBox.Append(s.chatRow(ch))
			}
		}
	}
	s.applySelection()
	s.keepMarks()
}

// chatSignature digests what the list actually draws: order, identity, title,
// tint and whether it is archived. Anything else about a chat can change
// without the sidebar looking any different.
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
		write([]byte(c.AvatarPath))
		write([]byte(c.Kind))
		writeInt(int64(c.CastSize))
		if c.Archived {
			write([]byte{1})
		}
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

// chatAvatarSize is the picture beside a chat's title, in pixels.
const chatAvatarSize = 22

// chatMark is what stands beside a chat's title: its character's avatar, or
// for a conversation with no character, a tile saying what kind it is.
func chatMark(ch store.Chat) gtk.Widgetter {
	var w gtk.Widgetter
	if ch.CharacterName != "" {
		w = NewCharacterAvatar(chars.Character{Name: ch.CharacterName, Accent: ch.Accent,
			AvatarPath: ch.AvatarPath}, chatAvatarSize)
	} else {
		icon := IconChat
		switch ch.Kind {
		case store.KindDesigner, store.KindStyleDesigner, store.KindWorldDesigner,
			store.KindPersonaDesigner, store.KindPromptOptimizer:
			icon = IconDesigner
		case store.KindNovel:
			icon = IconDraft
		}
		tile := gtk.NewBox(gtk.OrientationHorizontal, 0)
		tile.AddCSSClass("avatar")
		tile.AddCSSClass("chat-kind")
		tile.SetSizeRequest(chatAvatarSize, chatAvatarSize)
		// Homogeneous rather than an expanding icon: expansion spreads up
		// from a child, and it made the tile as wide as the row allowed.
		tile.SetHomogeneous(true)
		tile.SetHExpand(false)
		img := gtk.NewImageFromIconName(icon)
		img.SetHAlign(gtk.AlignCenter)
		tile.Append(img)
		w = tile
	}
	base := gtk.BaseWidget(w)
	base.AddCSSClass("chat-avatar")
	base.SetVAlign(gtk.AlignCenter)
	return w
}

// chatRowWith is a chat's row, with the line a search matched under the title
// when there is one.
// lastReplyLine is a reply as one plain line: without its markup, and without
// a speaker's name on the front.
func lastReplyLine(s string) string {
	s = strings.NewReplacer("*", "", "\"", "", "_", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func (s *Sidebar) chatRowWith(ch store.Chat, snippet string) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("sidebar-item")
	btn.AddCSSClass("chat-row")

	box := gtk.NewBox(gtk.OrientationHorizontal, 9)

	// The character's picture, or their initial on their tint when they have
	// none, so a scene is found down a long list by the face in it. It was a
	// tinted dot, which said little more than that two scenes shared someone.
	dot := chatMark(ch)
	box.Append(dot)

	title := ch.Title
	if title == "" {
		title = "New Chat"
	}
	// A scene with one character reads as who it is with and where it got
	// to: their name, and the start of their newest reply under it.
	if snippet == "" && ch.CharacterName != "" && ch.CastSize <= 1 {
		title = ch.CharacterName
		if r := lastReplyLine(ch.LastReply); r != "" {
			snippet = "› " + r
		}
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
	more.SetTooltipText("Rename, export, archive, select or delete")
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
	s.attachRowMenu(btn, ch)
	s.rows[id] = &chatRow{btn: btn, box: box, dot: dot, archived: ch.Archived}
	s.order = append(s.order, id)
	return btn
}

// attachRowMenu wires the right-click menu on a conversation row.
func (s *Sidebar) attachRowMenu(btn *gtk.Button, ch store.Chat) {
	id := ch.ID
	// Built on the first right-click rather than with the row. A popover menu
	// registers itself with the window's actions as it is made, and doing
	// that for every chat was nearly half of what drawing the list cost:
	// measured with 400 chats, a quarter of a second at startup and again
	// whenever a message reordered the list.
	var pop *gtk.PopoverMenu
	show := func(x, y float64) {
		if pop == nil {
			pop = rowPopover(btn, ch)
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
func rowPopover(btn *gtk.Button, ch store.Chat) *gtk.PopoverMenu {
	// The target is attached as a real GVariant rather than encoded into a
	// detailed action string. "win.rename-chat(7)" looks right but parses its
	// target as an int32, while the action is declared to take an int64, the
	// types do not match, so GTK quietly refuses to activate the item and the
	// menu entry does nothing at all when clicked.
	menu := gio.NewMenu()
	archive, archiveAction := "Archive", "win.archive-chat"
	if ch.Archived {
		archive, archiveAction = "Unarchive", "win.unarchive-chat"
	}
	type entry struct {
		label  string
		action string
	}
	items := []entry{{"Rename…", "win.rename-chat"}, {"Export…", "win.export-chat"}}
	if scene.CanContinue(ch) {
		items = append(items, entry{"Continue in a New Chat", "win.continue-chat"})
	}
	items = append(items, entry{archive, archiveAction}, entry{"Select", "win.select-chat"},
		entry{"Delete", "win.delete-chat"})
	for _, it := range items {
		item := gio.NewMenuItem(it.label, "")
		item.SetActionAndTargetValue(it.action, glib.NewVariantInt64(ch.ID))
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

// Select records which conversation is open and marks its row, unless the
// window is showing one of the navigation pages, which then holds the mark
// alone. The row is marked when SetActivePage says a chat is showing again.
func (s *Sidebar) Select(id int64) {
	s.selected = id
	s.applySelection()
}

func (s *Sidebar) applySelection() {
	for id, row := range s.rows {
		if id == s.selected && s.page == NavNone {
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
		return "Previous 7 Days"
	case !t.Before(startOfToday.AddDate(0, 0, -30)):
		return "Previous 30 Days"
	default:
		return "Earlier"
	}
}

func fire(fn func()) {
	if fn != nil {
		fn()
	}
}

// PersonaChoice is one of your personas as the profile menu lists it.
type PersonaChoice struct {
	ID    int64
	Name  string
	Facts string
}

// SetPersonas lists your personas at the top of the profile menu, with a
// check on the one new chats are played as. A single persona is not listed:
// there is nothing to switch to.
func (s *Sidebar) SetPersonas(list []PersonaChoice, active int64) {
	if s.personaBox == nil {
		return
	}
	for child := s.personaBox.FirstChild(); child != nil; child = s.personaBox.FirstChild() {
		s.personaBox.Remove(child)
	}
	if len(list) < 2 {
		s.personaBox.SetVisible(false)
		return
	}
	for _, p := range list {
		id := p.ID
		b := gtk.NewButton()
		b.AddCSSClass("sidebar-item")
		icon := ""
		if id == active {
			icon = IconCheck
		}
		row := rowContent(icon, p.Name)
		if icon == "" {
			// The names line up whether or not they carry the check.
			spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
			spacer.SetSizeRequest(16, -1)
			row.Prepend(spacer)
		}
		b.SetChild(row)
		tip := "Play new chats as " + p.Name
		if p.Facts != "" {
			tip += " (" + p.Facts + ")"
		}
		b.SetTooltipText(tip)
		b.ConnectClicked(func() {
			s.profileMenu.Popdown()
			if s.OnUsePersona != nil {
				s.OnUsePersona(id)
			}
		})
		s.personaBox.Append(b)
	}
	sep := gtk.NewSeparator(gtk.OrientationHorizontal)
	sep.SetMarginTop(4)
	sep.SetMarginBottom(4)
	s.personaBox.Append(sep)
	s.personaBox.SetVisible(true)
}
