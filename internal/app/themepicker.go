package app

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
	"astral/internal/theme"
)

// themePicker is the Appearance page's choice of colour theme: one circle per
// theme, split between its canvas and its accent, with the chosen one ringed.
//
// Circles rather than a dropdown because the thing being chosen is a colour, and a
// list of names makes you pick one to find out what it looks like. Split circles
// rather than single ones because a theme is two decisions, what the window is and
// what stands out against it, and either alone is a misleading preview.
//
// Unlike everything else in Settings a choice here is applied and saved at once,
// not on Save. Nothing else on the dialog can be judged without trying it, and
// there is no way to judge a colour theme except to see it on the window behind.
// Save and Cancel afterwards leave it alone.
//
// There is no circle for following the desktop. It is not a palette, so it is a
// quiet button under the circles, shown only when there is a theme to go back from.
type themePicker struct {
	a *App

	box     *gtk.Box
	buttons []*gtk.ToggleButton
	follow  *gtk.Button
	name    *gtk.Label
	summary *gtk.Label

	// handlers are every signal connection made on the widgets above. The
	// handlers reach back to the picker, and the picker holds the widgets, so
	// left connected they are a circle through GTK that Go's collector cannot
	// see. release cuts them when the dialog closes.
	handlers []connection
	syncing  bool
}

// connection is a signal handler that has to be cut when a dialog closes, because
// what it does reaches something above the widget it is connected to.
type connection struct {
	obj coreglib.Objector
	h   coreglib.SignalHandle
}

func (c connection) cut() { coreglib.BaseObject(c.obj).HandlerDisconnect(c.h) }

func (a *App) newThemePicker() *themePicker {
	p := &themePicker{a: a}

	p.name = gtk.NewLabel("")
	p.name.SetXAlign(0)
	p.name.AddCSSClass("field-label")
	p.summary = wrappingLabel("")
	p.summary.AddCSSClass("settings-hint")

	// Two rows, five over four, each centred so the short one sits under the
	// middle of the long one; a FlowBox can only start every row at the left.
	// Each row wraps by itself where nine circles and their names are wider
	// than the dialog is at its narrowest, a sheet the width of the window,
	// instead of forcing the dialog wider or clipping the names. A size group
	// keeps the columns even whatever the names' widths.
	const firstRow = 5
	rows := gtk.NewBox(gtk.OrientationVertical, 12)
	rows.SetHAlign(gtk.AlignCenter)
	rows.SetMarginTop(6)
	rows.SetMarginBottom(6)
	var flows []*adw.WrapBox
	for range 2 {
		f := adw.NewWrapBox()
		f.SetAlign(0.5)
		f.SetChildSpacing(18)
		f.SetLineSpacing(12)
		f.SetHAlign(gtk.AlignCenter)
		rows.Append(f)
		flows = append(flows, f)
	}
	cells := gtk.NewSizeGroup(gtk.SizeGroupHorizontal)

	for i, t := range theme.Themes {
		id := t.ID
		swatch := gtk.NewToggleButton()
		// Centred, not filled. A button fills its cell by default, and the cell is
		// as wide as the name under it, so every theme with a name longer than the
		// circle was drawn as an oval, and the ring round the chosen one with it.
		swatch.SetHAlign(gtk.AlignCenter)
		swatch.SetVAlign(gtk.AlignCenter)
		swatch.AddCSSClass("theme-swatch")
		swatch.AddCSSClass(theme.SwatchClass(id))
		swatch.SetTooltipText(t.Summary)
		// The button has no label of its own, so without this a screen reader would
		// announce an unnamed toggle nine times over.
		swatch.UpdateProperty(
			[]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
			[]coreglib.Value{*coreglib.NewValue(t.Name)})

		caption := gtk.NewLabel(t.Name)
		caption.AddCSSClass("caption")

		cell := gtk.NewBox(gtk.OrientationVertical, 6)
		cell.SetHAlign(gtk.AlignCenter)
		cell.Append(swatch)
		cell.Append(caption)
		cells.AddWidget(cell)
		flows[min(i/firstRow, 1)].Append(cell)

		h := swatch.ConnectToggled(func() {
			if p.syncing {
				return
			}
			if !swatch.Active() {
				// Clicking the chosen one again would turn it off and leave nothing
				// selected. It stays chosen.
				swatch.SetActive(true)
				return
			}
			p.choose(id)
		})
		p.handlers = append(p.handlers, connection{swatch, h})
		p.buttons = append(p.buttons, swatch)
	}

	// A small, quiet button: the way back from a choice is a correction, not an
	// action, and much too loud as a full one.
	p.follow = gtk.NewButtonWithLabel("Follow the Desktop Instead")
	p.follow.SetHAlign(gtk.AlignCenter)
	p.follow.AddCSSClass("flat")
	p.follow.AddCSSClass("theme-follow")
	h := p.follow.ConnectClicked(func() { p.choose(theme.Follow) })
	p.handlers = append(p.handlers, connection{p.follow, h})

	outer, card := groupCard("Theme")
	head := gtk.NewBox(gtk.OrientationVertical, 2)
	head.Append(p.name)
	head.Append(p.summary)
	card.Append(head)
	card.Append(rows)
	card.Append(p.follow)
	p.box = outer

	p.sync()
	return p
}

// choose applies a setting and saves it, then brings the circles into line.
func (p *themePicker) choose(setting string) {
	if p.a.cfg.Theme != setting {
		p.a.cfg.Theme = setting
		if err := store.SaveConfig(p.a.cfg); err != nil {
			p.a.toast("Could not save the theme: " + err.Error())
		}
		p.a.theme.apply(setting)
	}
	p.sync()
}

// sync marks the chosen circle and leaves the rest clear, and says what is chosen.
// The guard is for the notify that setting a button's state fires: without it,
// clearing the others would re-enter choose through their own handlers.
func (p *themePicker) sync() {
	p.syncing = true
	for i, b := range p.buttons {
		b.SetActive(theme.Themes[i].ID == p.a.cfg.Theme)
	}
	p.syncing = false

	if t, ok := theme.ByID(p.a.cfg.Theme); ok {
		p.name.SetLabel(t.Name)
		p.summary.SetLabel(t.Summary)
		p.follow.SetVisible(true)
		return
	}
	p.name.SetLabel("Following the Desktop")
	p.summary.SetLabel("Astral draws Ink when the desktop is dark and Paper when it is light. " +
		"Choose a theme to set one here instead.")
	p.follow.SetVisible(false)
}

// release disconnects every handler, so that the dialog and everything in it can
// be freed once it has closed.
func (p *themePicker) release() {
	for _, c := range p.handlers {
		c.cut()
	}
	p.handlers = nil
}
