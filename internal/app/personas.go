package app

import (
	"context"
	"log"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
	"astral/internal/ui"
)

// Your personas: the people you play as.
//
// There used to be one of you, a name and a paragraph on the You page of
// Settings. Now there are as many as you like, each with an age, a gender, a
// race, an appearance and the rest in fields of their own, one of them in use
// by default, and every chat remembers which it was started as. The Persona
// Creator builds one with you the way the character designer builds a
// character.
//
// The one in use by default is also written into the settings as a name and a
// description, which is what everything that only asks "who am I" reads, the
// phone included; see store.Config.ActivePersona.

// migratePersonas makes the persona you had into the first of your personas,
// the first time this version runs, and keeps the settings' copy of the one
// in use by default up to date.
func (a *App) migratePersonas() {
	if a.store == nil {
		return
	}
	all, err := a.store.Personas()
	if err != nil {
		log.Printf("astral: reading your personas: %v", err)
		return
	}
	if len(all) == 0 {
		if a.cfg.PersonaName == "" && strings.TrimSpace(a.cfg.PersonaDescription) == "" {
			return
		}
		p := chars.Profile{Name: a.cfg.PersonaName, Details: strings.TrimSpace(a.cfg.PersonaDescription)}
		id, err := a.store.SavePersona(p)
		if err != nil {
			log.Printf("astral: keeping your persona: %v", err)
			return
		}
		// The scenes you have already played were played as this persona,
		// and go on being played as it whoever you use next.
		if err := a.store.AdoptOldChats(id); err != nil {
			log.Printf("astral: giving your chats your persona: %v", err)
		}
		a.cfg.ActivePersona = id
		a.savePersonaSetting()
		return
	}
	if _, err := a.store.Persona(a.cfg.ActivePersona); err != nil {
		a.cfg.ActivePersona = all[0].ID
	}
	a.mirrorActivePersona()
}

// mirrorActivePersona writes the persona in use by default into the settings.
func (a *App) mirrorActivePersona() {
	if a.cfg.ActivePersona == 0 {
		return
	}
	p, err := a.store.Persona(a.cfg.ActivePersona)
	if err != nil {
		return
	}
	a.cfg.PersonaName, a.cfg.PersonaDescription = p.Name, p.Description()
}

// savePersonaSetting mirrors the persona in use, saves the settings, and tells
// everything that shows who you are.
func (a *App) savePersonaSetting() {
	a.mirrorActivePersona()
	if err := store.SaveConfig(a.cfg); err != nil {
		a.toast("Could not save your settings: " + err.Error())
	}
	if a.chat != nil {
		a.chat.SetConfig(a.cfg)
	}
	a.refreshPersonaMenu()
	if a.sidebar != nil {
		a.refreshProfile()
	}
}

// refreshProfile shows the persona in use on the pill under the sidebar: its
// name, with its age, gender and race under it, or an invitation to make one
// when there is none.
func (a *App) refreshProfile() {
	if a.sidebar == nil {
		return
	}
	facts, picture := "", ""
	if a.store != nil && a.cfg.ActivePersona != 0 {
		if p, err := a.store.Persona(a.cfg.ActivePersona); err == nil {
			facts, picture = p.Facts(), p.AvatarPath
		}
	}
	a.sidebar.SetProfile(a.cfg.PersonaName, facts, picture)
}

// useByDefault makes a persona the one new chats are played as.
func (a *App) useByDefault(p chars.Profile) {
	a.cfg.ActivePersona = p.ID
	a.savePersonaSetting()
	a.refreshWelcome()
	a.toast("New chats are played as " + p.DisplayName() + ".")
}

// refreshPersonaMenu gives the sidebar's profile menu the list to switch
// between.
func (a *App) refreshPersonaMenu() {
	if a.sidebar == nil || a.store == nil {
		return
	}
	all, err := a.store.Personas()
	if err != nil {
		return
	}
	choices := make([]ui.PersonaChoice, 0, len(all))
	for _, p := range all {
		choices = append(choices, ui.PersonaChoice{ID: p.ID, Name: p.DisplayName(), Facts: p.Facts()})
	}
	a.sidebar.SetPersonas(choices, a.cfg.ActivePersona)
}

// usePersonaByID is the profile menu's switch.
func (a *App) usePersonaByID(id int64) {
	if p, err := a.store.Persona(id); err == nil {
		a.useByDefault(p)
	}
}

