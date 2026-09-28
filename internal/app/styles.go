package app

import (
	"context"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/ui"
)

// showStyles lists the writing styles and lets one be chosen, edited or
// removed. It mirrors the character list deliberately: the two are the same
// kind of thing, a named bundle of instructions, and there is no reason for
// them to be managed in two different shapes.
func (a *App) showStyles() {
	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle("Writing Styles")
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
		badge := gtk.NewLabel("In Use")
		badge.AddCSSClass("character-card-tag")
		head.Append(badge)
	}
	col.Append(head)

	desc := gtk.NewLabel(ui.Snippet(st.Resolved(), 260))
	desc.SetXAlign(0)
	desc.SetWrap(true)
	desc.SetLines(3)
	desc.SetEllipsize(pango.EllipsizeEnd)
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

		// The designer, pointed at a style that already exists.
		revise := gtk.NewButtonFromIconName(ui.IconDesigner)
		revise.SetTooltipText("Talk " + style.Name + " through with the designer")
		revise.AddCSSClass("flat")
		revise.ConnectClicked(func() {
			parent.Close()
			a.reviseStyle(style)
		})
		side.Append(revise)

		del := gtk.NewButtonFromIconName(ui.IconTrash)
		del.SetTooltipText("Delete " + style.Name)
		del.AddCSSClass("flat")
		del.ConnectClicked(func() {
			parent.Close()
			a.confirm("Delete the "+style.Name+" Style",
				"Scenes written in it are unaffected, and Default takes its place.",
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
	ui.FreeOnClose(d)
	if isNew {
		d.SetTitle("New Writing Style")
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
	card.Append(labelledField("Name", "Two or three words, shown in the list.", nameEntry))

	body := st.Instructions
	if isNew && strings.TrimSpace(body) == "" {
		// Starting from the default rather than a blank box: editing something
		// that already works is a far easier way in than writing rules for a
		// model from nothing.
		body = chars.DefaultStyle().Instructions
	}
	frame, view := multilineField(body, 10)
	card.Append(labelledField("Instructions",
		"One per line, using {{char}} and {{user}} rather than names.",
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
// reviseStyle opens a design conversation about a style that already exists.
//
// The style rides on the chat's note, which is the only field a designer chat has
// spare and is unused on one. A style is identified by its name and has no id, so
// there is nothing else to carry it by.
func (a *App) reviseStyle(st chars.WritingStyle) {
	if strings.TrimSpace(st.Name) == "" {
		a.toast("Save this style first, then the designer can revise it.")
		return
	}
	a.chat.Clear()
	a.chat.LoadChat(store.Chat{
		Model: a.cfg.Model,
		Kind:  store.KindStyleDesigner,
		Note:  st.Name,
		Title: "Revising " + st.Name,
	}, chars.Character{}, nil)
	a.chat.ShowGreeting(chars.ReviseStyleOpening(st))
	a.showChat()
	a.sidebar.Select(0)
	a.setTitle(store.Chat{Title: "Revising " + st.Name}, chars.Character{})
	a.chat.FocusComposer()
}

func (a *App) newStyleDesignerChat() {
	a.startPlainChat(store.KindStyleDesigner, "Designing a Writing Style", chars.StyleDesignerOpening)
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

	// A revision's build is shown the style as it stands; see
	// chars.ReviseStyleFromConversation.
	var existing chars.WritingStyle
	revising := false
	if was := strings.TrimSpace(a.chat.Chat().Note); was != "" {
		for _, s := range a.cfg.Styles() {
			if s.Name == was {
				existing, revising = s, true
			}
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		opts := fitBuild(ctx, client, model, store.KindStyleDesigner, opts, history)
		var st chars.WritingStyle
		var err error
		if revising {
			st, err = chars.ReviseStyleFromConversation(ctx, client, model, existing, history, opts)
		} else {
			st, err = chars.BuildStyleFromConversation(ctx, client, model, history, opts)
		}

		coreglib.IdleAdd(func() bool {
			a.chat.SetBuilding(false)
			if err != nil {
				a.toast("Could not build the style: " + friendlyBuildError(err))
				return false
			}
			// A revision keeps the name it started under unless the conversation
			// changed it, so saving replaces that style rather than adding a
			// second one beside it under a name one word different.
			isNew := true
			if was := strings.TrimSpace(a.chat.Chat().Note); was != "" {
				isNew = false
				if strings.TrimSpace(st.Name) == "" {
					st.Name = was
				}
				if st.Name != was {
					// Renamed in the conversation. The old one goes, or you are
					// left with both.
					a.cfg.DeleteStyle(was)
				}
			}
			a.editStyle(st, isNew)
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
		a.refreshProfile()
	}
	a.refreshWelcome()
}
