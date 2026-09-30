package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/chars"
	"astral/internal/store"
	"astral/internal/ui"
)

// showCharacters shows the cast: everyone you can play a scene with.
func (a *App) showCharacters() {
	characters, err := a.store.Characters()
	if err != nil {
		a.toast("Could not read your characters: " + err.Error())
	}
	p := newPage("Characters")
	p.setCaption(count(len(characters), "character", "characters"))
	a.refreshNavCounts()

	// A file, or a link: a Chub character page or a card's own address.
	importMenu := gtk.NewBox(gtk.OrientationVertical, 2)
	importPop := gtk.NewPopover()
	for _, it := range []struct {
		label string
		fire  func()
	}{
		{"Import a File…", a.actionImportCharacter},
		{"Import from a Link…", a.importFromLink},
	} {
		fire := it.fire
		b := gtk.NewButtonWithLabel(it.label)
		b.AddCSSClass("flat")
		gtk.BaseWidget(b.Child()).SetHAlign(gtk.AlignStart)
		b.ConnectClicked(func() {
			importPop.Popdown()
			fire()
		})
		importMenu.Append(b)
	}
	importPop.SetChild(importMenu)
	p.commands.Append(commandMenu(ui.IconFolder, "Import", importPop))
	p.commands.Append(commandButton(ui.IconAdd, "New Character", func() {
		a.editCharacter(chars.Character{})
	}))

	list := gtk.NewBox(gtk.OrientationVertical, 8)
	p.body.Append(list)
	if len(characters) == 0 {
		empty := gtk.NewLabel("No characters yet, so import a card or create one.")
		empty.SetWrap(true)
		empty.SetJustify(gtk.JustifyCenter)
		empty.SetVExpand(true)
		empty.AddCSSClass("dim-label")
		list.Append(empty)
	}
	rows := make([]filterRow, 0, len(characters))
	for _, c := range characters {
		rows = append(rows, filterRow{
			Widget: a.castRow(c),
			Text:   strings.ToLower(c.Name + " " + c.SummaryFor(a.cfg.PersonaName) + " " + strings.Join(c.Tags, " ")),
		})
	}
	searchableList(list, "Search by name, description or tag", rows)
	list.Append(addRow("New Character", func() {
		a.editCharacter(chars.Character{})
	}))
	a.installPage(pageCharacters, p)
}

