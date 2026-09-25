package app

import (
	"context"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/ui"
)

// showStyles lists the writing styles and lets one be chosen, edited or
// removed. It mirrors the character list deliberately: the two are the same
// kind of thing — a named bundle of instructions — and there is no reason for
// them to be managed in two different shapes.
func (a *App) showStyles() {
	d := adw.NewDialog()
	d.SetTitle("Writing styles")
	d.SetContentWidth(560)
	d.SetContentHeight(620)

	header := adw.NewHeaderBar()

	designBtn := gtk.NewButtonFromIconName(ui.IconDesigner)
	designBtn.SetTooltipText("Design a style with the model")
	designBtn.ConnectClicked(func() {
		d.Close()
		a.newStyleDesignerChat()
	})
	header.PackStart(designBtn)

	newBtn := gtk.NewButtonFromIconName(ui.IconAdd)
	newBtn.SetTooltipText("Write a style yourself")
	newBtn.ConnectClicked(func() {
		d.Close()
		a.editStyle(chars.WritingStyle{}, true)
	})
	header.PackEnd(newBtn)

	list := gtk.NewBox(gtk.OrientationVertical, 8)
	list.SetMarginTop(14)
	list.SetMarginBottom(14)
	list.SetMarginStart(14)
	list.SetMarginEnd(14)

	active := a.cfg.Style().Name
	for _, st := range a.cfg.Styles() {
		list.Append(a.styleRow(st, st.Name == active, d))
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(list))
	d.SetChild(tv)
	d.Present(a.win)
}

// styleRow is one style: click to use it, with edit and delete alongside.
func (a *App) styleRow(st chars.WritingStyle, active bool, parent *adw.Dialog) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)

	use := gtk.NewButton()
	use.AddCSSClass("character-card")
	use.SetHExpand(true)

	col := gtk.NewBox(gtk.OrientationVertical, 3)
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	name := gtk.NewLabel(st.Name)
	name.SetXAlign(0)
	name.AddCSSClass("character-card-name")
	head.Append(name)
	if active {
		badge := gtk.NewLabel("In use")
		badge.AddCSSClass("character-card-tag")
		head.Append(badge)
	}
	col.Append(head)

	desc := gtk.NewLabel(ui.Snippet(st.Resolved(), 150))
	desc.SetXAlign(0)
	desc.SetWrap(true)
	desc.SetLines(3)
	desc.SetEllipsize(3)
	desc.AddCSSClass("character-card-desc")
	col.Append(desc)
	use.SetChild(col)
	use.SetTooltipText("Write in this style")

	style := st
	use.ConnectClicked(func() {
		a.cfg.ActiveStyle = style.Name
		if err := store.SaveConfig(a.cfg); err != nil {
			a.toast("Could not save: " + err.Error())
			return
		}
		a.pushConfig()
		parent.Close()
		a.toast("Now writing in the " + style.Name + " style.")
	})
	row.Append(use)

	// The default is the fallback every character relies on, so it has no edit
	// or delete. It still reserves their width, or its card would stretch
	// wider than the rest and leave the column ragged.
	if style.Name == chars.DefaultStyleName {
		spacer := gtk.NewBox(gtk.OrientationVertical, 0)
		spacer.SetSizeRequest(34, 1)
		row.Append(spacer)
	}
	if style.Name != chars.DefaultStyleName {
		side := gtk.NewBox(gtk.OrientationVertical, 4)
		side.SetVAlign(gtk.AlignCenter)

		edit := gtk.NewButtonFromIconName(ui.IconEdit)
		edit.SetTooltipText("Edit " + style.Name)
		edit.AddCSSClass("flat")
		edit.ConnectClicked(func() {
			parent.Close()
			a.editStyle(style, false)
		})
		side.Append(edit)

		del := gtk.NewButtonFromIconName(ui.IconTrash)
		del.SetTooltipText("Delete " + style.Name)
		del.AddCSSClass("flat")
		del.ConnectClicked(func() {
			parent.Close()
			a.confirm("Delete the "+style.Name+" style?",
				"Scenes written in it are unaffected. If it is in use, Astral falls back to Default.",
				"Delete", func() {
					a.cfg.DeleteStyle(style.Name)
					if err := store.SaveConfig(a.cfg); err != nil {
						a.toast("Could not save: " + err.Error())
						return
					}
					a.pushConfig()
					a.showStyles()
				})
		})
		side.Append(del)
		row.Append(side)
	}
	return row
}