// showPersonas lists your personas.
func (a *App) showPersonas() {
	d := adw.NewDialog()
	d.SetTitle("Personas")
	d.SetContentWidth(560)
	d.SetContentHeight(640)

	header := adw.NewHeaderBar()
	designBtn := gtk.NewButtonFromIconName(ui.IconDesigner)
	designBtn.SetTooltipText("Create a persona with the Persona Creator")
	designBtn.ConnectClicked(func() {
		d.Close()
		a.newPersonaDesignerChat()
	})
	header.PackStart(designBtn)
	newBtn := gtk.NewButtonFromIconName(ui.IconAdd)
	newBtn.SetTooltipText("Write a persona yourself")
	newBtn.ConnectClicked(func() {
		d.Close()
		a.editPersona(chars.Profile{})
	})
	header.PackEnd(newBtn)

	page := gtk.NewBox(gtk.OrientationVertical, 8)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)
	about := wrappingLabel("The people you play as in your chats.")
	about.AddCSSClass("settings-hint")
	page.Append(about)

	all, err := a.store.Personas()
	if err != nil {
		page.Append(wrappingLabel("Could not read them: " + err.Error()))
	}
	for _, p := range all {
		page.Append(a.personaCard(p, d))
	}
	page.Append(addRow("Create a Persona with the Persona Creator", func() {
		d.Close()
		a.newPersonaDesignerChat()
	}))
	page.Append(addRow("Write a Persona Yourself", func() {
		d.Close()
		a.editPersona(chars.Profile{})
	}))

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// personaCard is one persona in the list.
func (a *App) personaCard(p chars.Profile, parent *adw.Dialog) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("character-card")
	col := gtk.NewBox(gtk.OrientationVertical, 3)
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	head.Append(ui.NewPersonaAvatar(p, 26))
	name := gtk.NewLabel(p.DisplayName())
	name.SetXAlign(0)
	name.SetHExpand(true)
	name.AddCSSClass("character-card-name")
	head.Append(name)
	if p.ID == a.cfg.ActivePersona {
		tag := gtk.NewLabel("In Use")
		tag.AddCSSClass("character-card-tag")
		tag.SetTooltipText("New chats are played as " + p.DisplayName())
		head.Append(tag)
	}
	col.Append(head)
	if f := p.Facts(); f != "" {
		col.Append(cardDescription(ui.Snippet(f, 200)))
	}
	btn.SetChild(col)
	btn.SetTooltipText("Open " + p.DisplayName())
	btn.ConnectClicked(func() {
		parent.Close()
		a.editPersona(p)
	})
	return btn
}

