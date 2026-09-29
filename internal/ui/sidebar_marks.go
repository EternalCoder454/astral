package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/graphene"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Picking out several chats at once.
//
// Ctrl and a click marks a chat or unmarks it, Shift and a click marks the run
// of chats between the last one marked and this one, and while anything is
// marked a plain click marks too, the way a phone's selection works, so a
// touchscreen gets there through Select on a row's menu. A bar under the list
// says how many are marked and archives or deletes them; Escape, or the bar's close
// button, lets them go.

// chatRow is one conversation in the list and the parts of it that change.
type chatRow struct {
	btn *gtk.Button
	box *gtk.Box
	dot gtk.Widgetter
	// archived is whether the chat is put away, which decides what the bar's
	// Archive button does to it.
	archived bool
	// check stands in for the avatar while the row is marked. Made the first
	// time it is needed: most rows are never marked.
	check *gtk.Image
}

// Rows are found under the pointer by widget name: one gesture on the list
// serves every row, where one per row would be several hundred closures that
// gotk4 keeps alive for the life of the process.
const (
	rowPrefix   = "chat-row-"
	rowMoreName = "chat-row-more"
)

func rowName(id int64) string { return rowPrefix + strconv.FormatInt(id, 10) }

// rowAt finds the chat under a point on the list, and whether the point is on
// the row's menu button.
func (s *Sidebar) rowAt(x, y float64) (id int64, onMore bool) {
	picked := s.listBox.Pick(x, y, gtk.PickDefault)
	if picked == nil {
		return 0, false
	}
	for w := gtk.BaseWidget(picked); w != nil; {
		name := w.Name()
		if name == rowMoreName {
			onMore = true
		} else if rest, ok := strings.CutPrefix(name, rowPrefix); ok {
			if n, err := strconv.ParseInt(rest, 10, 64); err == nil {
				return n, onMore
			}
		}
		parent := w.Parent()
		if parent == nil {
			break
		}
		w = gtk.BaseWidget(parent)
	}
	return 0, false
}

// watchListClicks puts the one gesture on the list that sees a click before
// the row does, so a click that marks a chat does not also open it.
func (s *Sidebar) watchListClicks() {
	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_PRIMARY)
	click.SetPropagationPhase(gtk.PhaseCapture)
	click.ConnectPressed(func(n int, x, y float64) {
		id, onMore := s.rowAt(x, y)
		if id == 0 {
			return
		}
		mods := click.CurrentEventState()
		switch {
		case onMore && len(s.marked) == 0:
			s.openMenuAt(id, x, y)
		case mods&gdk.ShiftMask != 0:
			s.markRun(id)
		case mods&gdk.ControlMask != 0, len(s.marked) > 0:
			s.toggleMark(id)
		default:
			return // an ordinary click, which opens the chat
		}
		click.SetState(gtk.EventSequenceClaimed)
	})
	s.listBox.AddController(click)

	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if len(s.marked) == 0 {
			return false
		}
		switch keyval {
		case gdk.KEY_Escape:
			s.ClearMarks()
		case gdk.KEY_Delete, gdk.KEY_KP_Delete:
			s.deleteMarked()
		case gdk.KEY_a, gdk.KEY_A:
			if state&gdk.ControlMask == 0 {
				return false
			}
			s.markAll()
		default:
			return false
		}
		return true
	})
	s.widget.AddController(keys)
}

// openMenuAt opens a row's menu at a point on the list.
func (s *Sidebar) openMenuAt(id int64, x, y float64) {
	show, ok := s.rowMenus[id]
	row := s.rows[id]
	if !ok || row == nil {
		return
	}
	p := graphene.NewPointAlloc().Init(float32(x), float32(y))
	if at, ok := s.listBox.ComputePoint(row.btn, p); ok {
		show(float64(at.X()), float64(at.Y()))
	}
}

// buildMarkBar is the bar that appears under the list while chats are marked.
func (s *Sidebar) buildMarkBar() *gtk.Revealer {
	bar := gtk.NewBox(gtk.OrientationVertical, 6)
	bar.AddCSSClass("mark-bar")

	top := gtk.NewBox(gtk.OrientationHorizontal, 6)
	s.markLabel = gtk.NewLabel("")
	s.markLabel.SetXAlign(0)
	s.markLabel.SetHExpand(true)
	s.markLabel.AddCSSClass("mark-count")
	top.Append(s.markLabel)
	done := gtk.NewButtonFromIconName(IconClose)
	done.AddCSSClass("flat")
	done.AddCSSClass("mark-close")
	done.SetTooltipText("Stop selecting (Escape)")
	done.ConnectClicked(s.ClearMarks)
	top.Append(done)
	bar.Append(top)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	buttons.SetHomogeneous(true)
	all := gtk.NewButtonWithLabel("Select All")
	all.SetTooltipText("Select every chat in the list (Ctrl+A)")
	all.ConnectClicked(s.markAll)
	buttons.Append(all)
	s.archiveBtn = gtk.NewButtonWithLabel("Archive")
	s.archiveBtn.ConnectClicked(s.archiveMarked)
	buttons.Append(s.archiveBtn)
	del := gtk.NewButtonWithLabel("Delete")
	del.AddCSSClass("destructive-action")
	del.SetTooltipText("Delete the selected chats (Delete)")
	del.ConnectClicked(s.deleteMarked)
	buttons.Append(del)
	bar.Append(buttons)

	s.markBar = gtk.NewRevealer()
	s.markBar.SetTransitionType(gtk.RevealerTransitionTypeSlideUp)
	s.markBar.SetTransitionDuration(140)
	s.markBar.SetChild(bar)
	s.markBar.SetRevealChild(false)
	return s.markBar
}

