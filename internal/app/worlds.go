package app

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/chars"
	"astral/internal/scene"
	"astral/internal/store"
	"astral/internal/ui"
	"astral/internal/world"
)

// showWorlds shows the settings a character can belong to.
func (a *App) showWorlds() {
	worlds, err := a.store.Worlds()
	if err != nil {
		a.toast("Could not read your worlds: " + err.Error())
	}
	p := newPage("Worlds")
	p.setCaption(count(len(worlds), "world", "worlds"))
	a.refreshNavCounts()
	p.commands.Append(commandButton(ui.IconFolder, "Import", a.actionImportWorld))
	p.commands.Append(commandButton(ui.IconAdd, "New World", func() {
		a.editWorld(world.World{})
	}))

	list := gtk.NewBox(gtk.OrientationVertical, 8)
	p.body.Append(list)
	if len(worlds) == 0 {
		empty := gtk.NewLabel("No worlds yet, so create a setting your characters can share.")
		empty.SetWrap(true)
		empty.SetJustify(gtk.JustifyCenter)
		empty.AddCSSClass("dim-label")
		list.Append(empty)
	}
	rows := make([]filterRow, 0, len(worlds))
	for _, w := range worlds {
		rows = append(rows, filterRow{
			Widget: a.worldRow(w),
			Text:   strings.ToLower(w.Name + " " + w.Description),
		})
	}
	searchableList(list, "Search worlds", rows)
	list.Append(addRow("New World", func() {
		a.editWorld(world.World{})
	}))
	a.installPage(pageWorlds, p)
}

// worldRow is one world: click to open its lorebook.
func (a *App) worldRow(w world.World) *gtk.Box {
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
		desc := cardDescription(d)
		col.Append(desc)
	}
	open.SetChild(col)
	open.SetTooltipText("Open " + w.Name)

	setting := w
	open.ConnectClicked(func() {
		a.showWorld(setting)
	})
	row.Append(open)

	// Laid out as a character's row is: edit beside the card, and deleting,
	// which takes the lorebook with it, a step away in a menu.
	side := gtk.NewBox(gtk.OrientationHorizontal, 2)
	side.SetVAlign(gtk.AlignCenter)
	side.AddCSSClass("card-actions")
	row.AddCSSClass("card-row")
	edit := gtk.NewButtonFromIconName(ui.IconEdit)
	edit.SetTooltipText("Rename " + setting.Name)
	edit.AddCSSClass("flat")
	edit.ConnectClicked(func() {
		a.editWorld(setting)
	})
	side.Append(edit)

	acts := gio.NewSimpleActionGroup()
	del := gio.NewSimpleAction("delete", nil)
	del.ConnectActivate(func(*glib.Variant) {
		a.confirm("Delete "+setting.Name,
			"Its lorebook is deleted too, but its characters are kept.",
			"Delete", func() {
				if err := a.store.DeleteWorld(setting.ID); err != nil {
					a.toast("Could not delete: " + err.Error())
					return
				}
				a.refreshPage(pageWorlds)
			})
	})
	acts.AddAction(del)
	row.InsertActionGroup("world", acts)
	menu := gio.NewMenu()
	menu.Append("Details", "world.open")
	danger := gio.NewMenu()
	danger.Append("Delete", "world.delete")
	menu.AppendSection("", danger)
	open2 := gio.NewSimpleAction("open", nil)
	open2.ConnectActivate(func(*glib.Variant) { a.showWorld(setting) })
	acts.AddAction(open2)
	more := gtk.NewMenuButton()
	more.SetIconName(ui.IconMore)
	more.SetTooltipText("More for " + setting.Name)
	more.AddCSSClass("flat")
	more.SetMenuModel(menu)
	side.Append(more)
	row.Append(side)
	return row
}

