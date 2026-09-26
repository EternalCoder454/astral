package app

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/store"
	"astral/internal/ui"
)

// showCastPicker chooses who is in a scene with more than one character.
//
// A list with checkboxes rather than the one-click rows the cast page uses: a
// group is a set, so there is a moment between choosing and starting that a
// single click cannot express.
func (a *App) showCastPicker() {
	characters, err := a.store.Characters()
	if err != nil {
		a.toast("Could not read your characters: " + err.Error())
		return
	}
	if len(characters) < 2 {
		a.toast("A group needs at least two characters.")
		return
	}

	d := adw.NewDialog()
	d.SetTitle("Who is in this scene?")
	d.SetContentWidth(560)
	d.SetContentHeight(620)

	list := gtk.NewBox(gtk.OrientationVertical, 8)
	list.SetMarginTop(14)
	list.SetMarginBottom(14)
	list.SetMarginStart(14)
	list.SetMarginEnd(14)

	// Selection order is the cast order, which is the order they are introduced
	// to the model and the order the first greeting comes from. Ticking someone
	// first should mean something.
	var chosen []int64
	checks := make(map[int64]*gtk.CheckButton, len(characters))
	byID := make(map[int64]chars.Character, len(characters))
	for _, c := range characters {
		byID[c.ID] = c
	}

	start := gtk.NewButtonWithLabel("Start the scene")
	start.AddCSSClass("suggested-action")
	start.SetSensitive(false)

	count := gtk.NewLabel("")
	count.AddCSSClass("dim-label")
	count.SetXAlign(0)

	refresh := func() {
		start.SetSensitive(len(chosen) >= 2)
		switch len(chosen) {
		case 0:
			count.SetText(fmt.Sprintf("Pick two to %d characters.", store.MaxCast))
		case 1:
			count.SetText("Pick at least one more.")
		default:
			names := make([]string, 0, len(chosen))
			for _, id := range chosen {
				names = append(names, byID[id].Name)
			}
			count.SetText(strings.Join(names, ", "))
		}
		// At the limit the unticked boxes go insensitive rather than silently
		// refusing a click, so the cap is visible before it is hit.
		full := len(chosen) >= store.MaxCast
		for id, ch := range checks {
			if !ch.Active() {
				ch.SetSensitive(!full)
			}
			_ = id
		}
	}

	rows := make([]filterRow, 0, len(characters))
	for _, c := range characters {
		c := c
		row := gtk.NewBox(gtk.OrientationHorizontal, 10)
		row.AddCSSClass("character-card")

		check := gtk.NewCheckButton()
		check.SetVAlign(gtk.AlignCenter)
		checks[c.ID] = check
		row.Append(check)

		row.Append(ui.NewCharacterAvatar(c, 32))

		col := gtk.NewBox(gtk.OrientationVertical, 1)
		col.SetHExpand(true)
		col.SetVAlign(gtk.AlignCenter)
		name := gtk.NewLabel(c.Name)
		name.SetXAlign(0)
		name.AddCSSClass("character-name")
		col.Append(name)
		if s := c.Summary(); s != "" {
			sum := gtk.NewLabel(s)
			sum.SetXAlign(0)
			sum.SetEllipsize(3) // PANGO_ELLIPSIZE_END
			sum.AddCSSClass("dim-label")
			col.Append(sum)
		}
		row.Append(col)

		check.ConnectToggled(func() {
			if check.Active() {
				if len(chosen) >= store.MaxCast {
					check.SetActive(false)
					return
				}
				chosen = append(chosen, c.ID)
			} else {
				for i, id := range chosen {
					if id == c.ID {
						chosen = append(chosen[:i], chosen[i+1:]...)
						break
					}
				}
			}
			refresh()
		})
		// The whole row toggles, not just the box: a 16px target beside a
		// 56px row is a target nobody aims at.
		click := gtk.NewGestureClick()
		click.ConnectReleased(func(int, float64, float64) {
			if check.Sensitive() || check.Active() {
				check.SetActive(!check.Active())
			}
		})
		row.AddController(click)

		rows = append(rows, filterRow{
			Widget: row,
			Text:   strings.ToLower(c.Name + " " + c.Summary() + " " + strings.Join(c.Tags, " ")),
		})
	}
	searchableList(list, "Search by name, description or tag", rows)

	footer := gtk.NewBox(gtk.OrientationHorizontal, 10)
	footer.SetMarginTop(6)
	footer.SetMarginBottom(14)
	footer.SetMarginStart(14)
	footer.SetMarginEnd(14)
	count.SetHExpand(true)
	footer.Append(count)
	footer.Append(start)

	start.ConnectClicked(func() {
		cast := make([]chars.Character, 0, len(chosen))
		for _, id := range chosen {
			cast = append(cast, byID[id])
		}
		d.Close()
		a.newGroupChat(cast)
	})
	refresh()

	tv := adw.NewToolbarView()
	tv.AddTopBar(adw.NewHeaderBar())
	tv.SetContent(scrolled(list))
	tv.AddBottomBar(footer)
	d.SetChild(tv)
	d.Present(a.win)
}

// newGroupChat clears the centre panel for a fresh scene with a cast. Nothing is
// written until the first message is sent.
func (a *App) newGroupChat(cast []chars.Character) {
	if len(cast) < 2 {
		if len(cast) == 1 {
			a.newChat(cast[0])
		}
		return
	}
	a.chat.Clear()
	a.chat.LoadScene(store.Chat{
		Model:       a.cfg.Model,
		CharacterID: cast[0].ID,
		Kind:        store.KindRoleplay,
		Title:       groupTitle(cast),
	}, cast, nil)
	a.showPortraitFor(cast[0])
	// One opening, from the first of them. Every card carries a greeting
	// written for a scene with nobody else in it, so showing all of them opens
	// the scene with five people introducing themselves to you in parallel and
	// none of them noticing each other.
	if g := chars.Greeting(cast[0], a.persona()); g != "" {
		a.chat.ShowGreeting(g)
	}
	a.showChat()
	a.sidebar.Select(0)
	a.setTitle(store.Chat{Title: groupTitle(cast)}, chars.Character{Name: groupTitle(cast)})
	a.chat.FocusComposer()
}

// groupTitle names a scene after the people in it. Two names in full, then a
// count: "Vesper, Kestrel" reads as a scene, "Vesper, Kestrel, Ash, Wren" reads
// as a list and does not fit a sidebar row either way.
func groupTitle(cast []chars.Character) string {
	names := chars.CastNames(cast)
	switch len(names) {
	case 0:
		return "New scene"
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return fmt.Sprintf("%s, %s and %d others", names[0], names[1], len(names)-2)
	}
}
