package app

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/ui"
	"astral/internal/world"
)

// showLorebook lists what is true in a world.
//
// Entries the model wrote but was unsure of are shown first and marked. They
// are stored switched off, so nothing unverified reaches a scene until someone
// has looked at it: an entry is permanent and is injected into every later
// scene that mentions its subject, which is a much longer reach than a single
// wrong reply has.
func (a *App) showLorebook(w world.World) {
	d := adw.NewDialog()
	d.SetTitle(w.Name)
	d.SetContentWidth(620)
	d.SetContentHeight(680)

	header := adw.NewHeaderBar()
	back := gtk.NewButtonFromIconName(ui.IconPanelLeft)
	back.SetTooltipText("All worlds")
	back.ConnectClicked(func() {
		d.Close()
		a.showWorlds()
	})
	header.PackStart(back)

	newBtn := gtk.NewButtonFromIconName(ui.IconAdd)
	newBtn.SetTooltipText("Add an entry")
	newBtn.ConnectClicked(func() {
		d.Close()
		a.editLore(world.Entry{WorldID: w.ID, Enabled: true}, w)
	})
	header.PackEnd(newBtn)

	page := gtk.NewBox(gtk.OrientationVertical, 8)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)

	entries, err := a.store.LoreEntries(w.ID)
	if err != nil {
		a.toast("Could not read the lorebook: " + err.Error())
	}

	var held, rest []world.Entry
	for _, e := range entries {
		if e.Auto && !e.Enabled {
			held = append(held, e)
		} else {
			rest = append(rest, e)
		}
	}

	if len(entries) == 0 {
		empty := gtk.NewLabel("Nothing here yet.\n\nAstral adds entries as you play: when a scene " +
			"establishes something about a person, a place or how this world works, it is " +
			"written down here and sent back whenever it comes up again. You can add your own too.")
		empty.SetWrap(true)
		empty.SetJustify(gtk.JustifyCenter)
		empty.SetVExpand(true)
		empty.AddCSSClass("dim-label")
		page.Append(empty)
	}

	if len(held) > 0 {
		heading := gtk.NewLabel("Waiting for review")
		heading.SetXAlign(0)
		heading.AddCSSClass("settings-heading")
		page.Append(heading)
		hint := gtk.NewLabel("Astral was not confident about these, so they are switched off and are not being used. Turn one on to accept it.")
		hint.SetXAlign(0)
		hint.SetWrap(true)
		hint.AddCSSClass("settings-hint")
		page.Append(hint)
		for _, e := range held {
			page.Append(a.loreRow(e, w, d))
		}
	}
	if len(rest) > 0 {
		heading := gtk.NewLabel("In use")
		heading.SetXAlign(0)
		heading.AddCSSClass("settings-heading")
		heading.SetMarginTop(8)
		page.Append(heading)
		for _, e := range rest {
			page.Append(a.loreRow(e, w, d))
		}
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// loreRow is one entry, with the switch that decides whether it reaches a scene.
func (a *App) loreRow(e world.Entry, w world.World, parent *adw.Dialog) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)

	card := gtk.NewBox(gtk.OrientationVertical, 4)
	card.AddCSSClass("character-card")
	card.SetHExpand(true)

	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	name := gtk.NewLabel(e.Name)
	name.SetXAlign(0)
	name.SetHExpand(true)
	name.SetEllipsize(3)
	name.AddCSSClass("character-card-name")
	head.Append(name)

	if e.Auto {
		tag := gtk.NewLabel(fmt.Sprintf("learned · %.0f%%", e.Confidence*100))
		tag.SetTooltipText("Written by the model, with how sure it was")
		tag.AddCSSClass("character-card-tag")
		head.Append(tag)
	}
	if e.Constant {
		tag := gtk.NewLabel("always")
		tag.SetTooltipText("Sent on every turn, whether or not it was mentioned")
		tag.AddCSSClass("character-card-tag")
		head.Append(tag)
	}

	sw := gtk.NewSwitch()
	sw.SetActive(e.Enabled)
	sw.SetVAlign(gtk.AlignCenter)
	sw.SetTooltipText("Whether this is sent to the model")
	entry := e
	sw.ConnectStateSet(func(state bool) bool {
		entry.Enabled = state
		// Accepting a held entry makes it the user's, so a later automatic
		// pass cannot silently replace what has just been approved.
		if state && entry.Auto {
			entry.Auto = false
		}
		if _, err := a.store.SaveLoreEntry(entry); err != nil {
			a.toast("Could not save: " + err.Error())
			return true // refuse the change rather than show a state we did not store
		}
		a.reloadLoreIfOpen(w.ID)
		return false
	})
	head.Append(sw)
	card.Append(head)

	if keys := strings.Join(e.Keys, ", "); keys != "" {
		k := gtk.NewLabel("Triggers on: " + keys)
		k.SetXAlign(0)
		k.SetWrap(true)
		k.SetLines(1)
		k.SetEllipsize(3)
		k.AddCSSClass("character-card-tag")
		card.Append(k)
	}

	body := gtk.NewLabel(ui.Snippet(e.Content, 180))
	body.SetXAlign(0)
	body.SetWrap(true)
	body.SetLines(3)
	body.SetEllipsize(3)
	body.AddCSSClass("character-card-desc")
	card.Append(body)
	row.Append(card)

	side := gtk.NewBox(gtk.OrientationVertical, 4)
	side.SetVAlign(gtk.AlignCenter)
	edit := gtk.NewButtonFromIconName(ui.IconEdit)
	edit.SetTooltipText("Edit this entry")
	edit.AddCSSClass("flat")
	edit.ConnectClicked(func() {
		parent.Close()
		a.editLore(entry, w)
	})
	side.Append(edit)

	del := gtk.NewButtonFromIconName(ui.IconTrash)
	del.SetTooltipText("Delete this entry")
	del.AddCSSClass("flat")
	del.ConnectClicked(func() {
		parent.Close()
		a.confirm("Delete “"+entry.Name+"”?",
			"It will stop being sent to the model. If the scene establishes it again, Astral may learn it back.",
			"Delete", func() {
				if err := a.store.DeleteLoreEntry(entry.ID); err != nil {
					a.toast("Could not delete: " + err.Error())
					return
				}
				a.reloadLoreIfOpen(w.ID)
				a.showLorebook(w)
			})
	})
	side.Append(del)
	row.Append(side)
	return row
}

