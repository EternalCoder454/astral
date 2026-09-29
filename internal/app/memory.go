package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"astral/internal/chars"
	"astral/internal/scene"

	"astral/internal/store"
	"astral/internal/ui"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
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
	ui.FreeOnClose(d)
	d.SetTitle("Scene Memory")
	d.SetContentWidth(620)

	page := gtk.NewBox(gtk.OrientationVertical, 16)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)

	// How the scene stands now: where and when, and what everyone is wearing
	// and holding, how things are between you, and what is unresolved, sent
	// every turn. See chars.SceneState.
	nowOuter, nowCard := groupCard("Scene State")
	oldSetting, oldState, oldAuto := a.chat.Setting()
	setting := gtk.NewEntry()
	setting.SetText(oldSetting)
	setting.SetPlaceholderText("Her flat, two in the morning, rain on the windows")
	setting.SetMaxLength(chars.SettingChars)
	setting.SetHExpand(true)
	suggest := gtk.NewButtonWithLabel("Suggest")
	suggest.SetTooltipText("Have the model say how the scene stands now")
	// Kept up to date by Astral after each reply until you write your own,
	// and then left alone unless this is switched back on.
	auto := gtk.NewSwitch()
	auto.SetActive(oldAuto)
	auto.SetVAlign(gtk.AlignCenter)
	suggesting := false
	manual := func() {
		if !suggesting {
			auto.SetActive(false)
		}
	}
	setting.ConnectChanged(manual)
	parts := make(map[string]*gtk.Entry, len(chars.StateFields))
	for _, f := range chars.StateFields {
		e := gtk.NewEntry()
		e.SetText(oldState.Get(f.Key))
		e.SetPlaceholderText(f.Example)
		e.SetMaxLength(chars.StateChars)
		e.SetHExpand(true)
		e.ConnectChanged(manual)
		parts[f.Key] = e
	}
	suggest.ConnectClicked(func() {
		suggest.SetSensitive(false)
		suggest.SetLabel("Thinking…")
		a.chat.SuggestSetting(func(line string, state chars.SceneState, err error) {
			suggest.SetSensitive(true)
			suggest.SetLabel("Suggest")
			if err != nil {
				a.toast("Could not suggest one: " + err.Error())
				return
			}
			suggesting = true
			if line != "" {
				setting.SetText(line)
			}
			for key, e := range parts {
				if v := state.Get(key); v != "" {
					e.SetText(v)
				}
			}
			suggesting = false
		})
	})
	nowRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	nowRow.Append(setting)
	nowRow.Append(suggest)
	nowCard.Append(labelledField("Where and When",
		"Sent every turn with the rest of this, so the scene keeps track of how it stands.", nowRow))
	for _, f := range chars.StateFields {
		nowCard.Append(labelledField(f.Label, f.Hint, parts[f.Key]))
	}
	autoRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	autoLabel := gtk.NewLabel("Update as the Scene Moves")
	autoLabel.SetXAlign(0)
	autoLabel.SetHExpand(true)
	autoLabel.SetTooltipText("Astral updates these after each reply; writing your own turns it off")
	autoRow.Append(autoLabel)
	autoRow.Append(auto)
	nowCard.Append(autoRow)
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

	// saveMemory keeps what was changed here, and says whether anything was
	// and whether it all went. Save uses it, and so does Continue in a New
	// Chat, which carries on from what is stored and would otherwise drop
	// what was typed here.
	saveMemory := func() (changed, ok bool) {
		text := strings.TrimSpace(textOf(view))
		if upto != 0 && text != strings.TrimSpace(recap) {
			if err := a.chat.SetRecap(text); err != nil {
				a.toast("Could not save the record: " + err.Error())
				return changed, false
			}
			changed = true
		}
		var state chars.SceneState
		for key, e := range parts {
			state.Set(key, e.Text())
		}
		if now := strings.TrimSpace(setting.Text()); now != oldSetting || state != oldState || auto.Active() != oldAuto {
			if err := a.chat.SetSetting(now, state, auto.Active()); err != nil {
				a.toast("Could not save the scene state: " + err.Error())
				return changed, false
			}
			changed = true
		}
		return changed, true
	}

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
		pinsCard.Append(a.pinRow(p))
	}
	page.Append(pinsOuter)
	page.Append(a.seenCard())
	page.Append(a.usageCard())
	if ch := a.chat.Chat(); scene.CanContinue(ch) {
		contOuter, contCard := groupCard("")
		cont := gtk.NewButtonWithLabel("Continue in a New Chat")
		cont.SetHAlign(gtk.AlignStart)
		id := ch.ID
		cont.ConnectClicked(func() {
			if _, ok := saveMemory(); !ok {
				return
			}
			d.Close()
			a.continueChat(id)
		})
		contCard.Append(labelledField("Start Fresh, Keep the Story",
			"A new chat with this record, the pins and the last few messages, and the model's memory nearly empty.", cont))
		page.Append(contOuter)
	}

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)
	save := gtk.NewButtonWithLabel("Save")
	save.AddCSSClass("suggested-action")
	save.ConnectClicked(func() {
		if changed, ok := saveMemory(); ok {
			d.Close()
			if changed {
				a.toast("Saved, from your next message on.")
			}
		}
	})
	header.PackEnd(save)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolledToFit(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// seenCard lists what the next turn sends besides the conversation: the
// lorebook entries and what brought each in, and the earlier moments it is
// reminded of. The meter below says how much room they take; this says what
// they are, so a character who forgets something, or brings up a part of the
// world that has nothing to do with the scene, can be seen to have been told.
func (a *App) seenCard() *gtk.Box {
	s := a.chat.Seen()
	outer, card := groupCard("What the Model Sees")
	line := func(text string, hint bool) {
		l := gtk.NewLabel(text)
		l.SetXAlign(0)
		l.SetWrap(true)
		l.SetWrapMode(pango.WrapWordChar)
		if hint {
			l.AddCSSClass("settings-hint")
		}
		card.Append(l)
	}
	heading := func(text string) {
		l := gtk.NewLabel(text)
		l.SetXAlign(0)
		l.AddCSSClass("heading")
		card.Append(l)
	}
	line("Sent with your next message, besides the conversation itself.", true)
	if s.World != "" {
		heading("From the Lorebook of " + s.World)
		if len(s.Lore) == 0 {
			line("No entries right now. They come in when the scene mentions them.", true)
		}
		for _, l := range s.Lore {
			if l.Left {
				line(l.Name+": left out, no room. "+l.Why, true)
				continue
			}
			line(l.Name+": "+l.Why, false)
		}
	}
	if s.Record == 0 {
		heading("Earlier Moments")
		line("None needed yet: the whole scene still fits in the model's memory.", true)
		return outer
	}
	if len(s.Pinned) > 0 {
		heading("Pinned")
		for _, m := range s.Pinned {
			line(m.Who+": "+ui.Snippet(m.Text, 160), false)
		}
	}
	heading("Recalled From Earlier")
	if len(s.Recalled) == 0 {
		line("Nothing from before the record matches what is happening now.", true)
	}
	for _, m := range s.Recalled {
		line(m.Who+": "+ui.Snippet(m.Text, 160), false)
	}
	return outer
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
func (a *App) pinRow(p store.Moment) *gtk.Box {
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
	// The row is found from the button when it is pressed rather than held
	// by the handler: a handler that holds the widgets around its own would
	// keep the dialog alive after it closes.
	unpin.ConnectClicked(func() {
		if err := a.chat.Unpin(p.ID); err != nil {
			a.toast("Could not unpin it: " + err.Error())
			return
		}
		if row := unpin.Parent(); row != nil {
			gtk.BaseWidget(row).SetVisible(false)
		}
	})
	row.Append(unpin)
	return row
}

// continueChat carries a chat on in a new one, and opens it: the story so
// far written into its record, the pins, and the last few messages word for
// word. See scene.ContinueChat.
func (a *App) continueChat(chatID int64) {
	src, err := a.store.Chat(chatID)
	if err != nil {
		a.toast("Could not continue it: " + err.Error())
		return
	}
	if !scene.CanContinue(src) {
		a.toast("Only a scene or a general chat can be continued.")
		return
	}
	if a.chat != nil && a.chat.Chat().ID == chatID && a.chat.Busy() {
		a.toast("Wait for the reply to finish, then continue.")
		return
	}
	if a.continuing {
		return
	}
	a.continuing = true
	a.toast("Writing the story so far for the new chat…")
	client, cfg := a.client, a.cfg
	sceneModel := src.Model
	if sceneModel == "" {
		sceneModel = cfg.Model
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), continueTimeout)
		defer cancel()
		model := scene.FitHousekeeping(ctx, client, cfg.HousekeepingModel, sceneModel)
		next, err := scene.ContinueChat(ctx, client, model, a.store, cfg, src)
		scene.NoteUsed(model, "")
		coreglib.IdleAdd(func() bool {
			a.continuing = false
			if err != nil {
				a.toast("Could not continue it: " + friendlyBuildError(err))
				return false
			}
			a.refreshSidebar()
			open := func() {
				if err := a.openChat(next.ID); err != nil {
					a.toast("Continued, but could not open it: " + err.Error())
					return
				}
				a.toast("Continued in a new chat. The original is unchanged in your chats.")
			}
			// Opened for you only if you are still where you asked for it
			// and nothing is being written: this can take minutes, and
			// switching chats under a reply would throw that reply away.
			if a.chat != nil && a.chat.Chat().ID == chatID && !a.chat.Busy() {
				open()
				return false
			}
			a.toastAction("“"+next.Title+"” is ready.", "Open", open)
			return false
		})
	}()
}

// continueTimeout bounds writing the story so far for a continuation: a
// whole window's worth of scene, read by a large model.
const continueTimeout = 5 * time.Minute

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
