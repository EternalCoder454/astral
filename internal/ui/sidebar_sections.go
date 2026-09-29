package ui

import (
	"sort"
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
)

// How the sidebar's chats are sectioned: by the day they were last written in,
// or by the character they are with, and always with the archive last.

// chatSection is one titled run of chats in the list.
type chatSection struct {
	Title string
	Chats []store.Chat
}

// otherChats is where a chat with no character goes when the list is grouped
// by character: an assistant chat, a designer session, a scene in a world
// with nobody in particular there.
const otherChats = "Other Chats"

// splitArchived separates the chats that are put away from the rest, each in
// the order it came in.
func splitArchived(chats []store.Chat) (live, archived []store.Chat) {
	for _, ch := range chats {
		if ch.Archived {
			archived = append(archived, ch)
		} else {
			live = append(live, ch)
		}
	}
	return live, archived
}

// chatSections sections the chats for the list, in the grouping asked for.
func chatSections(chats []store.Chat, byCharacter bool) []chatSection {
	if byCharacter {
		return sectionsByCharacter(chats)
	}
	return sectionsByDate(chats)
}

// sectionsByDate is the list as it has always been: a heading for each stretch
// of time, in the order the chats arrive, which is most recent first.
func sectionsByDate(chats []store.Chat) []chatSection {
	var out []chatSection
	for _, ch := range chats {
		title := sectionFor(ch.UpdatedAt)
		if n := len(out); n == 0 || out[n-1].Title != title {
			out = append(out, chatSection{Title: title})
		}
		out[len(out)-1].Chats = append(out[len(out)-1].Chats, ch)
	}
	return out
}

// sectionsByCharacter gives each character one section named after them,
// ordered by their most recent chat, with the most recent chat first inside
// it. A scene with a cast goes under its lead character, who is the one the
// chat is filed under. Chats with nobody go last under "Other Chats", so the
// catch-all does not move about the list as it fills.
//
// The chats are sorted here rather than trusted to arrive in order, so the
// grouping does not quietly depend on the query that fed it. Characters are
// told apart by id, not by name: two characters may share one.
func sectionsByCharacter(chats []store.Chat) []chatSection {
	sorted := append([]store.Chat(nil), chats...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].UpdatedAt.After(sorted[j].UpdatedAt)
	})

	var out []chatSection
	at := map[int64]int{}
	var other []store.Chat
	for _, ch := range sorted {
		if ch.CharacterID == 0 || ch.CharacterName == "" {
			other = append(other, ch)
			continue
		}
		i, ok := at[ch.CharacterID]
		if !ok {
			i = len(out)
			at[ch.CharacterID] = i
			out = append(out, chatSection{Title: ch.CharacterName})
		}
		out[i].Chats = append(out[i].Chats, ch)
	}
	if len(other) > 0 {
		out = append(out, chatSection{Title: otherChats, Chats: other})
	}
	return out
}

// archivedHeader is the heading of the archive at the end of the list. It is a
// button because the archive starts closed: its rows are only built while it
// is open, so a long archive costs the list nothing.
func (s *Sidebar) archivedHeader(count int) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("sidebar-item")
	btn.AddCSSClass("archived-toggle")

	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	name := gtk.NewLabel("Archived")
	name.SetXAlign(0)
	name.AddCSSClass("archived-name")
	box.Append(name)
	n := gtk.NewLabel(strconv.Itoa(count))
	n.SetXAlign(0)
	n.SetHExpand(true)
	n.AddCSSClass("archived-count")
	box.Append(n)
	icon, tip := "pan-end-symbolic", "Show archived chats"
	if s.archivedOpen {
		icon, tip = "pan-down-symbolic", "Hide archived chats"
	}
	box.Append(gtk.NewImageFromIconName(icon))
	btn.SetChild(box)
	btn.SetTooltipText(tip)

	btn.ConnectClicked(func() {
		s.archivedOpen = !s.archivedOpen
		s.lastSig = 0 // the same chats, drawn differently
		s.SetChats(s.chats)
	})
	return btn
}

// SetGroupByCharacter sets how the list is grouped without telling anyone, for
// restoring what was chosen last time.
func (s *Sidebar) SetGroupByCharacter(on bool) {
	s.byCharacter = on
	s.groupBtn.SetActive(on)
	s.lastSig = 0
	if len(s.chats) > 0 {
		s.SetChats(s.chats)
	}
}