// castRow is one character in the list: click to play, with edit and delete
// alongside.
func (a *App) castRow(c chars.Character) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)

	play := gtk.NewButton()
	play.AddCSSClass("character-card")
	play.SetHExpand(true)

	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	avatar := ui.NewCharacterAvatar(c, 38)
	gtk.BaseWidget(avatar).SetVAlign(gtk.AlignStart)
	box.Append(avatar)

	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.SetHExpand(true)
	head := gtk.NewBox(gtk.OrientationHorizontal, 6)
	name := gtk.NewLabel(c.Name)
	name.SetXAlign(0)
	name.SetHExpand(true)
	name.SetEllipsize(pango.EllipsizeEnd)
	name.AddCSSClass("character-card-name")
	head.Append(name)
	// Where their scenes are set, said on the screen where you pick one.
	if c.WorldID != 0 {
		if w, err := a.store.World(c.WorldID); err == nil {
			tag := gtk.NewLabel(w.Name)
			tag.SetEllipsize(pango.EllipsizeEnd)
			tag.SetMaxWidthChars(24)
			tag.SetTooltipText("Scenes with " + c.Name + " are set in " + w.Name)
			tag.AddCSSClass("character-card-tag")
			head.Append(tag)
		}
	}
	col.Append(head)

	desc := cardDescription(ui.Snippet(c.SummaryFor(a.cfg.PersonaName), 240))
	col.Append(desc)

	if len(c.Tags) > 0 {
		tags := gtk.NewBox(gtk.OrientationHorizontal, 4)
		for i, t := range c.Tags {
			if i == 4 {
				break // a row of tags is a hint, not an index
			}
			l := gtk.NewLabel(t)
			l.AddCSSClass("character-card-tag")
			tags.Append(l)
		}
		col.Append(tags)
	}
	box.Append(col)
	play.SetChild(box)
	play.SetTooltipText("Start a scene with " + c.Name)

	character := c
	play.ConnectClicked(func() {
		a.newChat(character)
	})
	row.Append(play)

	// Across, not down. Stacked, the four of them set the height of every
	// row, so a card holding two lines of description was built to the
	// height of six and most of it was empty. In a row they sit beside the
	// card at its own height, dimmed until the row is pointed at or tabbed
	// into, which is when they are wanted.
	side := gtk.NewBox(gtk.OrientationHorizontal, 2)
	side.SetVAlign(gtk.AlignCenter)
	side.AddCSSClass("card-actions")
	row.AddCSSClass("card-row")

	// Favorites come first in every list. The star stays lit when the rest
	// of the row's buttons are dimmed, so which ones they are shows.
	star := gtk.NewButtonFromIconName(ui.IconStarOff)
	star.AddCSSClass("flat")
	star.AddCSSClass("favorite-star")
	star.SetVAlign(gtk.AlignCenter)
	setStar := func(on bool) {
		if on {
			star.SetIconName(ui.IconStar)
			star.AddCSSClass("favorite-on")
			star.SetTooltipText("Remove " + c.Name + " from your favorites")
		} else {
			star.SetIconName(ui.IconStarOff)
			star.RemoveCSSClass("favorite-on")
			star.SetTooltipText("Add " + c.Name + " to your favorites")
		}
	}
	setStar(c.Favorite)
	fav := c.Favorite
	star.ConnectClicked(func() {
		if err := a.store.SetFavorite(c.ID, !fav); err != nil {
			a.toast("Could not change that: " + err.Error())
			return
		}
		fav = !fav
		setStar(fav)
		a.refreshWelcome()
	})
	// Beside the other buttons rather than among them: they are dimmed
	// together, and a lit star inside them would be dimmed too.
	row.Append(star)

	edit := gtk.NewButtonFromIconName(ui.IconEdit)
	edit.SetTooltipText("Edit " + c.Name)
	edit.AddCSSClass("flat")
	edit.ConnectClicked(func() {
		a.editCharacter(character)
	})
	side.Append(edit)

	info := gtk.NewButtonFromIconName(ui.IconInfo)
	info.SetTooltipText("Everything about " + c.Name + ", and their scenes")
	info.AddCSSClass("flat")
	info.ConnectClicked(func() {
		a.showCharacter(character)
	})
	side.Append(info)

	// The designer, pointed at somebody who already exists. The form next door
	// edits the words; this argues about them, which is what you want when the
	// problem is that the description is all adjectives.
	revise := gtk.NewButtonFromIconName(ui.IconDesigner)
	revise.SetTooltipText("Revise " + c.Name + " with the designer")
	revise.AddCSSClass("flat")
	revise.ConnectClicked(func() {
		a.reviseCharacter(character)
	})
	side.Append(revise)

	del := gtk.NewButtonFromIconName(ui.IconTrash)
	del.SetTooltipText("Delete " + c.Name)
	del.AddCSSClass("flat")
	del.ConnectClicked(func() {
		a.confirm("Delete "+character.Name,
			"The character is removed, but their chats are kept.",
			"Delete", func() {
				if err := a.store.DeleteCharacter(character.ID); err != nil {
					a.toast("Could not delete: " + err.Error())
					return
				}
				a.refreshWelcome()
				a.showCharacters()
			})
	})
	side.Append(del)
	row.Append(side)

	return row
}

// characterForm holds the editor's widgets so Save can read them back.
type characterForm struct {
	name         *gtk.Entry
	age          *gtk.Entry
	gender       *gtk.Entry
	race         *gtk.Entry
	occupation   *gtk.Entry
	relationship *gtk.Entry
	description  *gtk.TextView
	personality  *gtk.TextView
	appearance   *gtk.TextView
	speech       *gtk.TextView
	scenario     *gtk.TextView
	firstMes     *gtk.TextView
	mesExample   *gtk.TextView
	instructions *gtk.TextView
	tags         *gtk.Entry
}

// editCharacter opens the character editor.
//
// The fields are the character-card spec's, named and explained in plain
// language. Keeping the spec's shape means anything written here exports back
// out as a card that other tools can read.
func (a *App) editCharacter(c chars.Character) { a.editCharacterWith(c, nil) }

