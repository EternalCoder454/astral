package app

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/store"
	"astral/internal/ui"
)

// A character's own page.
//
// Worlds got one and characters did not, which was the wrong way round: a world
// is a setting and a character is the thing you actually play. Everything about
// one was spread across a row in a list, a form, and whatever scenes happened to
// be in the sidebar, and there was no screen that answered "who is this, and what
// have I played with them".

// showCharacter is that screen.
func (a *App) showCharacter(c chars.Character) {
	d := adw.NewDialog()
	d.SetTitle(c.Name)
	d.SetContentWidth(620)
	d.SetContentHeight(720)

	header := adw.NewHeaderBar()
	back := gtk.NewButtonFromIconName(ui.IconPanelLeft)
	back.SetTooltipText("All characters")
	back.ConnectClicked(func() {
		d.Close()
		a.showCharacters()
	})
	header.PackStart(back)

	edit := gtk.NewButtonFromIconName(ui.IconEdit)
	edit.SetTooltipText("Edit " + c.Name)
	edit.ConnectClicked(func() {
		d.Close()
		a.editCharacter(c)
	})
	header.PackEnd(edit)

	revise := gtk.NewButtonFromIconName(ui.IconDesigner)
	revise.SetTooltipText("Talk " + c.Name + " through with the designer")
	revise.ConnectClicked(func() {
		d.Close()
		a.reviseCharacter(c)
	})
	header.PackEnd(revise)

	page := gtk.NewBox(gtk.OrientationVertical, 8)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)

	// Who they are, at the top, with the face they are shown with everywhere
	// else so the page is recognisably about the same person.
	head := gtk.NewBox(gtk.OrientationHorizontal, 14)
	head.Append(ui.NewCharacterAvatar(c, 64))
	col := gtk.NewBox(gtk.OrientationVertical, 4)
	col.SetHExpand(true)
	col.SetVAlign(gtk.AlignCenter)

	name := gtk.NewLabel(c.Name)
	name.SetXAlign(0)
	name.AddCSSClass("character-page-name")
	col.Append(name)

	if c.WorldID != 0 {
		if w, err := a.store.World(c.WorldID); err == nil {
			where := gtk.NewButtonWithLabel(w.Name)
			where.AddCSSClass("character-card-tag")
			where.SetHAlign(gtk.AlignStart)
			where.SetTooltipText("Scenes with " + c.Name + " are set in " + w.Name)
			where.ConnectClicked(func() {
				d.Close()
				a.showWorld(w)
			})
			col.Append(where)
		}
	}
	head.Append(col)

	play := gtk.NewButtonWithLabel("Play a Scene")
	play.AddCSSClass("suggested-action")
	play.SetVAlign(gtk.AlignCenter)
	play.ConnectClicked(func() {
		d.Close()
		a.newChat(c)
	})
	head.Append(play)
	page.Append(head)

	if s := strings.TrimSpace(c.SummaryFor(a.cfg.PersonaName)); s != "" {
		about := gtk.NewLabel(s)
		about.SetXAlign(0)
		about.SetWrap(true)
		about.AddCSSClass("settings-hint")
		about.SetMarginBottom(4)
		page.Append(about)
	}

	page.Append(a.relationsCard(c, d))
	page.Append(a.scenesCard(c, d))

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// scenesCard lists what has been played with this character.
//
// Both kinds: scenes that are theirs, and group scenes they are one of. A list
// that showed only the first would miss every group they are in, which for
// somebody mostly played in groups is all of them.
func (a *App) scenesCard(c chars.Character, parent *adw.Dialog) *gtk.Box {
	outer, card := groupCard("Scenes")
	chats, err := a.store.ChatsWith(c.ID)
	if err != nil {
		card.Append(wrappingLabel("Could not read them: " + err.Error()))
		return outer
	}
	if len(chats) == 0 {
		none := wrappingLabel("Nothing played yet.")
		none.AddCSSClass("settings-hint")
		card.Append(none)
		return outer
	}
	for i, ch := range chats {
		if i == 12 {
			// A long list here is a worse sidebar. Twelve is enough to find the
			// one you meant and few enough to read.
			more := wrappingLabel(fmt.Sprintf("and %d more, in the sidebar.", len(chats)-i))
			more.AddCSSClass("settings-hint")
			card.Append(more)
			break
		}
		card.Append(a.sceneRow(ch, parent))
	}
	return outer
}

// sceneRow is one conversation, which opens it.
func (a *App) sceneRow(ch store.Chat, parent *adw.Dialog) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("character-card")

	row := gtk.NewBox(gtk.OrientationHorizontal, 10)
	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.SetHExpand(true)

	title := ch.Title
	if title == "" {
		title = "New Chat"
	}
	t := gtk.NewLabel(title)
	t.SetXAlign(0)
	t.AddCSSClass("character-card-name")
	col.Append(t)

	var note []string
	if ch.MessageCount > 0 {
		note = append(note, fmt.Sprintf("%d messages", ch.MessageCount))
	}
	if ch.CastSize > 1 {
		note = append(note, fmt.Sprintf("%d characters", ch.CastSize))
	}
	if !ch.UpdatedAt.IsZero() {
		note = append(note, agoText(ch.UpdatedAt))
	}
	if len(note) > 0 {
		n := gtk.NewLabel(strings.Join(note, " · "))
		n.SetXAlign(0)
		n.AddCSSClass("settings-hint")
		col.Append(n)
	}
	row.Append(col)
	btn.SetChild(row)

	id := ch.ID
	btn.ConnectClicked(func() {
		parent.Close()
		if err := a.openChat(id); err != nil {
			a.toast("Could not open it: " + err.Error())
		}
	})
	return btn
}

