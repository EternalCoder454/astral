package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/store"
	"astral/internal/ui"
)

// showCharacters opens the cast: everyone you can play a scene with.
func (a *App) showCharacters() {
	d := adw.NewDialog()
	d.SetTitle("Characters")
	d.SetContentWidth(560)
	d.SetContentHeight(620)

	header := adw.NewHeaderBar()

	importBtn := gtk.NewButtonFromIconName(ui.IconFolder)
	importBtn.SetTooltipText("Import a character card (.png or .json)")
	importBtn.ConnectClicked(func() {
		d.Close()
		a.actionImportCharacter()
	})
	header.PackStart(importBtn)

	newBtn := gtk.NewButtonFromIconName(ui.IconAdd)
	newBtn.SetTooltipText("Create a character")
	newBtn.ConnectClicked(func() {
		d.Close()
		a.editCharacter(chars.Character{})
	})
	header.PackEnd(newBtn)

	list := gtk.NewBox(gtk.OrientationVertical, 8)
	list.SetMarginTop(14)
	list.SetMarginBottom(14)
	list.SetMarginStart(14)
	list.SetMarginEnd(14)

	characters, err := a.store.Characters()
	if err != nil {
		a.toast("Could not read your characters: " + err.Error())
	}
	if len(characters) == 0 {
		empty := gtk.NewLabel("No characters yet.\n\nImport a card with the folder button, or create one with +.")
		empty.SetWrap(true)
		empty.SetJustify(gtk.JustifyCenter)
		empty.SetVExpand(true)
		empty.AddCSSClass("dim-label")
		list.Append(empty)
	}
	for _, c := range characters {
		list.Append(a.castRow(c, d))
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(list))
	d.SetChild(tv)
	d.Present(a.win)
}

