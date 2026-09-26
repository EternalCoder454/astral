package ui

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Correcting a turn is how a scene is steered.
//
// By turn twenty the transcript is the strongest instruction the model has:
// it holds twenty worked examples of what is acceptable here, and it outranks
// anything the system prompt says. So a reply that gets one thing wrong is not
// a reply that will be forgotten, it is precedent. Rerolling throws away
// everything that was right about it; deleting loses the turn. Editing is the
// one option that keeps the scene and fixes the line.
//
// Your own messages too, because a typo you sent is quoted back at you for the
// rest of the evening.

// editHeight is the dialog's height. Roomy on purpose: a reply is several
// paragraphs and the point is to read it while changing it.
const editHeight = 460

// EditMessage opens the editor for a row. onSave is given the new text and
// returns whether it was stored, so a failure leaves the dialog open with the
// text still in it rather than losing the edit.
func EditMessage(parent gtk.Widgetter, row *MessageRow, onSave func(string) bool) {
	d := adw.NewDialog()
	d.SetTitle("Edit message")
	d.SetContentWidth(640)
	d.SetContentHeight(editHeight)

	view := gtk.NewTextView()
	view.SetWrapMode(gtk.WrapWordChar)
	view.SetLeftMargin(12)
	view.SetRightMargin(12)
	view.SetTopMargin(12)
	view.SetBottomMargin(12)
	view.AddCSSClass("composer")
	view.Buffer().SetText(row.Text())

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(view)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	header.SetShowStartTitleButtons(false)

	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)

	save := gtk.NewButtonWithLabel("Save")
	save.AddCSSClass("suggested-action")
	save.ConnectClicked(func() {
		buf := view.Buffer()
		start, end := buf.Bounds()
		text := strings.TrimSpace(buf.Text(start, end, false))
		if text == "" {
			// An empty edit is a deletion asked for the wrong way round, and
			// deleting is its own button.
			return
		}
		if onSave(text) {
			d.Close()
		}
	})
	header.PackEnd(save)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scroll)
	d.SetChild(tv)
	d.Present(parent)
	view.GrabFocus()
}
