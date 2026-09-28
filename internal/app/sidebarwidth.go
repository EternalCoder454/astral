package app

import (
	"math"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/graphene"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
)

// Dragging the sidebar wider.
//
// It was a fixed proportion of the window, which is defensible and was wrong in
// practice: the width you want depends on how long your chat titles are, not on
// how wide your monitor is. A person with one long-titled scene wants it wide and
// a person who navigates by the four buttons wants it narrow, and neither of them
// wants to be told.

// Sidebar width bounds. The lower one is where the four navigation buttons stop
// fitting their labels; the upper one is where the transcript starts losing its
// readable column on an ordinary window.
const (
	minSidebarWidth = 180
	maxSidebarWidth = 520
)

// sidebarWithGrip is the sidebar with a drag handle down its trailing edge.
func (a *App) sidebarWithGrip() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)

	inner := a.sidebar.Widget()
	box.Append(inner)

	grip := gtk.NewBox(gtk.OrientationVertical, 0)
	grip.AddCSSClass("sidebar-grip")
	grip.SetSizeRequest(6, -1)
	grip.SetVExpand(true)
	// The cursor is what tells anybody this is draggable at all: a six pixel
	// strip with no affordance is a six pixel strip nobody touches.
	grip.SetCursor(gdk.NewCursorFromName("col-resize", nil))

	// The width is worked out from where the pointer is in the window, not
	// from how far it has moved across the grip: the grip moves with the width
	// it sets, so measured from the grip every other motion cancelled the one
	// before it and the edge crept after the pointer at half its speed.
	var start int
	var grabbed, from float64
	drag := gtk.NewGestureDrag()
	drag.ConnectDragBegin(func(x, y float64) {
		start = a.cfg.SidebarWidth
		grabbed = x
		from, _ = a.windowX(grip, x)
		// Lit for the whole drag, not only while the pointer happens to be
		// over the strip, so the edge being moved stays the thing you see.
		grip.AddCSSClass("dragging")
	})
	drag.ConnectDragUpdate(func(dx, dy float64) {
		// Where the pointer is now, across the grip as it is laid out at this
		// moment, which is also how the gesture measured it.
		if x, ok := a.windowX(grip, grabbed+dx); ok {
			a.applySidebarWidth(start + int(math.Round(x-from)))
		}
	})
	drag.ConnectDragEnd(func(dx, dy float64) {
		grip.RemoveCSSClass("dragging")
		// Saved once, at the end. Writing the file on every pointer motion would
		// be a few hundred writes to drag it across the screen.
		if err := store.SaveConfig(a.cfg); err != nil {
			a.toast("Could not save the sidebar width: " + err.Error())
		}
	})
	grip.AddController(drag)

	// Back to the default, for anyone who has dragged it somewhere they regret.
	reset := gtk.NewGestureClick()
	reset.SetButton(1)
	reset.ConnectPressed(func(n int, x, y float64) {
		if n < 2 {
			return
		}
		a.applySidebarWidth(store.DefaultConfig().SidebarWidth)
		if err := store.SaveConfig(a.cfg); err != nil {
			a.toast("Could not save the sidebar width: " + err.Error())
		}
	})
	grip.AddController(reset)

	grip.SetTooltipText("Drag to resize the sidebar, or double click to reset it")
	box.Append(grip)
	return box
}

// applySidebarWidth pins the sidebar to an exact width.
//
// An OverlaySplitView sizes its sidebar as a fraction of the window, clamped
// between a minimum and a maximum. Setting both ends to the same number turns
// that into a fixed width, and a fraction of one keeps it there on every window
// size rather than only on wide ones.
func (a *App) applySidebarWidth(w int) {
	if w < minSidebarWidth {
		w = minSidebarWidth
	}
	if w > maxSidebarWidth {
		w = maxSidebarWidth
	}
	a.cfg.SidebarWidth = w
	if a.split == nil {
		return
	}
	a.split.SetMinSidebarWidth(float64(w))
	a.split.SetMaxSidebarWidth(float64(w))
	a.split.SetSidebarWidthFraction(1)
}

// windowX is where a point x across widget w lies across the window.
func (a *App) windowX(w gtk.Widgetter, x float64) (float64, bool) {
	p := graphene.NewPointAlloc().Init(float32(x), 0)
	out, ok := gtk.BaseWidget(w).ComputePoint(a.win, p)
	if !ok {
		return 0, false
	}
	return float64(out.X()), true
}
