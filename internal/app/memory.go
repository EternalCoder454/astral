package app

import (
	"strings"

	"astral/internal/store"
	"astral/internal/ui"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// A scene's memory, where you can see it.
//
// Astral has remembered long scenes for a while: it folds early turns into a
// written record and recalls the exact moments a conversation comes back to.
// None of it could be seen, so none of it could be corrected, and a record
// that got one thing wrong went on being wrong for the rest of the scene.
// Every app that does this well shows it: this is that page.

// editMemory shows a scene's record, editable, and what is pinned.
func (a *App) editMemory() {
	if a.chat == nil {
		return
	}
	d := adw.NewDialog()
	d.SetTitle("Scene Memory")
	d.SetContentWidth(620)

	page := gtk.NewBox(gtk.OrientationVertical, 16)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)

	recap, upto := a.chat.Recap()
	outer, card := groupCard("")
	frame, view := multilineField(recap, 8)
	hint := "What the model is told about the part of the scene it can no longer see."
	if upto == 0 {
		hint = "Written once the scene outgrows the model's memory. Until then it reads every turn."
	}
	card.Append(labelledField("Record of the Scene So Far", hint, frame))
	view.SetEditable(upto != 0)
	page.Append(outer)

	pinsOuter, pinsCard := groupCard("Pinned")
	pins := a.chat.Pins()
	if len(pins) == 0 {
		empty := gtk.NewLabel("Nothing pinned. Point at a message and use the pin to keep it in mind for good.")
		empty.SetXAlign(0)
		empty.SetWrap(true)
		empty.AddCSSClass("settings-hint")
		pinsCard.Append(empty)
	}
	for _, p := range pins {
		pinsCard.Append(a.pinRow(p, pinsCard))
	}
	page.Append(pinsOuter)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)
	save := gtk.NewButtonWithLabel("Save")
	save.AddCSSClass("suggested-action")
	save.SetSensitive(upto != 0)
	save.ConnectClicked(func() {
		text := strings.TrimSpace(textOf(view))
		if text == strings.TrimSpace(recap) {
			d.Close()
			return
		}
		if err := a.chat.SetRecap(text); err != nil {
			a.toast("Could not save the record: " + err.Error())
			return
		}
		d.Close()
		a.toast("Saved, from your next message on.")
	})
	header.PackEnd(save)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolledToFit(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// pinRow is one pinned message: who said it, the start of it, and Unpin.
func (a *App) pinRow(p store.Moment, list *gtk.Box) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	text := gtk.NewLabel(a.chat.SpeakerName(p.Role, p.CharacterID) + ": " + ui.Snippet(p.Content, 180))
	text.SetXAlign(0)
	text.SetWrap(true)
	text.SetWrapMode(pango.WrapWordChar)
	text.SetHExpand(true)
	text.SetLines(3)
	text.SetEllipsize(pango.EllipsizeEnd)
	row.Append(text)
	unpin := gtk.NewButtonWithLabel("Unpin")
	unpin.AddCSSClass("flat")
	unpin.SetVAlign(gtk.AlignCenter)
	unpin.ConnectClicked(func() {
		if err := a.chat.Unpin(p.ID); err != nil {
			a.toast("Could not unpin it: " + err.Error())
			return
		}
		list.Remove(row)
	})
	row.Append(unpin)
	return row
}

// branchChat copies a chat up to a message into a new one, and opens it.
func (a *App) branchChat(chatID, messageID int64) {
	src, err := a.store.Chat(chatID)
	if err != nil {
		a.toast("Could not branch: " + err.Error())
		return
	}
	ch, err := a.store.BranchChat(chatID, messageID, store.BranchTitle(src.Title))
	if err != nil {
		a.toast("Could not branch: " + err.Error())
		return
	}
	a.refreshSidebar()
	if err := a.openChat(ch.ID); err != nil {
		a.toast("Branched, but could not open it: " + err.Error())
		return
	}
	a.toast("Branched. The original is unchanged in your chats.")
}