// Mark picks out one chat, as Select on its menu does.
func (s *Sidebar) Mark(id int64) {
	if _, ok := s.rows[id]; !ok {
		return
	}
	s.marked[id] = true
	s.anchor = id
	s.showMarks()
	s.focusRow(id)
}

// toggleMark marks a chat, or unmarks it if it was.
func (s *Sidebar) toggleMark(id int64) {
	if s.marked[id] {
		delete(s.marked, id)
	} else {
		s.marked[id] = true
	}
	s.anchor = id
	s.showMarks()
	s.focusRow(id)
}

// markRun marks every chat from the last one marked to this one. With nothing
// marked yet it runs from the chat that is open, as a file manager runs from
// the file that is selected.
func (s *Sidebar) markRun(id int64) {
	from := s.anchor
	if _, ok := s.rows[from]; !ok {
		from = s.selected
	}
	a, b := indexOf(s.order, from), indexOf(s.order, id)
	if a < 0 {
		a = b
	}
	if a > b {
		a, b = b, a
	}
	for _, each := range s.order[a : b+1] {
		s.marked[each] = true
	}
	if s.anchor == 0 {
		s.anchor = id
	}
	s.showMarks()
	s.focusRow(id)
}

// focusRow gives a row the keyboard. Keys go where the focus is, and a click
// that marks is taken from the row before the row can take the focus itself,
// so without this Escape and Delete would go to whatever had it before, most
// often the composer.
func (s *Sidebar) focusRow(id int64) {
	if row, ok := s.rows[id]; ok {
		row.btn.GrabFocus()
	}
}

func (s *Sidebar) markAll() {
	for _, id := range s.order {
		s.marked[id] = true
	}
	s.showMarks()
}

// ClearMarks unmarks everything.
func (s *Sidebar) ClearMarks() {
	if len(s.marked) == 0 {
		return
	}
	clear(s.marked)
	s.anchor = 0
	s.showMarks()
}

// Marked is the marked chats in the order the list shows them.
func (s *Sidebar) Marked() []int64 {
	var ids []int64
	for _, id := range s.order {
		if s.marked[id] {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Sidebar) deleteMarked() {
	ids := s.Marked()
	if len(ids) == 0 || s.OnDeleteChats == nil {
		return
	}
	s.ClearMarks()
	s.OnDeleteChats(ids)
}

// archiveMarked puts the marked chats away, or brings them back when every one
// of them is already put away.
func (s *Sidebar) archiveMarked() {
	ids := s.Marked()
	if len(ids) == 0 || s.OnArchiveChats == nil {
		return
	}
	archive := !s.markedAllArchived()
	s.ClearMarks()
	s.OnArchiveChats(ids, archive)
}

// markedAllArchived says whether every marked chat is one that is put away.
func (s *Sidebar) markedAllArchived() bool {
	for _, id := range s.Marked() {
		if row := s.rows[id]; row != nil && !row.archived {
			return false
		}
	}
	return true
}

// keepMarks is run after the list is drawn again: a marked chat that is still
// listed stays marked, and one that has gone is forgotten.
func (s *Sidebar) keepMarks() {
	if len(s.marked) == 0 {
		return
	}
	// A search shows only some chats, and the others stay marked behind it,
	// so only a full list can say a chat has gone.
	if !s.searching {
		for id := range s.marked {
			if _, ok := s.rows[id]; !ok {
				delete(s.marked, id)
			}
		}
	}
	s.showMarks()
}

// showMarks brings the rows and the bar into line with what is marked.
func (s *Sidebar) showMarks() {
	for id, row := range s.rows {
		on := s.marked[id]
		if on {
			row.btn.AddCSSClass("marked")
			if row.check == nil {
				row.check = gtk.NewImageFromIconName(IconCheck)
				row.check.AddCSSClass("chat-row-check")
				row.box.InsertChildAfter(row.check, row.dot)
			}
		} else {
			row.btn.RemoveCSSClass("marked")
		}
		gtk.BaseWidget(row.dot).SetVisible(!on)
		if row.check != nil {
			row.check.SetVisible(on)
		}
	}
	n := len(s.marked)
	if n > 0 {
		s.markLabel.SetText(fmt.Sprintf("%d Selected", n))
		if s.markedAllArchived() {
			s.archiveBtn.SetLabel("Unarchive")
			s.archiveBtn.SetTooltipText("Bring the selected chats back")
		} else {
			s.archiveBtn.SetLabel("Archive")
			s.archiveBtn.SetTooltipText("Put the selected chats away")
		}
	}
	s.markBar.SetRevealChild(n > 0)
	if n > 0 {
		s.widget.AddCSSClass("marking")
	} else {
		s.widget.RemoveCSSClass("marking")
	}
}

func indexOf(ids []int64, id int64) int {
	for i, each := range ids {
		if each == id {
			return i
		}
	}
	return -1
}