// relationsCard is how this character knows the others.
//
// It reaches the prompt only in a scene where both are present, which is what
// makes it worth having at all: a group scene otherwise puts five people in a
// room who have never met, and the model invents whatever history it needs and
// forgets it by the next turn.
func (a *App) relationsCard(c chars.Character, parent *adw.Dialog) *gtk.Box {
	outer, card := groupCard("Relationships")

	hint := wrappingLabel("One line per pair, used only when both are in a scene.")
	hint.AddCSSClass("settings-hint")
	card.Append(hint)

	everyone, err := a.store.Characters()
	if err != nil {
		card.Append(wrappingLabel("Could not read your characters: " + err.Error()))
		return outer
	}
	if len(everyone) < 2 {
		none := wrappingLabel("Nobody else to know yet.")
		none.AddCSSClass("settings-hint")
		card.Append(none)
		return outer
	}

	existing := map[int64]string{}
	if rels, err := a.store.Relations(c.ID); err == nil {
		for _, r := range rels {
			existing[r.Other.ID] = r.Note
		}
	}

	// Everyone who already has a relation, then a way to add one. The ones with
	// nothing recorded are not listed: a cast of thirty would be thirty empty
	// boxes, and the ones that matter are the few somebody has written.
	shown := 0
	for _, other := range everyone {
		if other.ID == c.ID {
			continue
		}
		note, ok := existing[other.ID]
		if !ok {
			continue
		}
		card.Append(a.relationRow(c, other, note))
		shown++
	}
	if shown == 0 {
		none := wrappingLabel("Nothing recorded.")
		none.AddCSSClass("settings-hint")
		card.Append(none)
	}

	add := gtk.NewButtonWithLabel("Add Someone…")
	add.SetHAlign(gtk.AlignStart)
	add.ConnectClicked(func() {
		a.pickRelation(c, everyone, existing, func() {
			parent.Close()
			a.showCharacter(c)
		})
	})
	card.Append(add)
	return outer
}

// relationRow is one recorded relation, editable where it sits.
func (a *App) relationRow(c, other chars.Character, note string) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)

	who := gtk.NewLabel(other.Name)
	who.SetXAlign(0)
	who.SetVAlign(gtk.AlignStart)
	who.SetMarginTop(6)
	who.SetSizeRequest(130, -1)
	who.SetEllipsize(3) // PANGO_ELLIPSIZE_END
	who.AddCSSClass("field-label")
	row.Append(who)

	frame, view := multilineField(note, 1)
	frame.SetHExpand(true)
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(func() {
		next := strings.TrimSpace(textOf(view))
		if next == note {
			return
		}
		if err := a.store.SetRelation(c.ID, other.ID, next); err != nil {
			a.toast("Could not save that: " + err.Error())
			return
		}
		note = next
	})
	view.AddController(focus)
	row.Append(frame)
	return row
}

// pickRelation chooses somebody to record a relation with.
func (a *App) pickRelation(c chars.Character, everyone []chars.Character, existing map[int64]string, done func()) {
	d := adw.NewDialog()
	d.SetTitle("Add a Relationship")
	d.SetContentWidth(460)

	list := gtk.NewBox(gtk.OrientationVertical, 6)
	list.SetMarginTop(12)
	list.SetMarginBottom(12)
	list.SetMarginStart(12)
	list.SetMarginEnd(12)

	rows := make([]filterRow, 0, len(everyone))
	for _, other := range everyone {
		if other.ID == c.ID {
			continue
		}
		if _, already := existing[other.ID]; already {
			continue
		}
		other := other
		btn := gtk.NewButton()
		btn.AddCSSClass("character-card")
		inner := gtk.NewBox(gtk.OrientationHorizontal, 10)
		inner.Append(ui.NewCharacterAvatar(other, 28))
		l := gtk.NewLabel(other.Name)
		l.SetXAlign(0)
		l.SetHExpand(true)
		inner.Append(l)
		btn.SetChild(inner)
		btn.ConnectClicked(func() {
			d.Close()
			// Saved with a placeholder so the row exists to be typed into. An
			// empty note deletes the relation, so leaving it alone undoes this.
			if err := a.store.SetRelation(c.ID, other.ID, "They have met."); err != nil {
				a.toast("Could not save that: " + err.Error())
				return
			}
			done()
		})
		rows = append(rows, filterRow{
			Widget: btn,
			Text:   strings.ToLower(other.Name + " " + other.SummaryFor(a.cfg.PersonaName)),
		})
	}
	if len(rows) == 0 {
		none := wrappingLabel("Everybody else already has a line.")
		none.AddCSSClass("settings-hint")
		list.Append(none)
	}
	searchableList(list, "Search by name", rows)

	tv := adw.NewToolbarView()
	tv.AddTopBar(adw.NewHeaderBar())
	tv.SetContent(scrolledToFit(list))
	d.SetChild(tv)
	d.Present(a.win)
}
