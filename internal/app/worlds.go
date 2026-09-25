package app

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
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

	if d := ui.Snippet(w.Description, 180); d != "" {
		desc := gtk.NewLabel(d)
		desc.SetXAlign(0)
		desc.SetWrap(true)
		desc.SetLines(2)
		desc.SetEllipsize(3)
		desc.AddCSSClass("character-card-desc")
		col.Append(desc)
	}
	open.SetChild(col)
	open.SetTooltipText("Open " + w.Name)

	setting := w
	open.ConnectClicked(func() {
		parent.Close()
		a.showWorld(setting)
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

// showWorld is a world's own page, and the answer to a question the app had no
// answer for: how do you actually play in one?
//
// Before this, a world was a lorebook and nothing else. Getting a scene set in
// one meant creating the world, then opening the character editor, then finding
// the world dropdown, then leaving and picking that character off the home
// screen. Every step of that is real, and none of it was visible. So the world
// now holds the thing you came for: the people in it, each one click from a
// scene, with the lorebook kept as what it always was, the world's memory.
func (a *App) showWorld(w world.World) {
	d := adw.NewDialog()
	d.SetTitle(w.Name)
	d.SetContentWidth(600)
	d.SetContentHeight(660)

	header := adw.NewHeaderBar()
	back := gtk.NewButtonFromIconName(ui.IconPanelLeft)
	back.SetTooltipText("All worlds")
	back.ConnectClicked(func() {
		d.Close()
		a.showWorlds()
	})
	header.PackStart(back)

	edit := gtk.NewButtonFromIconName(ui.IconEdit)
	edit.SetTooltipText("Edit this world")
	edit.ConnectClicked(func() {
		d.Close()
		a.editWorld(w)
	})
	header.PackEnd(edit)

	page := gtk.NewBox(gtk.OrientationVertical, 8)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)

	if desc := strings.TrimSpace(w.Description); desc != "" {
		l := gtk.NewLabel(desc)
		l.SetXAlign(0)
		l.SetWrap(true)
		l.AddCSSClass("settings-hint")
		l.SetMarginBottom(6)
		page.Append(l)
	}

	characters, err := a.store.Characters()
	if err != nil {
		a.toast("Could not read your characters: " + err.Error())
	}
	var here, elsewhere []chars.Character
	for _, c := range characters {
		if c.WorldID == w.ID {
			here = append(here, c)
		} else {
			elsewhere = append(elsewhere, c)
		}
	}

	heading := gtk.NewLabel("Play here")
	heading.SetXAlign(0)
	heading.AddCSSClass("settings-heading")
	page.Append(heading)

	if len(here) == 0 {
		hint := gtk.NewLabel("Nobody lives in " + w.Name + " yet. A world is played through " +
			"its characters: give it someone, and their scenes draw on this lorebook and add back to it.")
		hint.SetXAlign(0)
		hint.SetWrap(true)
		hint.AddCSSClass("settings-hint")
		page.Append(hint)
	} else {
		hint := gtk.NewLabel("Pick someone to start a scene. What happens in it is remembered in the lorebook below.")
		hint.SetXAlign(0)
		hint.SetWrap(true)
		hint.AddCSSClass("settings-hint")
		page.Append(hint)

		for _, c := range here {
			page.Append(a.worldCastRow(c, w, d))
		}
	}

	actions := gtk.NewBox(gtk.OrientationHorizontal, 8)
	actions.SetMarginTop(8)
	actions.SetMarginBottom(4)

	write := gtk.NewButtonWithLabel("Write someone who lives here")
	if len(here) == 0 {
		write.AddCSSClass("suggested-action")
	}
	write.SetTooltipText("Open the character editor with " + w.Name + " already set")
	write.ConnectClicked(func() {
		d.Close()
		a.editCharacter(chars.Character{WorldID: w.ID})
	})
	actions.Append(write)

	if len(elsewhere) > 0 {
		move := gtk.NewButtonWithLabel("Move someone in…")
		move.SetTooltipText("Bring a character you already have into " + w.Name)
		move.ConnectClicked(func() {
			d.Close()
			a.moveIntoWorld(w, elsewhere)
		})
		actions.Append(move)
	}
	page.Append(actions)

	// The lorebook, as one row rather than a headerbar button: it is the
	// world's memory, and reads better as part of the world than as a control
	// floating above it.
	lore := gtk.NewLabel("Lorebook")
	lore.SetXAlign(0)
	lore.AddCSSClass("settings-heading")
	lore.SetMarginTop(8)
	page.Append(lore)
	page.Append(a.lorebookRow(w, d))

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// worldCastRow is one character in a world: click to play, with a way out of
// the world beside it.
func (a *App) worldCastRow(c chars.Character, w world.World, parent *adw.Dialog) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)

	play := gtk.NewButton()
	play.AddCSSClass("character-card")
	play.SetHExpand(true)

	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	avatar := ui.NewCharacterAvatar(c, 36)
	gtk.BaseWidget(avatar).SetVAlign(gtk.AlignStart)
	box.Append(avatar)

	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.SetHExpand(true)
	name := gtk.NewLabel(c.Name)
	name.SetXAlign(0)
	name.SetEllipsize(3)
	name.AddCSSClass("character-card-name")
	col.Append(name)

	if sum := ui.Snippet(c.Summary(), 180); sum != "" {
		desc := gtk.NewLabel(sum)
		desc.SetXAlign(0)
		desc.SetWrap(true)
		desc.SetLines(2)
		desc.SetEllipsize(3)
		desc.AddCSSClass("character-card-desc")
		col.Append(desc)
	}
	box.Append(col)
	play.SetChild(box)
	play.SetTooltipText("Start a scene with " + c.Name + " in " + w.Name)

	character := c
	play.ConnectClicked(func() {
		parent.Close()
		a.newChat(character)
	})
	row.Append(play)

	side := gtk.NewBox(gtk.OrientationVertical, 4)
	side.SetVAlign(gtk.AlignCenter)
	open := gtk.NewButtonFromIconName(ui.IconEdit)
	open.SetTooltipText("Edit " + character.Name)
	open.AddCSSClass("flat")
	open.ConnectClicked(func() {
		parent.Close()
		a.editCharacter(character)
	})
	side.Append(open)
	row.Append(side)
	return row
}