// editStyle opens the style editor. isNew only changes the dialog's title.
func (a *App) editStyle(st chars.WritingStyle, isNew bool) {
	d := adw.NewDialog()
	if isNew {
		d.SetTitle("New writing style")
	} else {
		d.SetTitle("Edit " + st.Name)
	}
	d.SetContentWidth(600)
	d.SetContentHeight(560)

	page := gtk.NewBox(gtk.OrientationVertical, 16)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)

	outer, card := groupCard("")
	nameEntry := gtk.NewEntry()
	nameEntry.SetText(st.Name)
	nameEntry.SetPlaceholderText("Sparse and cold")
	card.Append(labelledField("Name", "How it appears in the list. Two or three words.", nameEntry))

	body := st.Instructions
	if isNew && strings.TrimSpace(body) == "" {
		// Starting from the default rather than a blank box: editing something
		// that already works is a far easier way in than writing rules for a
		// model from nothing.
		body = chars.DefaultStyle().Instructions
	}
	frame, view := multilineField(body, 10)
	card.Append(labelledField("How the prose should sound",
		"One instruction per line, in the imperative, sentence length, paragraph count, tense, how much interiority, what to avoid.\n\n"+
			"Write {{char}} for whichever character is being played and {{user}} for you; a style applies to everyone, so it should not name anyone.\n\n"+
			"Do not mention asterisks or quotes: Astral handles formatting, and repeating it here only competes with it.",
		frame))
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
		switch {
		case name == "":
			a.toast("A style needs a name.")
			nameEntry.GrabFocus()
			return
		case name == chars.DefaultStyleName:
			a.toast("“Default” is the built-in style, pick another name.")
			nameEntry.GrabFocus()
			return
		case strings.TrimSpace(textOf(view)) == "":
			a.toast("A style needs some instructions.")
			return
		}
		// If the name changed, the old entry would otherwise be left behind.
		if !isNew && st.Name != name {
			a.cfg.DeleteStyle(st.Name)
		}
		a.cfg.SetStyle(chars.WritingStyle{Name: name, Instructions: textOf(view)})
		if err := store.SaveConfig(a.cfg); err != nil {
			a.toast("Could not save: " + err.Error())
			return
		}
		a.pushConfig()
		d.Close()
		a.toast("Now writing in the " + name + " style.")
	})
	header.PackEnd(save)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
	nameEntry.GrabFocus()
}

// newStyleDesignerChat opens a conversation whose product is a writing style.
func (a *App) newStyleDesignerChat() {
	a.startPlainChat(store.KindStyleDesigner, "Designing a writing style", chars.StyleDesignerOpening)
}

// buildStyleFromChat turns the open design conversation into a style, opening
// it in the editor for review rather than saving it outright.
func (a *App) buildStyleFromChat() {
	history := a.chat.History()
	if len(history) < 2 {
		a.toast("Talk it through a little first, then I can build the style.")
		return
	}
	model := a.cfg.Model
	if ch := a.chat.Chat(); ch.Model != "" {
		model = ch.Model
	}
	if model == "" {
		a.toast("Choose a model first.")
		return
	}

	a.chat.SetBuilding(true)
	client := a.client
	opts := ollama.Options{TopP: a.cfg.TopP, RepeatPenalty: a.cfg.RepeatPenalty, NumCtx: a.cfg.NumCtx}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		st, err := chars.BuildStyleFromConversation(ctx, client, model, history, opts)

		coreglib.IdleAdd(func() bool {
			a.chat.SetBuilding(false)
			if err != nil {
				a.toast("Could not build the style: " + friendlyBuildError(err))
				return false
			}
			a.editStyle(st, true)
			return false
		})
	}()
}

// pushConfig applies a settings change to the objects already running.
func (a *App) pushConfig() {
	if a.chat != nil {
		a.chat.SetConfig(a.cfg)
	}
	if a.sidebar != nil {
		a.sidebar.SetProfile(a.cfg.PersonaName, a.cfg.PersonaDescription)
	}
	a.refreshWelcome()
}