// editWorld is the name and description of a setting.
func (a *App) editWorld(w world.World) {
	d := adw.NewDialog()
	ui.FreeOnClose(d)
	if w.ID == 0 {
		d.SetTitle("New World")
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

	frame, view := multilineField(w.Description, 3)
	card.Append(labelledField("Description",
		"One or two sentences on what this place is like.",
		frame))

	rulesFrame, rulesView := multilineField(w.Rules, 5)
	card.Append(labelledField("Rules",
		"Always true here and sent every turn, so keep it short.",
		rulesFrame))
	page.Append(outer)

	header := saveHeader(d, "", func() bool {
		name := strings.TrimSpace(nameEntry.Text())
		if name == "" {
			a.toast("A world needs a name.")
			nameEntry.GrabFocus()
			return false
		}
		w.Name, w.Description, w.Rules = name, textOf(view), textOf(rulesView)
		if _, err := a.store.SaveWorld(w); err != nil {
			a.toast("Could not save: " + err.Error())
			return false
		}
		a.refreshPage(pageWorlds)
		return true
	})

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolledToFit(page))
	d.SetChild(tv)
	d.Present(a.win)
	nameEntry.GrabFocus()
}

// showWorldPicker asks which world to play in, and goes straight there. One
// world and there is nothing to ask.
func (a *App) showWorldPicker() {
	worlds, err := a.store.Worlds()
	if err != nil {
		a.toast("Could not read your worlds: " + err.Error())
		return
	}
	switch len(worlds) {
	case 0:
		a.editWorld(world.World{})
		return
	case 1:
		a.startWorldScene(worlds[0])
		return
	}

	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle("Play in a World")
	d.SetContentWidth(520)

	list := gtk.NewBox(gtk.OrientationVertical, 6)
	list.SetMarginTop(14)
	list.SetMarginBottom(14)
	list.SetMarginStart(14)
	list.SetMarginEnd(14)
	for _, w := range worlds {
		btn := gtk.NewButton()
		btn.AddCSSClass("launch-row")
		col := gtk.NewBox(gtk.OrientationVertical, 1)
		title := gtk.NewLabel(w.Name)
		title.SetXAlign(0)
		title.AddCSSClass("launch-row-title")
		col.Append(title)
		if desc := strings.TrimSpace(w.Description); desc != "" {
			note := gtk.NewLabel(ui.Snippet(desc, 90))
			note.SetXAlign(0)
			note.SetEllipsize(pango.EllipsizeEnd)
			note.AddCSSClass("launch-row-note")
			col.Append(note)
		}
		btn.SetChild(col)
		btn.ConnectClicked(func() {
			d.Close()
			a.startWorldScene(w)
		})
		list.Append(btn)
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(adw.NewHeaderBar())
	tv.SetContent(scrolledToFit(list))
	d.SetChild(tv)
	d.Present(a.win)
}

// narratorFor turns a world into something a scene can be played against.
//
// A world is a place. You can be somewhere without anyone in particular being
// there, and until now Astral could not express that: a scene needed a
// character, so a world you had just written was unusable until you also
// invented someone to meet in it. This is the missing half. The model plays the
// place rather than a person, and whoever the scene turns out to need.
//
// It is never saved. There is no row for it and it never appears in a cast,
// because it is not a character anyone wrote: it is rebuilt from the world each
// time, so editing the world changes the scenes already running in it.
func narratorFor(w world.World) chars.Character {
	ca := scene.Narrator(w)
	ca.Accent = ui.AccentFor(w.Name)
	return ca
}

// startWorldScene opens a scene set in a world, with no character required.
func (a *App) startWorldScene(w world.World) {
	a.chat.Clear()
	ca := narratorFor(w)
	a.chat.LoadChat(store.Chat{
		Model:   a.cfg.Model,
		WorldID: w.ID,
		Kind:    store.KindRoleplay,
		Title:   w.Name,
	}, ca, nil)
	a.showPortraitFor(ca)
	a.showChat()
	a.sidebar.Select(0)
	a.setTitle(store.Chat{Title: w.Name}, ca)
	a.chat.FocusComposer()
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
	ui.FreeOnClose(d)
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

	// The designer, pointed at a world that already exists. The form next door
	// edits the words; this argues about them, which is what you want when the
	// rules have stopped describing the place it turned into.
	revise := gtk.NewButtonFromIconName(ui.IconDesigner)
	revise.SetTooltipText("Talk this world through with the designer")
	revise.ConnectClicked(func() {
		d.Close()
		a.reviseWorld(w)
	})
	header.PackEnd(revise)

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

	heading := gtk.NewLabel("Play Here")
	heading.SetXAlign(0)
	heading.AddCSSClass("settings-heading")
	page.Append(heading)

	// Straight in, with nobody in particular. A world is a place, and needing
	// to invent a character before you could visit one was the app asking you
	// to do its paperwork.
	enter := gtk.NewButton()
	enter.AddCSSClass("launch-row")
	enter.AddCSSClass("launch-row-primary")
	enterRow := gtk.NewBox(gtk.OrientationHorizontal, 12)
	enterIcon := gtk.NewImageFromIconName(ui.IconWorlds)
	enterIcon.SetPixelSize(18)
	enterIcon.SetVAlign(gtk.AlignCenter)
	enterRow.Append(enterIcon)
	enterCol := gtk.NewBox(gtk.OrientationVertical, 1)
	enterCol.SetHExpand(true)
	enterTitle := gtk.NewLabel("Start a Scene Here")
	enterTitle.SetXAlign(0)
	enterTitle.AddCSSClass("launch-row-title")
	enterCol.Append(enterTitle)
	enterNote := gtk.NewLabel("The model plays the place and whoever you meet.")
	enterNote.SetXAlign(0)
	enterNote.SetWrap(true)
	enterNote.AddCSSClass("launch-row-note")
	enterCol.Append(enterNote)
	enterRow.Append(enterCol)
	enter.SetChild(enterRow)
	enter.ConnectClicked(func() {
		d.Close()
		a.startWorldScene(w)
	})
	page.Append(enter)

	if len(here) > 0 {
		hint := gtk.NewLabel("Or with someone who lives here.")
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

	write := gtk.NewButtonWithLabel("Write Someone Who Lives Here")
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
		move := gtk.NewButtonWithLabel("Move Someone In…")
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

	// A lorebook could only be filled by hand or by play, which is no help to
	// anybody whose setting is already written down somewhere.
	fromText := gtk.NewButtonWithLabel("Read Lore from Text…")
	fromText.SetHAlign(gtk.AlignStart)
	fromText.SetTooltipText("Have the model write entries from your notes")
	fromText.ConnectClicked(func() {
		d.Close()
		a.showLoreFromText(w, func() { a.showWorld(w) })
	})
	page.Append(fromText)

	keep := gtk.NewLabel("This World as a File")
	keep.SetXAlign(0)
	keep.AddCSSClass("settings-heading")
	keep.SetMarginTop(8)
	page.Append(keep)

	export := gtk.NewButtonWithLabel("Export This World…")
	export.SetHAlign(gtk.AlignStart)
	export.SetTooltipText("Write this world and its whole lorebook to one file")
	export.ConnectClicked(func() { a.exportWorld(w) })
	page.Append(export)

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
	name.SetEllipsize(pango.EllipsizeEnd)
	name.AddCSSClass("character-card-name")
	col.Append(name)

	if sum := ui.Snippet(c.SummaryFor(a.cfg.PersonaName), 180); sum != "" {
		desc := cardDescription(sum)
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

	// Not a delete: from inside a world, the useful action is to take someone
	// out of it, and deleting the character outright from here would be a very
	// different thing wearing the same icon.
	out := gtk.NewButtonFromIconName(ui.IconTrash)
	out.SetTooltipText("Move " + character.Name + " out of " + w.Name)
	out.AddCSSClass("flat")
	out.ConnectClicked(func() {
		parent.Close()
		a.confirm("Move "+character.Name+" Out of "+w.Name,
			"Their scenes stop drawing on this world's lorebook.",
			"Move Out", func() {
				character.WorldID = 0
				if _, err := a.store.SaveCharacter(character); err != nil {
					a.toast("Could not move them out: " + err.Error())
					return
				}
				a.toast(character.Name + " no longer lives in " + w.Name + ".")
				a.showWorld(w)
			})
	})
	side.Append(out)
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

	body := gtk.NewLabel("What is true here, kept up to date as you play.")
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
	ui.FreeOnClose(d)
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

	hint := gtk.NewLabel("They leave any other world and use this one's lorebook.")
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
		name.SetEllipsize(pango.EllipsizeEnd)
		name.AddCSSClass("character-card-name")
		col.Append(name)
		if sum := ui.Snippet(c.SummaryFor(a.cfg.PersonaName), 140); sum != "" {
			desc := cardDescription(sum)
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