// editCharacterWith opens the editor and, on a successful save, hands the
// saved character to onSaved, which is how the designer offers to play the
// scene it just built.
func (a *App) editCharacterWith(c chars.Character, onSaved func(chars.Character)) {
	d := adw.NewDialog()
	ui.FreeOnClose(d)
	if c.ID == 0 {
		d.SetTitle("New Character")
	} else {
		d.SetTitle("Edit " + c.Name)
	}
	d.SetContentWidth(640)
	d.SetContentHeight(780)

	f := &characterForm{}
	page := gtk.NewBox(gtk.OrientationVertical, 16)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)

	// Identity.
	idOuter, idCard := groupCard("Identity")
	f.name = gtk.NewEntry()
	f.name.SetText(c.Name)
	f.name.SetPlaceholderText("Who is this?")
	idCard.Append(labelledField("Name", "", f.name))

	// The facts a scene reaches for first, in fields of their own, as a
	// persona has them; see chars.Character.
	entry := func(text, placeholder string) *gtk.Entry {
		e := gtk.NewEntry()
		e.SetText(text)
		e.SetPlaceholderText(placeholder)
		return e
	}
	f.age = entry(c.Age, "34, or early forties")
	f.gender = entry(c.Gender, "Woman, man…")
	f.race = entry(c.Race, "Human, elf…")
	facts := gtk.NewBox(gtk.OrientationHorizontal, 10)
	facts.SetHomogeneous(true)
	facts.Append(labelledField("Age", "", f.age))
	facts.Append(labelledField("Gender", "", f.gender))
	facts.Append(labelledField("Race", "", f.race))
	idCard.Append(facts)
	f.occupation = entry(c.Occupation, "What they do")
	idCard.Append(labelledField("Occupation", "", f.occupation))
	f.relationship = entry(c.Relationship, "An old friend, a rival, a stranger")
	idCard.Append(labelledField("Relationship to You", "", f.relationship))

	descFrame, descView := multilineField(c.Description, 3)
	f.description = descView
	idCard.Append(labelledField("Description",
		"Who they are and what they want, as behaviour rather than adjectives.",
		descFrame))

	persFrame, persView := multilineField(c.Personality, 2)
	f.personality = persView
	idCard.Append(labelledField("Personality",
		"A few comma-separated traits, like wry, guarded, quick to anger.",
		persFrame))

	appFrame, appView := multilineField(c.Appearance, 2)
	f.appearance = appView
	idCard.Append(labelledField("Appearance",
		"Face, build, what they wear, how they hold themselves.",
		appFrame))

	speechFrame, speechView := multilineField(c.Speech, 2)
	f.speech = speechView
	idCard.Append(labelledField("How They Talk",
		"Sentence length, habits, and what they never say out loud.",
		speechFrame))

	// Two images, because the crops want different things: the avatar is a
	// face at 28px beside every message, the portrait is the whole figure
	// beside the scene.
	idCard.Append(a.imageField("Avatar", "A face, shown beside every message and in lists.",
		c.Name+"-avatar",
		func() string { return c.AvatarPath },
		func(p string) { c.AvatarPath = p }))
	idCard.Append(a.imageField("Portrait", "The larger image shown beside the scene while you play.",
		c.Name+"-portrait",
		func() string { return c.PortraitPath },
		func(p string) { c.PortraitPath = p }))

	// Which setting this character belongs to. A scene inherits the lorebook
	// from here, and it is where anything learned while playing them goes.
	worlds, _ := a.store.Worlds()
	worldNames := []string{"None"}
	worldIDs := []int64{0}
	selected := uint(0)
	for i, w := range worlds {
		worldNames = append(worldNames, w.Name)
		worldIDs = append(worldIDs, w.ID)
		if w.ID == c.WorldID {
			selected = uint(i + 1)
		}
	}
	worldDrop := gtk.NewDropDownFromStrings(worldNames)
	worldDrop.SetSelected(selected)
	worldDrop.NotifyProperty("selected", func() {
		if i := int(worldDrop.Selected()); i >= 0 && i < len(worldIDs) {
			c.WorldID = worldIDs[i]
		}
	})
	hint := "The setting they live in, whose lorebook the scene uses."
	if len(worlds) == 0 {
		hint = "No worlds yet: create one under Worlds (Ctrl+W)."
	}
	idCard.Append(labelledField("World", hint, worldDrop))

	f.tags = gtk.NewEntry()
	f.tags.SetText(strings.Join(c.Tags, ", "))
	f.tags.SetPlaceholderText("fantasy, detective, slow-burn")
	idCard.Append(labelledField("Tags", "Comma-separated, and only used for finding them again.", f.tags))
	page.Append(idOuter)

	// The scene.
	sceneOuter, sceneCard := groupCard("Opening")
	scenFrame, scenView := multilineField(c.Scenario, 3)
	f.scenario = scenView
	sceneCard.Append(labelledField("Scenario", "Where this starts, and what is going on when it does.", scenFrame))

	firstFrame, firstView := multilineField(c.FirstMes, 3)
	f.firstMes = firstView
	sceneCard.Append(labelledField("Opening Message",
		"Their first words, whose length and tone the model copies.",
		firstFrame))

	exFrame, exView := multilineField(c.MesExample, 3)
	f.mesExample = exView
	sceneCard.Append(labelledField("Example Dialogue",
		"Optional sample lines starting {{user}}: or {{char}}:, split by <START>.",
		exFrame))
	page.Append(sceneOuter)

	// Instructions.
	insOuter, insCard := groupCard("Instructions")
	insFrame, insView := multilineField(c.Instructions, 3)
	f.instructions = insView
	if c.ID != 0 {
		revise := gtk.NewButtonWithLabel("Revise with the Designer…")
		revise.SetHAlign(gtk.AlignStart)
		revise.SetTooltipText("Talk this character through with the designer instead of typing")
		revise.ConnectClicked(func() {
			d.Close()
			a.reviseCharacter(c)
		})
		insCard.Append(revise)
	}
	insCard.Append(labelledField("Rules for This Character",
		"Your own rules for them, one per line.",
		insFrame))
	page.Append(insOuter)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)

	save := gtk.NewButtonWithLabel("Save")
	save.AddCSSClass("suggested-action")
	save.ConnectClicked(func() {
		saved, ok := a.saveCharacterForm(c, f)
		if !ok {
			return
		}
		d.Close()
		if onSaved != nil {
			onSaved(saved)
		}
	})
	header.PackEnd(save)

	// Import without export is a one-way door. This writes the character back
	// out as a V2 card, which is what everything else in the ecosystem reads.
	//
	// It sits in an overflow menu rather than beside Cancel. Cancel and Save
	// are what a dialog's header promises; a third bare button wedged in with
	// them reads as neither, and Export is not part of that decision.
	if c.ID != 0 {
		menu := gtk.NewMenuButton()
		menu.SetIconName(ui.IconMenu)
		menu.SetTooltipText("More actions")
		menu.AddCSSClass("flat")

		items := gtk.NewBox(gtk.OrientationVertical, 0)
		items.AddCSSClass("menu-popover")
		export := gtk.NewButtonWithLabel("Export as a Character Card…")
		export.AddCSSClass("flat")
		export.SetTooltipText("Save a file other roleplay apps can read")
		gtk.BaseWidget(export.Child()).SetHAlign(gtk.AlignStart)
		pop := gtk.NewPopover()
		export.ConnectClicked(func() {
			pop.Popdown()
			a.exportCharacter(c)
		})
		items.Append(export)
		pop.SetChild(items)
		menu.SetPopover(pop)
		header.PackEnd(menu)
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
	f.name.GrabFocus()
}

