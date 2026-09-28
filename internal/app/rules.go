package app

import (
	"fmt"
	"strings"

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
	outer, card := groupCard("Rules")

	hint := wrappingLabel("Every chat follows these, with {{char}} for the character and {{user}} for you.")
	hint.AddCSSClass("settings-hint")
	card.Append(hint)

	rows := gtk.NewBox(gtk.OrientationVertical, 8)
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
			none := wrappingLabel("No rules yet.")
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
			a.toast(fmt.Sprintf("That is the limit of %d rules.", store.MaxRules))
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
//
// The text is a wrapping box rather than a single-line field. A rule is a
// sentence and sentences are not short: "Nobody in this scene explains their own
// feelings out loud, and nobody says what they are about to do before doing it"
// is a perfectly ordinary rule and was previously forty visible characters with
// the rest off the side of a field you had to arrow through to read.
func (a *App) ruleRow(i, n int, save, refresh func()) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 6)
	rule := a.cfg.Rulebook[i]

	on := gtk.NewCheckButton()
	on.SetActive(rule.Enabled)
	// Top rather than centre: a rule three lines tall would otherwise put its
	// switch and its buttons halfway down, level with nothing.
	on.SetVAlign(gtk.AlignStart)
	on.SetMarginTop(6)
	on.SetTooltipText("Whether this rule is in force")
	on.ConnectToggled(func() {
		a.cfg.Rulebook[i].Enabled = on.Active()
		save()
	})
	row.Append(on)

	// Editable in place, and as tall as the rule needs. A dialog to change one
	// sentence is a dialog nobody opens to fix a word.
	frame, view := multilineField(rule.Text, 1)
	frame.SetHExpand(true)
	if !rule.Enabled {
		view.AddCSSClass("rule-off")
	}
	// On losing focus rather than on a key: Return inside a rule is a line break,
	// which a long rule is entitled to, and clicking away is how anyone finishes
	// typing in a box with no button of its own.
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(func() {
		text := strings.TrimSpace(textOf(view))
		if text == a.cfg.Rulebook[i].Text {
			return
		}
		a.cfg.Rulebook[i].Text = text
		save()
		// Emptied is deleted. The alternative is a blank row that does nothing
		// and cannot be told apart from a rule you have not written yet.
		if text == "" {
			a.cfg.RemoveRule(i)
			refresh()
		}
	})
	view.AddController(focus)
	row.Append(frame)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 0)
	buttons.SetVAlign(gtk.AlignStart)

	// Order is worth having even though the rules go out as one block: a model
	// weights the end of a list, so the rule you most want obeyed belongs last.
	up := gtk.NewButtonFromIconName("go-up-symbolic")
	up.AddCSSClass("flat")
	up.SetTooltipText("Move this rule earlier")
	up.SetSensitive(i > 0)
	up.ConnectClicked(func() {
		if a.cfg.MoveRule(i, -1) {
			save()
			refresh()
		}
	})
	buttons.Append(up)

	down := gtk.NewButtonFromIconName("go-down-symbolic")
	down.AddCSSClass("flat")
	down.SetTooltipText("Move this rule later, where it is weighted more")
	down.SetSensitive(i < n-1)
	down.ConnectClicked(func() {
		if a.cfg.MoveRule(i, 1) {
			save()
			refresh()
		}
	})
	buttons.Append(down)

	del := gtk.NewButtonFromIconName(ui.IconTrash)
	del.AddCSSClass("flat")
	del.SetTooltipText("Delete this rule")
	del.ConnectClicked(func() {
		if a.cfg.RemoveRule(i) {
			save()
			refresh()
		}
	})
	buttons.Append(del)
	row.Append(buttons)
	return row
}