// lorebookRow summarises the world's memory and opens it.
func (a *App) lorebookRow(w world.World, parent *adw.Dialog) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("character-card")

	col := gtk.NewBox(gtk.OrientationVertical, 3)
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)

	entries, _ := a.store.LoreEntries(w.ID)
	held := 0
	for _, e := range entries {
		if e.Auto && !e.Enabled {
			held++
		}
	}

	title := gtk.NewLabel(fmt.Sprintf("%d entries", len(entries)))
	title.SetXAlign(0)
	title.SetHExpand(true)
	title.AddCSSClass("character-card-name")
	head.Append(title)
	if held > 0 {
		badge := gtk.NewLabel(fmt.Sprintf("%d to review", held))
		badge.AddCSSClass("character-card-tag")
		badge.AddCSSClass("review-badge")
		head.Append(badge)
	}
	col.Append(head)

	body := gtk.NewLabel("What is true in this world. Astral writes to it as you play, and sends back whatever the scene brings up.")
	body.SetXAlign(0)
	body.SetWrap(true)
	body.AddCSSClass("character-card-desc")
	col.Append(body)

	btn.SetChild(col)
	btn.SetTooltipText("Open the lorebook for " + w.Name)
	btn.ConnectClicked(func() {
		parent.Close()
		a.showLorebook(w)
	})
	return btn
}

// moveIntoWorld brings an existing character into a world, which is the same
// thing as setting the world in their editor, reached from the side you are
// actually standing on.
func (a *App) moveIntoWorld(w world.World, candidates []chars.Character) {
	d := adw.NewDialog()
	d.SetTitle("Move into " + w.Name)
	d.SetContentWidth(520)
	d.SetContentHeight(560)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() {
		d.Close()
		a.showWorld(w)
	})
	header.PackStart(cancel)

	list := gtk.NewBox(gtk.OrientationVertical, 8)
	list.SetMarginTop(14)
	list.SetMarginBottom(14)
	list.SetMarginStart(14)
	list.SetMarginEnd(14)

	hint := gtk.NewLabel("Their scenes will start drawing on this world's lorebook. Anyone already in another world moves out of it.")
	hint.SetXAlign(0)
	hint.SetWrap(true)
	hint.AddCSSClass("settings-hint")
	list.Append(hint)

	for _, c := range candidates {
		btn := gtk.NewButton()
		btn.AddCSSClass("character-card")

		box := gtk.NewBox(gtk.OrientationHorizontal, 10)
		avatar := ui.NewCharacterAvatar(c, 32)
		gtk.BaseWidget(avatar).SetVAlign(gtk.AlignStart)
		box.Append(avatar)

		col := gtk.NewBox(gtk.OrientationVertical, 2)
		col.SetHExpand(true)
		name := gtk.NewLabel(c.Name)
		name.SetXAlign(0)
		name.SetEllipsize(3)
		name.AddCSSClass("character-card-name")
		col.Append(name)
		if sum := ui.Snippet(c.Summary(), 140); sum != "" {
			desc := gtk.NewLabel(sum)
			desc.SetXAlign(0)
			desc.SetWrap(true)
			desc.SetLines(2)
			desc.SetEllipsize(3)
			desc.AddCSSClass("character-card-desc")
			col.Append(desc)
		}
		box.Append(col)
		btn.SetChild(box)

		character := c
		btn.ConnectClicked(func() {
			character.WorldID = w.ID
			if _, err := a.store.SaveCharacter(character); err != nil {
				a.toast("Could not move " + character.Name + ": " + err.Error())
				return
			}
			d.Close()
			a.toast(character.Name + " now lives in " + w.Name + ".")
			a.showWorld(w)
		})
		list.Append(btn)
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(list))
	d.SetChild(tv)
	d.Present(a.win)
}