// editPersona opens a persona to change, or a new one when its id is zero.
func (a *App) editPersona(p chars.Profile) {
	d := adw.NewDialog()
	title := "New Persona"
	if p.ID != 0 || p.Name != "" {
		title = p.DisplayName()
	}
	d.SetTitle(title)
	d.SetContentWidth(620)
	d.SetContentHeight(760)

	entry := func(text, placeholder string) *gtk.Entry {
		e := gtk.NewEntry()
		e.SetText(text)
		e.SetPlaceholderText(placeholder)
		return e
	}
	name := entry(p.Name, "What the characters call you")
	age := entry(p.Age, "27, or late thirties")
	gender := entry(p.Gender, "Woman, man…")
	race := entry(p.Race, "Human, elf…")
	appearFrame, appear := multilineField(p.Appearance, 3)
	personFrame, person := multilineField(p.Personality, 2)
	backFrame, back := multilineField(p.Background, 2)
	detailFrame, detail := multilineField(p.Details, 3)

	read := func() chars.Profile {
		q := p
		q.Name = strings.TrimSpace(name.Text())
		q.Age = strings.TrimSpace(age.Text())
		q.Gender = strings.TrimSpace(gender.Text())
		q.Race = strings.TrimSpace(race.Text())
		q.Appearance = textOf(appear)
		q.Personality = textOf(person)
		q.Background = textOf(back)
		q.Details = textOf(detail)
		return q
	}
	header := saveHeader(d, "Save this persona", func() bool {
		q := read()
		if q.Name == "" {
			a.toast("Give this persona a name first.")
			return false
		}
		id, err := a.store.SavePersona(q)
		if err != nil {
			a.toast("Could not save the persona: " + err.Error())
			return false
		}
		q.ID = id
		switch {
		case a.cfg.ActivePersona == 0 || a.cfg.ActivePersona == id:
			// The first persona is the one in use, and an edit to the one in
			// use changes who new chats are played as straight away.
			a.cfg.ActivePersona = id
			a.savePersonaSetting()
			a.toast(q.DisplayName() + " is saved.")
		case p.ID == 0:
			a.refreshPersonaMenu()
			a.toastAction(q.DisplayName()+" is saved.", "Use by Default", func() { a.useByDefault(q) })
		default:
			a.refreshPersonaMenu()
			a.toast(q.DisplayName() + " is saved.")
		}
		return true
	})

	page := gtk.NewBox(gtk.OrientationVertical, 14)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)
	hint := wrappingLabel("What the characters see and know about you.")
	hint.AddCSSClass("settings-hint")
	page.Append(hint)

	page.Append(labelledField("Name", "Characters call you this, and it replaces {{user}} in their cards.", name))
	page.Append(a.imageField("Picture", "Shown beside your messages and in the sidebar.",
		"persona",
		func() string { return p.AvatarPath },
		func(path string) { p.AvatarPath = path }))
	facts := gtk.NewBox(gtk.OrientationHorizontal, 10)
	facts.SetHomogeneous(true)
	facts.Append(labelledField("Age", "", age))
	facts.Append(labelledField("Gender", "", gender))
	facts.Append(labelledField("Race", "", race))
	page.Append(facts)
	page.Append(labelledField("Appearance", "What someone sees at a glance: build, face, hair, clothes.", appearFrame))
	page.Append(labelledField("Personality", "How you come across to other people.", personFrame))
	page.Append(labelledField("Background", "What people in the story would already know about you.", backFrame))
	page.Append(labelledField("Other Details", "Anything else, in your own words.", detailFrame))

	if p.ID != 0 {
		buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
		if p.ID != a.cfg.ActivePersona {
			use := gtk.NewButtonWithLabel("Use by Default")
			use.SetTooltipText("Play new chats as " + p.DisplayName())
			use.ConnectClicked(func() {
				d.Close()
				a.useByDefault(p)
			})
			buttons.Append(use)
		}
		del := gtk.NewButtonWithLabel("Delete")
		del.AddCSSClass("destructive-action")
		del.ConnectClicked(func() {
			a.confirm("Delete "+p.DisplayName(),
				"Chats played as "+p.DisplayName()+" are kept.",
				"Delete", func() {
					if err := a.store.DeletePersona(p.ID); err != nil {
						a.toast("Could not delete: " + err.Error())
						return
					}
					if a.cfg.ActivePersona == p.ID {
						a.cfg.ActivePersona = 0
						if rest, err := a.store.Personas(); err == nil && len(rest) > 0 {
							a.cfg.ActivePersona = rest[0].ID
						} else {
							a.cfg.PersonaName, a.cfg.PersonaDescription = "", ""
						}
					}
					a.savePersonaSetting()
					d.Close()
					a.toast(p.DisplayName() + " is deleted.")
				})
		})
		buttons.Append(del)
		page.Append(buttons)
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// showPersonaPicker chooses who you are in the open chat.
func (a *App) showPersonaPicker() {
	all, err := a.store.Personas()
	if err != nil {
		a.toast("Could not read your personas: " + err.Error())
		return
	}
	if len(all) == 0 {
		a.toast("You have no personas yet.")
		a.showPersonas()
		return
	}
	current := a.chat.Chat().PersonaID
	if current == 0 {
		current = a.cfg.ActivePersona
	}
	d := adw.NewAlertDialog("Choose Your Persona", "Characters see your choice from the next turn.")
	list := gtk.NewBox(gtk.OrientationVertical, 4)
	var group *gtk.CheckButton
	picked := current
	for _, p := range all {
		p := p
		radio := gtk.NewCheckButton()
		label := p.DisplayName()
		if f := p.Facts(); f != "" {
			label += " (" + ui.Snippet(f, 60) + ")"
		}
		radio.SetChild(wrappingLabel(label))
		if group == nil {
			group = radio
		} else {
			radio.SetGroup(group)
		}
		radio.SetActive(p.ID == current)
		radio.ConnectToggled(func() {
			if radio.Active() {
				picked = p.ID
			}
		})
		list.Append(radio)
	}
	d.SetExtraChild(scrolled(list))
	d.AddResponse("manage", "Personas…")
	d.AddResponse("cancel", "Cancel")
	d.AddResponse("ok", "Play as Them")
	d.SetResponseAppearance("ok", adw.ResponseSuggested)
	d.SetDefaultResponse("ok")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		switch response {
		case "manage":
			a.showPersonas()
		case "ok":
			p, err := a.store.Persona(picked)
			if err != nil {
				return
			}
			a.chat.PlayAs(p)
			// The rows already on screen carry the old name; drawing the chat
			// again relabels them.
			if id := a.chat.Chat().ID; id != 0 {
				if err := a.openChat(id); err != nil {
					a.toast("Could not reopen the chat: " + err.Error())
				}
			}
		}
	})
	d.Present(a.win)
}

// newPersonaDesignerChat opens the Persona Creator.
func (a *App) newPersonaDesignerChat() {
	a.startPlainChat(store.KindPersonaDesigner, "Creating a Persona", chars.PersonaDesignerOpening)
}

// buildPersonaFromChat turns the Persona Creator conversation into a persona,
// opened in the editor to look over before it is saved.
func (a *App) buildPersonaFromChat() {
	history := a.chat.History()
	if len(history) < 2 {
		a.toast("Talk it through a little first, then I can build the persona.")
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
		opts := fitBuild(ctx, client, model, store.KindPersonaDesigner, opts, history)
		p, err := chars.BuildPersonaFromConversation(ctx, client, model, history, opts)
		coreglib.IdleAdd(func() bool {
			a.chat.SetBuilding(false)
			if err != nil {
				a.toast("Could not build the persona: " + friendlyBuildError(err))
				return false
			}
			a.editPersona(p)
			return false
		})
	}()
}

// fitBuild gives a build the window its conversation needs, with room for the
// designer's prompt and the build instruction, which are sent around it.
func fitBuild(ctx context.Context, client *ollama.Client, model, kind string, opts ollama.Options, history []ollama.Message) ollama.Options {
	msgs := append([]ollama.Message{{Role: ollama.RoleSystem, Content: strings.Repeat(" ", 8000)}}, history...)
	sizing := opts
	sizing.NumPredict = max(opts.NumPredict, 2048) // a build writes a whole card
	opts.NumCtx = scene.FitContext(ctx, client, model, kind, sizing, msgs).NumCtx
	return opts
}