// castRow is one character in the list: click to play, with edit and delete
// alongside.
func (a *App) castRow(c chars.Character, parent *adw.Dialog) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)

	play := gtk.NewButton()
	play.AddCSSClass("character-card")
	play.SetHExpand(true)

	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	avatar := ui.NewAvatar(c.Initial(), c.Accent, 38)
	avatar.SetVAlign(gtk.AlignStart)
	box.Append(avatar)

	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.SetHExpand(true)
	name := gtk.NewLabel(c.Name)
	name.SetXAlign(0)
	name.SetEllipsize(3)
	name.AddCSSClass("character-card-name")
	col.Append(name)

	desc := gtk.NewLabel(ui.Snippet(c.Summary(), 110))
	desc.SetXAlign(0)
	desc.SetWrap(true)
	desc.SetLines(2)
	desc.SetEllipsize(3)
	desc.AddCSSClass("character-card-desc")
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
		parent.Close()
		a.newChat(character)
	})
	row.Append(play)

	side := gtk.NewBox(gtk.OrientationVertical, 4)
	side.SetVAlign(gtk.AlignCenter)
	edit := gtk.NewButtonFromIconName(ui.IconEdit)
	edit.SetTooltipText("Edit " + c.Name)
	edit.AddCSSClass("flat")
	edit.ConnectClicked(func() {
		parent.Close()
		a.editCharacter(character)
	})
	side.Append(edit)

	del := gtk.NewButtonFromIconName(ui.IconTrash)
	del.SetTooltipText("Delete " + c.Name)
	del.AddCSSClass("flat")
	del.ConnectClicked(func() {
		parent.Close()
		a.confirm("Delete "+character.Name+"?",
			"The character will be removed. Chats you have already played with them are kept.",
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
	description  *gtk.TextView
	personality  *gtk.TextView
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
// saved character to onSaved — which is how the designer offers to play the
// scene it just built.
func (a *App) editCharacterWith(c chars.Character, onSaved func(chars.Character)) {
	d := adw.NewDialog()
	if c.ID == 0 {
		d.SetTitle("New Character")
	} else {
		d.SetTitle("Edit " + c.Name)
	}
	d.SetContentWidth(620)
	d.SetContentHeight(700)

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

	descFrame, descView := multilineField(c.Description, 5)
	f.description = descView
	idCard.Append(labelledField("Description",
		"Who they are, how they look, how they speak. Sent to the model on every single turn, so keep it tight.\n\n"+
			"{{user}} becomes your name and {{char}} becomes theirs, here and in every other field.",
		descFrame))

	persFrame, persView := multilineField(c.Personality, 2)
	f.personality = persView
	idCard.Append(labelledField("Personality",
		"A few traits, usually comma-separated, wry, guarded, quick to anger.",
		persFrame))

	// Two images, because the crops want different things: the avatar is a
	// face at 28px beside every message, the portrait is the whole figure
	// beside the scene.
	idCard.Append(a.imageField("Avatar", "Shown beside every message and in lists. A face works best.",
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
	hint := "The setting they live in. Its lorebook is sent when the conversation touches it, and anything learned while playing them is written there."
	if len(worlds) == 0 {
		hint = "No worlds yet. Create one under Worlds (Ctrl+W) to give this character a setting with a lorebook."
	}
	idCard.Append(labelledField("World", hint, worldDrop))

	f.tags = gtk.NewEntry()
	f.tags.SetText(strings.Join(c.Tags, ", "))
	f.tags.SetPlaceholderText("fantasy, detective, slow-burn")
	idCard.Append(labelledField("Tags", "Comma-separated. Only used for finding them again.", f.tags))
	page.Append(idOuter)

	// The scene.
	sceneOuter, sceneCard := groupCard("The scene")
	scenFrame, scenView := multilineField(c.Scenario, 3)
	f.scenario = scenView
	sceneCard.Append(labelledField("Scenario", "Where this starts, and what is going on when it does.", scenFrame))

	firstFrame, firstView := multilineField(c.FirstMes, 5)
	f.firstMes = firstView
	sceneCard.Append(labelledField("Opening message",
		"Their first words. The model copies its length, tense and formatting for the rest of the scene, so write this one the way you want the whole thing to read.",
		firstFrame))

	exFrame, exView := multilineField(c.MesExample, 4)
	f.mesExample = exView
	sceneCard.Append(labelledField("Example dialogue",
		"Optional. Use <START> between exchanges, and prefix lines with {{user}}: and {{char}}:, Astral turns these into real example turns.",
		exFrame))
	page.Append(sceneOuter)

	// Instructions.
	insOuter, insCard := groupCard("Instructions")
	insFrame, insView := multilineField(c.Instructions, 5)
	f.instructions = insView
	insCard.Append(labelledField("How this character should be played",
		"Your own rules for them, \"keep replies to one paragraph\", \"never break the fourth wall\", \"{{char}} always lies about her past\".\n\n"+
			"These are added to Astral's roleplay framing rather than replacing it, and they are repeated at the end of the context on every turn, which is the position a model actually obeys. One instruction per line works best.",
		insFrame))
	page.Append(insOuter)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)

	// Import without export is a one-way door. This writes the character back
	// out as a V2 card, which is what everything else in the ecosystem reads.
	if c.ID != 0 {
		export := gtk.NewButtonWithLabel("Export…")
		export.SetTooltipText("Save as a character card other apps can read")
		export.ConnectClicked(func() { a.exportCharacter(c) })
		header.PackStart(export)
	}

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
	c.Description = textOf(f.description)
	c.Personality = textOf(f.personality)
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
	dialog.SetInitialName(safeFileName(c.Name) + ".json")
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
	dialog.SetTitle("Import a character card")

	cards := gtk.NewFileFilter()
	cards.SetName("Character cards")
	cards.AddPattern("*.png")
	cards.AddPattern("*.json")
	all := gtk.NewFileFilter()
	all.SetName("All files")
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
	a.toast(fmt.Sprintf("Imported %s. Opening a scene…", c.Name))
	a.newChat(c)
}

// saveAvatar copies an imported card's image into the data directory.
func saveAvatar(name string, data []byte) (string, error) {
	if err := os.MkdirAll(store.AvatarDir(), 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(store.AvatarDir(), safeFileName(name)+".png")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// safeFileName reduces a character's name to something safe to write to disk.
// A card is a downloaded file and its name is attacker-controlled, so this
// keeps only characters that cannot traverse or escape a directory, rather
// than trying to escape the ones that can.
func safeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "character"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
