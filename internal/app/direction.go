package app

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// A scene direction is the one control that steers a roleplay without editing
// anything permanent. A character's instructions are standing rules and a
// world's lore is what is true; neither is the right place to say "she is
// about to work out that he lied", which is about the next few turns and is
// meant to be cleared once it has happened.
//
// It reaches the model at the very end of the context, after the character,
// the style and the instructions, which is the position a model weights most.

// directionExamples are the shapes a direction takes, shown rather than
// described. A blank box with "what should happen next?" over it gets answers
// like "make it interesting"; three concrete examples get answers that work.
var directionExamples = []string{
	"{{char}} is about to work out that {{user}} lied about the manifest.",
	"Move them out of the map room and down to the docks.",
	"Something is wrong with the tide. Nobody has said so yet.",
	"Wind this down. Find a natural place to end the evening.",
}

// editDirection opens the scene-direction editor.
func (a *App) editDirection() {
	if a.chat == nil {
		return
	}
	d := adw.NewDialog()
	d.SetTitle("Scene direction")
	d.SetContentWidth(560)

	page := gtk.NewBox(gtk.OrientationVertical, 16)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)

	outer, card := groupCard("")
	frame, view := multilineField(a.chat.Note(), 5)
	card.Append(labelledField("Where this scene should go next",
		"Sent every turn until you change it. The model steers toward it without saying it out loud.",
		frame))

	ex := gtk.NewLabel("For example:\n" + strings.Join(directionExamples, "\n"))
	ex.SetXAlign(0)
	ex.SetWrap(true)
	ex.SetSelectable(true)
	ex.AddCSSClass("settings-hint")
	card.Append(ex)
	page.Append(outer)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)

	// Clearing is a first-class action, not a matter of selecting the text and
	// deleting it. A direction is supposed to be finished with.
	if strings.TrimSpace(a.chat.Note()) != "" {
		clear := gtk.NewButtonWithLabel("Clear")
		clear.SetTooltipText("Stop sending a direction with this scene")
		clear.ConnectClicked(func() {
			if err := a.chat.SetNote(""); err != nil {
				a.toast("Could not clear it: " + err.Error())
				return
			}
			d.Close()
			a.toast("Direction cleared.")
		})
		header.PackStart(clear)
	}

	save := gtk.NewButtonWithLabel("Save")
	save.AddCSSClass("suggested-action")
	save.ConnectClicked(func() {
		note := strings.TrimSpace(textOf(view))
		if err := a.chat.SetNote(note); err != nil {
			a.toast("Could not save: " + err.Error())
			return
		}
		d.Close()
		if note == "" {
			a.toast("Direction cleared.")
		} else {
			// Said explicitly, because the reply already on screen was written
			// before this existed and it would otherwise look ignored.
			a.toast("Direction set. It applies from your next message.")
		}
	})
	header.PackEnd(save)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolledToFit(page))
	d.SetChild(tv)
	d.Present(a.win)
	view.GrabFocus()
}
