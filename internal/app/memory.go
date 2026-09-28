package app

import (
	"fmt"
	"strconv"
	"strings"

	"astral/internal/chars"

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

	// Where and when the scene is now, in a line sent every turn.
	nowOuter, nowCard := groupCard("")
	setting := gtk.NewEntry()
	setting.SetText(a.chat.Setting())
	setting.SetPlaceholderText("Her flat, two in the morning, rain on the windows")
	setting.SetMaxLength(chars.SettingChars)
	setting.SetHExpand(true)
	suggest := gtk.NewButtonWithLabel("Suggest")
	suggest.SetTooltipText("Have the model say where and when the scene is now")
	suggest.ConnectClicked(func() {
		suggest.SetSensitive(false)
		suggest.SetLabel("Thinking…")
		a.chat.SuggestSetting(func(line string, err error) {
			suggest.SetSensitive(true)
			suggest.SetLabel("Suggest")
			if err != nil {
				a.toast("Could not suggest one: " + err.Error())
				return
			}
			if line != "" {
				setting.SetText(line)
			}
		})
	})
	nowRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	nowRow.Append(setting)
	nowRow.Append(suggest)
	nowCard.Append(labelledField("Where and When",
		"Sent every turn, so the scene keeps track of the room it is in.", nowRow))
	page.Append(nowOuter)

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
	page.Append(a.usageCard())

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)
	save := gtk.NewButtonWithLabel("Save")
	save.AddCSSClass("suggested-action")
	oldSetting := a.chat.Setting()
	save.ConnectClicked(func() {
		text := strings.TrimSpace(textOf(view))
		changed := false
		if upto != 0 && text != strings.TrimSpace(recap) {
			if err := a.chat.SetRecap(text); err != nil {
				a.toast("Could not save the record: " + err.Error())
				return
			}
			changed = true
		}
		if now := strings.TrimSpace(setting.Text()); now != oldSetting {
			if err := a.chat.SetSetting(now); err != nil {
				a.toast("Could not save where and when: " + err.Error())
				return
			}
			changed = true
		}
		d.Close()
		if changed {
			a.toast("Saved, from your next message on.")
		}
	})
	header.PackEnd(save)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolledToFit(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// usageCard shows how full the model's memory is on the next turn, and what
// with, the way Character.AI's memory meter does.
func (a *App) usageCard() *gtk.Box {
	u := a.chat.Usage()
	outer, card := groupCard("Memory Use")
	total := u.Used + u.Reply
	frac := float64(total) / float64(max(u.Window, 1))
	bar := gtk.NewLevelBar()
	bar.SetMinValue(0)
	bar.SetMaxValue(1)
	bar.SetValue(min(frac, 1))
	bar.SetHExpand(true)
	card.Append(bar)
	head := gtk.NewLabel(fmt.Sprintf("About %s of the %s tokens the model holds, %d%%, with %s kept for the reply.",
		thousands(u.Used), thousands(u.Window), int(frac*100+0.5), thousands(u.Reply)))
	head.SetXAlign(0)
	head.SetWrap(true)
	card.Append(head)
	for _, p := range u.Parts {
		l := gtk.NewLabel(fmt.Sprintf("%s: %s", p.Name, thousands(p.Tokens)))
		l.SetXAlign(0)
		l.AddCSSClass("settings-hint")
		card.Append(l)
	}
	if u.FoldsAt > 0 {
		note := gtk.NewLabel(fmt.Sprintf("The conversation is %s of %s tokens before its oldest turns fold into the record.",
			thousands(u.Conversation), thousands(u.FoldsAt)))
		note.SetXAlign(0)
		note.SetWrap(true)
		note.AddCSSClass("settings-hint")
		card.Append(note)
	}
	return outer
}

// thousands writes a count with separators: 12,480.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
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