// saveCharacterForm writes the editor back to the database. It returns the
// saved character and whether it succeeded, so the dialog knows to stay open
// on a validation failure.
func (a *App) saveCharacterForm(c chars.Character, f *characterForm) (chars.Character, bool) {
	name := strings.TrimSpace(f.name.Text())
	if name == "" {
		a.toast("A character needs a name.")
		f.name.GrabFocus()
		return c, false
	}
	c.Name = name
	c.Age = strings.TrimSpace(f.age.Text())
	c.Gender = strings.TrimSpace(f.gender.Text())
	c.Race = strings.TrimSpace(f.race.Text())
	c.Occupation = strings.TrimSpace(f.occupation.Text())
	c.Relationship = strings.TrimSpace(f.relationship.Text())
	c.Description = textOf(f.description)
	c.Personality = textOf(f.personality)
	c.Appearance = textOf(f.appearance)
	c.Speech = textOf(f.speech)
	c.Scenario = textOf(f.scenario)
	c.FirstMes = textOf(f.firstMes)
	c.MesExample = textOf(f.mesExample)
	c.Instructions = textOf(f.instructions)
	c.Tags = splitTags(f.tags.Text())
	if c.ID == 0 {
		c.Accent = ui.AccentFor(name)
	}

	id, err := a.store.SaveCharacter(c)
	if err != nil {
		a.toast("Could not save: " + err.Error())
		return c, false
	}
	c.ID = id
	a.refreshWelcome()
	a.refreshPage(pageCharacters)
	a.toast(name + " saved.")
	return c, true
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// exportCharacter writes a character out as a V2 card.
func (a *App) exportCharacter(c chars.Character) {
	data, err := chars.ExportCard(c)
	if err != nil {
		a.toast("Could not build the card: " + err.Error())
		return
	}

	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Export " + c.Name)
	dialog.SetInitialName(store.SafeFileName(c.Name) + ".json")
	dialog.Save(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := dialog.SaveFinish(res)
		if err != nil || file == nil {
			return // cancelled, which is not worth a message
		}
		if err := os.WriteFile(file.Path(), data, 0o644); err != nil {
			a.toast("Could not write the card: " + err.Error())
			return
		}
		a.toast("Exported " + c.Name + ".")
	})
}

