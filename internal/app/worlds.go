package app

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/ui"
	"astral/internal/world"
)

// showWorlds lists the settings a character can belong to.
func (a *App) showWorlds() {
	d := adw.NewDialog()
	d.SetTitle("Worlds")
	d.SetContentWidth(560)
	d.SetContentHeight(620)

	header := adw.NewHeaderBar()
	newBtn := gtk.NewButtonFromIconName(ui.IconAdd)
	newBtn.SetTooltipText("Create a world")
	newBtn.ConnectClicked(func() {
		d.Close()
		a.editWorld(world.World{})
	})
	header.PackEnd(newBtn)

	list := gtk.NewBox(gtk.OrientationVertical, 8)
	list.SetMarginTop(14)
	list.SetMarginBottom(14)
	list.SetMarginStart(14)
	list.SetMarginEnd(14)

	worlds, err := a.store.Worlds()
	if err != nil {
		a.toast("Could not read your worlds: " + err.Error())
	}
	if len(worlds) == 0 {
		empty := gtk.NewLabel("No worlds yet.\n\nA world holds the lorebook for a setting: " +
			"the people, places and rules that stay true across every scene played in it. " +
			"Astral adds to it as you play.")
		empty.SetWrap(true)
		empty.SetJustify(gtk.JustifyCenter)
		empty.SetVExpand(true)
		empty.AddCSSClass("dim-label")
		list.Append(empty)
	}
	for _, w := range worlds {
		list.Append(a.worldRow(w, d))
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(list))
	d.SetChild(tv)
	d.Present(a.win)
}

// worldRow is one world: click to open its lorebook.
func (a *App) worldRow(w world.World, parent *adw.Dialog) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)

	open := gtk.NewButton()
	open.AddCSSClass("character-card")
	open.SetHExpand(true)

	col := gtk.NewBox(gtk.OrientationVertical, 3)
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	name := gtk.NewLabel(w.Name)
	name.SetXAlign(0)
	name.AddCSSClass("character-card-name")
	head.Append(name)

	total, _ := a.store.CountLore(w.ID)
	entries, _ := a.store.LoreEntries(w.ID)
	held := 0
	for _, e := range entries {
		if e.Auto && !e.Enabled {
			held++
		}
	}
	count := gtk.NewLabel(fmt.Sprintf("%d entries", total))
	count.AddCSSClass("character-card-tag")
	head.Append(count)
	if held > 0 {
		// Surfaced here because a held entry is waiting on a decision, and
		// something waiting on you is worth saying before you go looking.
		badge := gtk.NewLabel(fmt.Sprintf("%d to review", held))
		badge.AddCSSClass("character-card-tag")
		badge.AddCSSClass("review-badge")
		head.Append(badge)
	}
	col.Append(head)

	if d := ui.Snippet(w.Description, 110); d != "" {
		desc := gtk.NewLabel(d)
		desc.SetXAlign(0)
		desc.SetWrap(true)
		desc.SetLines(2)
		desc.SetEllipsize(3)
		desc.AddCSSClass("character-card-desc")
		col.Append(desc)
	}
	open.SetChild(col)
	open.SetTooltipText("Open the lorebook for " + w.Name)

	setting := w
	open.ConnectClicked(func() {
		parent.Close()
		a.showLorebook(setting)
	})
	row.Append(open)

	side := gtk.NewBox(gtk.OrientationVertical, 4)
	side.SetVAlign(gtk.AlignCenter)
	edit := gtk.NewButtonFromIconName(ui.IconEdit)
	edit.SetTooltipText("Rename " + setting.Name)
	edit.AddCSSClass("flat")
	edit.ConnectClicked(func() {
		parent.Close()
		a.editWorld(setting)
	})
	side.Append(edit)

	del := gtk.NewButtonFromIconName(ui.IconTrash)
	del.SetTooltipText("Delete " + setting.Name)
	del.AddCSSClass("flat")
	del.ConnectClicked(func() {
		parent.Close()
		a.confirm("Delete "+setting.Name+"?",
			"Its lorebook goes with it. Characters who lived there are kept, and simply stop having a setting.",
			"Delete", func() {
				if err := a.store.DeleteWorld(setting.ID); err != nil {
					a.toast("Could not delete: " + err.Error())
					return
				}
				a.showWorlds()
			})
	})
	side.Append(del)
	row.Append(side)
	return row
}

// editWorld is the name and description of a setting.
func (a *App) editWorld(w world.World) {
	d := adw.NewDialog()
	if w.ID == 0 {
		d.SetTitle("New world")
	} else {
		d.SetTitle("Edit " + w.Name)
	}
	d.SetContentWidth(560)

	page := gtk.NewBox(gtk.OrientationVertical, 16)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)

	outer, card := groupCard("")
	nameEntry := gtk.NewEntry()
	nameEntry.SetText(w.Name)
	nameEntry.SetPlaceholderText("The Drowned Coast")
	card.Append(labelledField("Name", "", nameEntry))

	frame, view := multilineField(w.Description, 4)
	card.Append(labelledField("Description",
		"One or two sentences about the setting. This is sent whenever any of its lore is, so keep it short.",
		frame))
	page.Append(outer)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)
	save := gtk.NewButtonWithLabel("Save")
	save.AddCSSClass("suggested-action")
	save.ConnectClicked(func() {
		name := strings.TrimSpace(nameEntry.Text())
		if name == "" {
			a.toast("A world needs a name.")
			nameEntry.GrabFocus()
			return
		}
		w.Name, w.Description = name, textOf(view)
		if _, err := a.store.SaveWorld(w); err != nil {
			a.toast("Could not save: " + err.Error())
			return
		}
		d.Close()
		a.showWorlds()
	})
	header.PackEnd(save)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
	nameEntry.GrabFocus()
}
