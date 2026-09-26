package app

import (
	"fmt"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
	"astral/internal/ui"
)

// The rulebook: the standing instructions every scene is under, one per row,
// each switchable on its own.
//
// It replaced a freeform box, and the reason is what people did with the box.
// Instructions accumulate, and once six of them are in one paragraph, trying a
// scene without the third means deleting it and typing it back afterwards. So
// nobody tried. A switch makes a rule something you experiment with, and makes a
// rule you are not using something you keep instead of losing.
//
// Saved on the spot rather than with the rest of the dialog. A checkbox that
// takes effect when you close a window is a checkbox you click twice.

// buildRulebook is the rules card.
func (a *App) buildRulebook() *gtk.Box {
	outer, card := groupCard("Standing rules")

	hint := wrappingLabel("Every scene is played under these, on top of whatever a " +
		"character's own card says. Switch one off to try a scene without it.")
	hint.AddCSSClass("settings-hint")
	card.Append(hint)

	rows := gtk.NewBox(gtk.OrientationVertical, 2)
	card.Append(rows)

	var refresh func()
	save := func() {
		if err := store.SaveConfig(a.cfg); err != nil {
			a.toast("Could not save: " + err.Error())
		}
	}
	refresh = func() {
		for {
			child := rows.FirstChild()
			if child == nil {
				break
			}
			rows.Remove(child)
		}
		rules := a.cfg.Rules()
		if len(rules) == 0 {
			none := wrappingLabel("No rules yet. Anything you would otherwise retype into " +
				"every character belongs here.")
			none.AddCSSClass("settings-hint")
			rows.Append(none)
			return
		}
		for i := range rules {
			rows.Append(a.ruleRow(i, len(rules), save, refresh))
		}
	}
	refresh()

	entry := gtk.NewEntry()
	entry.SetHExpand(true)
	entry.SetPlaceholderText("Keep replies to two paragraphs")
	add := gtk.NewButtonWithLabel("Add")

	commit := func() {
		text := entry.Text()
		if text == "" {
			return
		}
		if !a.cfg.AddRule(text) {
			a.toast(fmt.Sprintf("That is the limit of %d rules. A list long enough to "+
				"contradict itself is worse than a short one.", store.MaxRules))
			return
		}
		entry.SetText("")
		save()
		refresh()
	}
	entry.ConnectActivate(commit)
	add.ConnectClicked(commit)

	adder := gtk.NewBox(gtk.OrientationHorizontal, 8)
	adder.Append(entry)
	adder.Append(add)
	card.Append(adder)
	return outer
}

// ruleRow is one rule: a switch, the text, and the controls to move or drop it.
func (a *App) ruleRow(i, n int, save, refresh func()) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 6)
	rule := a.cfg.Rulebook[i]

	on := gtk.NewCheckButton()
	on.SetActive(rule.Enabled)
	on.SetVAlign(gtk.AlignCenter)
	on.SetTooltipText("Whether this rule is in force")
	on.ConnectToggled(func() {
		a.cfg.Rulebook[i].Enabled = on.Active()
		save()
	})
	row.Append(on)

	// Editable in place. A rule is one sentence, and a dialog to change one
	// sentence is a dialog nobody opens to fix a word.
	text := gtk.NewEntry()
	text.SetText(rule.Text)
	text.SetHExpand(true)
	text.AddCSSClass("flat")
	commit := func() {
		a.cfg.Rulebook[i].Text = text.Text()
		save()
	}
	text.ConnectActivate(commit)
	// Also on losing focus, because clicking straight to Close is the ordinary
	// way to finish typing and pressing Return first is not obvious.
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(commit)
	text.AddController(focus)
	if !rule.Enabled {
		text.AddCSSClass("rule-off")
	}
	row.Append(text)

	// Order is worth having even though the rules go out as one block: a model
	// weights the end of a list, so the rule you most want obeyed belongs last.
	up := gtk.NewButtonFromIconName("go-up-symbolic")
	up.AddCSSClass("flat")
	up.SetVAlign(gtk.AlignCenter)
	up.SetTooltipText("Move this rule earlier")
	up.SetSensitive(i > 0)
	up.ConnectClicked(func() {
		if a.cfg.MoveRule(i, -1) {
			save()
			refresh()
		}
	})
	row.Append(up)

	down := gtk.NewButtonFromIconName("go-down-symbolic")
	down.AddCSSClass("flat")
	down.SetVAlign(gtk.AlignCenter)
	down.SetTooltipText("Move this rule later, where it is weighted more")
	down.SetSensitive(i < n-1)
	down.ConnectClicked(func() {
		if a.cfg.MoveRule(i, 1) {
			save()
			refresh()
		}
	})
	row.Append(down)

	del := gtk.NewButtonFromIconName(ui.IconTrash)
	del.AddCSSClass("flat")
	del.SetVAlign(gtk.AlignCenter)
	del.SetTooltipText("Delete this rule")
	del.ConnectClicked(func() {
		if a.cfg.RemoveRule(i) {
			save()
			refresh()
		}
	})
	row.Append(del)
	return row
}