// actionImportCharacter opens a file chooser for a character card.
func (a *App) actionImportCharacter() {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Import a Character Card")

	cards := gtk.NewFileFilter()
	cards.SetName("Character Cards")
	cards.AddPattern("*.png")
	cards.AddPattern("*.json")
	all := gtk.NewFileFilter()
	all.SetName("All Files")
	all.AddPattern("*")

	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(cards.Object)
	filters.Append(all.Object)
	dialog.SetFilters(filters)
	dialog.SetDefaultFilter(cards)

	dialog.Open(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := dialog.OpenFinish(res)
		if err != nil || file == nil {
			return // cancelled, which is not worth a message
		}
		a.importCard(file.Path())
	})
}

// importFromLink imports a character from a Chub page or a card's address.
func (a *App) importFromLink() {
	d := adw.NewAlertDialog("Import from a Link", "A Chub character page, or a link to a card's .png or .json.")
	ui.FreeOnClose(&d.Dialog)
	entry := gtk.NewEntry()
	entry.SetPlaceholderText("https://chub.ai/characters/…")
	entry.SetHExpand(true)
	entry.SetActivatesDefault(true)
	d.SetExtraChild(entry)
	d.AddResponse("cancel", "Cancel")
	d.AddResponse("ok", "Import")
	d.SetResponseAppearance("ok", adw.ResponseSuggested)
	d.SetDefaultResponse("ok")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response != "ok" {
			return
		}
		link := entry.Text()
		a.toast("Downloading the card…")
		go func() {
			c, avatar, err := chars.FetchCard(context.Background(), link)
			coreglib.IdleAdd(func() {
				if err != nil {
					a.toast("Could not import it: " + err.Error())
					return
				}
				a.saveImported(c, avatar)
			})
		}()
	})
	d.Present(a.win)
	entry.GrabFocus()
}

// importCard reads a card from disk and saves it as a character.
func (a *App) importCard(path string) {
	if path == "" {
		return
	}
	c, avatar, err := chars.ImportFile(path)
	if err != nil {
		a.toast("Could not import that file: " + err.Error())
		return
	}
	a.saveImported(c, avatar)
}

// saveImported stores a character read from a card, with its picture, and
// opens a scene with them.
func (a *App) saveImported(c chars.Character, avatar []byte) {
	c.Accent = ui.AccentFor(c.Name)
	if len(avatar) > 0 {
		if p, err := saveAvatar(c.Name, avatar); err == nil {
			c.AvatarPath = p
		}
	}
	id, err := a.store.SaveCharacter(c)
	if err != nil {
		a.toast("Could not save that character: " + err.Error())
		return
	}
	c.ID = id
	a.refreshWelcome()
	a.toast(fmt.Sprintf("Imported %s, opening a scene…", c.Name))
	a.newChat(c)
}

// saveAvatar copies an imported card's image into the data directory.
func saveAvatar(name string, data []byte) (string, error) { return store.SaveAvatar(name, data) }
