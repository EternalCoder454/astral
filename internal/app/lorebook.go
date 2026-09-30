package app

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

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
	ui.FreeOnClose(d)
	d.SetTitle(w.Name)
	d.SetContentWidth(620)
	d.SetContentHeight(680)

	header := adw.NewHeaderBar()
	back := gtk.NewButtonFromIconName(ui.IconPanelLeft)
	back.SetTooltipText("Back to " + w.Name)
	back.ConnectClicked(func() {
		d.Close()
		a.showWorld(w)
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
		empty := gtk.NewLabel("Nothing here yet, but Astral writes entries as you play.")
		empty.SetWrap(true)
		empty.SetJustify(gtk.JustifyCenter)
		empty.SetVExpand(true)
		empty.AddCSSClass("dim-label")
		page.Append(empty)
	}

	if len(held) > 0 {
		heading := gtk.NewLabel("Waiting for Review")
		heading.SetXAlign(0)
		heading.AddCSSClass("settings-heading")
		page.Append(heading)
		hint := gtk.NewLabel("Astral was unsure of these, so turn one on to accept it.")
		hint.SetXAlign(0)
		hint.SetWrap(true)
		hint.AddCSSClass("settings-hint")
		page.Append(hint)
		for _, e := range held {
			page.Append(a.loreRow(e, w, d))
		}
	}
	if len(rest) > 0 {
		heading := gtk.NewLabel("In Use")
		heading.SetXAlign(0)
		heading.AddCSSClass("settings-heading")
		heading.SetMarginTop(8)
		page.Append(heading)
		rows := make([]filterRow, 0, len(rest))
		for _, e := range rest {
			rows = append(rows, filterRow{
				Widget: a.loreRow(e, w, d),
				// The content matters as much as the name here. What anyone
				// remembers about a lore entry is what it said, not what it
				// was called, and this is the one list the app fills by itself
				// while you are busy doing something else.
				Text: strings.ToLower(e.Name + " " + strings.Join(e.Keys, " ") + " " + e.Content),
			})
		}
		searchableList(page, "Search entries", rows)
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
	name.SetEllipsize(pango.EllipsizeEnd)
	name.AddCSSClass("character-card-name")
	head.Append(name)

	if e.Auto {
		// Not "learned · 45%", which reads as a progress bar rather than as a
		// confidence, and left people wondering what the other 55% would be.
		tag := gtk.NewLabel(fmt.Sprintf("model · %.0f%% sure", e.Confidence*100))
		tag.SetTooltipText(fmt.Sprintf("Astral wrote this and was %.0f%% sure of it.", e.Confidence*100))
		tag.AddCSSClass("character-card-tag")
		head.Append(tag)
	}
	if e.Constant {
		tag := gtk.NewLabel("always")
		tag.SetTooltipText("Sent on every turn, whether or not it was mentioned")
		tag.AddCSSClass("character-card-tag")
		head.Append(tag)
	}

	entry := e
	card.Append(head)

	if keys := strings.Join(e.Keys, ", "); keys != "" {
		k := gtk.NewLabel("Triggers on: " + keys)
		k.SetXAlign(0)
		k.SetHAlign(gtk.AlignStart)
		k.SetMaxWidthChars(44)
		k.SetEllipsize(pango.EllipsizeEnd)
		k.SetTooltipText(keys)
		k.AddCSSClass("character-card-tag")
		card.Append(k)
	}

	body := gtk.NewLabel(ui.Snippet(e.Content, 260))
	body.SetXAlign(0)
	body.SetWrap(true)
	body.SetLines(3)
	body.SetEllipsize(pango.EllipsizeEnd)
	body.AddCSSClass("character-card-desc")
	card.Append(body)
	row.Append(card)

	// The switch lives in its own fixed-width column rather than at the end of
	// the card's heading. Inside the heading it came after a run of tags that
	// differ per entry, so every switch in the list landed at a different x
	// and the column read as scattered. Out here they line up.
	swCell := gtk.NewBox(gtk.OrientationVertical, 0)
	swCell.AddCSSClass("lore-switch-cell")
	swCell.SetVAlign(gtk.AlignCenter)
	sw := gtk.NewSwitch()
	sw.SetActive(e.Enabled)
	sw.SetHAlign(gtk.AlignCenter)
	sw.SetTooltipText("Whether this entry is sent to the model")
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
	swCell.Append(sw)
	row.Append(swCell)

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
		a.confirm("Delete “"+entry.Name+"”",
			"It stops being sent to the model, though Astral may learn it again.",
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
	ui.FreeOnClose(d)
	if e.ID == 0 {
		d.SetTitle("New Entry")
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
		"What this is about; renaming it starts a new entry.",
		nameEntry))

	keysEntry := gtk.NewEntry()
	keysEntry.SetText(strings.Join(e.Keys, ", "))
	keysEntry.SetPlaceholderText("Kestrel Bay, the Bay, the ferry")
	card.Append(labelledField("Triggers",
		"Words that send this entry when the scene, or another sent entry, mentions them.",
		keysEntry))

	frame, view := multilineField(e.Content, 7)
	card.Append(labelledField("What Is True",
		"Plain statements of fact, ideally under sixty words.",
		frame))

	constant := gtk.NewCheckButton()
	constant.SetChild(wrappingLabel("Always send this, even when nothing mentions it"))
	constant.SetActive(e.Constant)
	constant.SetTooltipText("Use sparingly: it costs its space on every single turn")
	card.Append(constant)

	enabled := gtk.NewCheckButton()
	enabled.SetChild(wrappingLabel("In Use"))
	enabled.SetActive(e.Enabled)
	card.Append(enabled)
	page.Append(outer)

	// When a triggered entry is sent, apart from being triggered. The chance
	// and the wait do nothing to an entry that is always sent, which the
	// tooltips say rather than switching them off: a handler on the checkbox
	// that reached for these two would keep the dialog's widgets alive after it
	// closed.
	whenOuter, whenCard := groupCard("When It Is Sent")
	chance := gtk.NewSpinButtonWithRange(1, 100, 1)
	chance.SetNumeric(true)
	chance.SetValue(float64(e.Odds()))
	chance.SetHAlign(gtk.AlignStart)
	// As wide as the wait below it, whose range has more digits, so the two
	// line up.
	chance.SetWidthChars(5)
	chance.SetTooltipText("Ignored when the entry is always sent")
	whenCard.Append(labelledField("Chance in Percent",
		"How often it is sent when triggered, 100 being every time.",
		chance))

	wait := gtk.NewSpinButtonWithRange(0, 10000, 1)
	wait.SetNumeric(true)
	wait.SetValue(float64(max(e.Wait, 0)))
	wait.SetHAlign(gtk.AlignStart)
	wait.SetWidthChars(5)
	wait.SetTooltipText("Ignored when the entry is always sent")
	whenCard.Append(labelledField("Wait for Messages",
		"It is not sent until the scene has this many messages, 0 being from the start.",
		wait))

	groupEntry := gtk.NewEntry()
	groupEntry.SetText(e.Group)
	groupEntry.SetMaxLength(world.MaxGroupChars)
	groupEntry.SetPlaceholderText("Rumours")
	whenCard.Append(labelledField("Group",
		"Of the triggered entries sharing a group, only the highest priority one is sent.",
		groupEntry))
	page.Append(whenOuter)

	header := saveHeader(d, "", func() bool {
		name := strings.TrimSpace(nameEntry.Text())
		if name == "" {
			a.toast("An entry needs a name.")
			nameEntry.GrabFocus()
			return false
		}
		if strings.TrimSpace(textOf(view)) == "" {
			a.toast("An entry needs something to say.")
			return false
		}
		e.Name = name
		e.Keys = splitTags(keysEntry.Text())
		e.Content = textOf(view)
		e.Constant = constant.Active()
		e.Enabled = enabled.Active()
		// What was typed in a spin button is not its value until it is read
		// back, and Save can be pressed before the field has lost focus.
		chance.Update()
		wait.Update()
		e.Chance = chance.ValueAsInt()
		e.Wait = wait.ValueAsInt()
		e.Group = world.CleanGroup(groupEntry.Text())
		// Editing it by hand makes it yours, so no later automatic pass
		// overwrites the decision just made.
		e.Auto = false
		if len(e.Keys) == 0 && !e.Constant {
			a.toast("Add a trigger word, or set it to always send.")
			return false
		}
		if _, err := a.store.SaveLoreEntry(e); err != nil {
			a.toast("Could not save: " + err.Error())
			return false
		}
		a.reloadLoreIfOpen(w.ID)
		a.showLorebook(w)
		return true
	})

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