// editLore is the entry editor.
func (a *App) editLore(e world.Entry, w world.World) {
	d := adw.NewDialog()
	if e.ID == 0 {
		d.SetTitle("New entry")
	} else {
		d.SetTitle(e.Name)
	}
	d.SetContentWidth(600)
	d.SetContentHeight(620)

	page := gtk.NewBox(gtk.OrientationVertical, 16)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)

	outer, card := groupCard("")
	nameEntry := gtk.NewEntry()
	nameEntry.SetText(e.Name)
	nameEntry.SetPlaceholderText("Kestrel Bay")
	card.Append(labelledField("Name",
		"What this is about. Astral matches on it when adding to this entry later, so renaming one starts a new entry rather than updating this.",
		nameEntry))

	keysEntry := gtk.NewEntry()
	keysEntry.SetText(strings.Join(e.Keys, ", "))
	keysEntry.SetPlaceholderText("Kestrel Bay, the Bay, the ferry")
	card.Append(labelledField("Triggers",
		"Comma separated. This entry is sent when the recent conversation mentions any of them. "+
			"Keep them specific: a common word matches everything and spends the budget other entries needed.",
		keysEntry))

	frame, view := multilineField(e.Content, 7)
	card.Append(labelledField("What is true",
		"Plain statements of fact, as the model should treat them. Under sixty words works best.",
		frame))

	constant := gtk.NewCheckButton()
	constant.SetChild(wrappingLabel("Always send this, even when nothing mentions it"))
	constant.SetActive(e.Constant)
	constant.SetTooltipText("Use sparingly: it costs its space on every single turn")
	card.Append(constant)

	enabled := gtk.NewCheckButton()
	enabled.SetChild(wrappingLabel("In use"))
	enabled.SetActive(e.Enabled)
	card.Append(enabled)
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
			a.toast("An entry needs a name.")
			nameEntry.GrabFocus()
			return
		}
		if strings.TrimSpace(textOf(view)) == "" {
			a.toast("An entry needs something to say.")
			return
		}
		e.Name = name
		e.Keys = splitTags(keysEntry.Text())
		e.Content = textOf(view)
		e.Constant = constant.Active()
		e.Enabled = enabled.Active()
		// Editing it by hand makes it yours, so no later automatic pass
		// overwrites the decision just made.
		e.Auto = false
		if len(e.Keys) == 0 && !e.Constant {
			a.toast("Add a trigger word, or set it to always send.")
			return
		}
		if _, err := a.store.SaveLoreEntry(e); err != nil {
			a.toast("Could not save: " + err.Error())
			return
		}
		a.reloadLoreIfOpen(w.ID)
		d.Close()
		a.showLorebook(w)
	})
	header.PackEnd(save)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
	nameEntry.GrabFocus()
}

// reloadLoreIfOpen refreshes the lorebook the open scene is using, so an edit
// takes effect on the next turn rather than the next time the chat is opened.
func (a *App) reloadLoreIfOpen(worldID int64) {
	if a.chat == nil {
		return
	}
	if w, _ := a.chat.Lore(); w.ID != worldID {
		return
	}
	if entries, err := a.store.LoreEntries(worldID); err == nil {
		a.chat.SetLore(entries)
	}
}
