package app

import (
	"context"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
	"astral/internal/ui"
)

// buildTimeout bounds the extraction call. It is generous because a large
// model writing a full card is legitimately slow, and the work is wasted if it
// is cut off halfway.
const buildTimeout = 6 * time.Minute

// showNewChat asks what kind of conversation to start.
//
// Before this existed the only way in was to pick a character, which meant a
// fresh install with no cast had no way to talk to the model at all, and no
// way to get help making the character it was asking for.
func (a *App) showNewChat() {
	d := adw.NewDialog()
	d.SetTitle("New Chat")
	d.SetContentWidth(420)

	page := gtk.NewBox(gtk.OrientationVertical, 4)
	page.SetMarginTop(12)
	page.SetMarginBottom(12)
	page.SetMarginStart(12)
	page.SetMarginEnd(12)

	// Icon, label, and a short line only where it earns one.
	//
	// This was five stacked cards of title-plus-paragraph, which read as a
	// form to be studied rather than a menu to be picked from. Most of those
	// paragraphs were explaining labels that should not have needed
	// explaining: "Just chat" had a line telling you it meant a plain
	// conversation, which is what a clearer label says by itself.
	add := func(icon, title, note string, primary bool, onClick func()) {
		btn := gtk.NewButton()
		btn.AddCSSClass("launch-row")
		if primary {
			btn.AddCSSClass("launch-row-primary")
		}

		row := gtk.NewBox(gtk.OrientationHorizontal, 12)
		img := gtk.NewImageFromIconName(icon)
		img.SetPixelSize(18)
		img.SetVAlign(gtk.AlignCenter)
		row.Append(img)

		col := gtk.NewBox(gtk.OrientationVertical, 1)
		col.SetHExpand(true)
		col.SetVAlign(gtk.AlignCenter)
		t := gtk.NewLabel(title)
		t.SetXAlign(0)
		t.AddCSSClass("launch-row-title")
		col.Append(t)
		if note != "" {
			n := gtk.NewLabel(note)
			n.SetXAlign(0)
			n.AddCSSClass("launch-row-note")
			col.Append(n)
		}
		row.Append(col)

		btn.SetChild(row)
		btn.ConnectClicked(func() {
			d.Close()
			onClick()
		})
		page.Append(btn)
	}

	heading := func(text string) {
		l := gtk.NewLabel(text)
		l.SetXAlign(0)
		l.AddCSSClass("launch-heading")
		page.Append(l)
	}

	// Playing comes first, and looks like it: it is what this dialog is most
	// often opened to do.
	// With no cast there is nothing to play, so the plain conversation takes
	// the emphasis instead of offering a route that leads nowhere.
	cast, _ := a.store.CountCharacters()
	worlds, _ := a.store.Worlds()
	if cast > 0 {
		add(ui.IconCharacters, "Play a Scene", "", true, a.showCharacters)
	}
	// A group needs two people to put in a room, so it appears when there are
	// two to pick from and not before.
	if cast > 1 {
		add(ui.IconCharacters, "Play with a Group", "", false, a.showCastPicker)
	}
	// A world is a place, so it is somewhere to go rather than someone to
	// meet. It belongs next to the cast and not buried in the worlds list,
	// which is where it was: unreachable without first inventing a character
	// to be met there.
	if len(worlds) > 0 {
		add(ui.IconWorlds, "Play in a World", "", cast == 0, a.showWorldPicker)
	}
	add(ui.IconChat, "General Chat", "", cast == 0 && len(worlds) == 0, a.newAssistantChat)

	heading("Create")
	// Labels only: each says what it makes, and a line under it saying the
	// model interviews you was the same line three times.
	add(ui.IconDesigner, "New Character", "", false, a.newDesignerChat)
	add(ui.IconHome, "New Persona", "", false, a.newPersonaDesignerChat)
	add(ui.IconEdit, "New Writing Style", "", false, a.newStyleDesignerChat)
	add(ui.IconWorlds, "New World", "", false, a.newWorldDesignerChat)
	add(ui.IconFolder, "Import a Character", "from a .png or .json card", false, a.actionImportCharacter)

	tv := adw.NewToolbarView()
	tv.AddTopBar(adw.NewHeaderBar())
	tv.SetContent(page)
	d.SetChild(tv)
	d.Present(a.win)
}

// newDesignerChat opens a conversation whose product is a character.
func (a *App) newDesignerChat() {
	a.startPlainChat(store.KindDesigner, "Designing a Character", chars.DesignerOpening)
}

// reviseCharacter opens a designer conversation about somebody who already
// exists.
//
// The character rides on the chat, which is what makes it a revision: the prompt
// gets their card as the starting point, the chip says Save rather than Create,
// and what comes out keeps their row so every scene they are in carries on with
// them in it.
//
// This is the answer to changing the designer's instructions and wanting the
// characters written before that brought up to match, which otherwise meant
// deleting them and losing their scenes.
func (a *App) reviseCharacter(ca chars.Character) {
	if ca.ID == 0 {
		a.toast("Save this character first, then the designer can revise them.")
		return
	}
	a.chat.Clear()
	a.chat.LoadChat(store.Chat{
		Model:       a.cfg.Model,
		Kind:        store.KindDesigner,
		CharacterID: ca.ID,
		Title:       "Revising " + ca.Name,
	}, ca, nil)
	a.chat.ShowGreeting(chars.ReviseOpening(ca))
	a.showChat()
	a.sidebar.Select(0)
	a.setTitle(store.Chat{Title: "Revising " + ca.Name}, chars.Character{})
	a.chat.FocusComposer()
	a.refreshAttachAvailability()
}

// newAssistantChat opens a plain conversation with the model.
func (a *App) newAssistantChat() {
	a.startPlainChat(store.KindAssistant, "Chat", "")
}

// startPlainChat opens an unsaved chat of the given kind. Nothing is written
// until the first message, so opening one and changing your mind leaves
// nothing behind.
func (a *App) startPlainChat(kind, title, opening string) {
	a.chat.Clear()
	a.chat.LoadChat(store.Chat{
		Model: a.cfg.Model,
		Kind:  kind,
		Title: title,
	}, chars.Character{}, nil)
	if opening != "" {
		a.chat.ShowGreeting(opening)
	}
	a.showChat()
	a.sidebar.Select(0)
	a.setTitle(store.Chat{Title: title}, chars.Character{})
	a.chat.FocusComposer()
	a.refreshAttachAvailability()
}

// refreshAttachAvailability decides whether the attach control is offered.
//
// A design chat and a plain chat take pictures; a scene does not, since a
// character has no way to be shown one. Whether the chat's model can see does
// not decide it any more: when it cannot, another model that can looks at the
// picture on its behalf (see scene.Seer), so the only chat that refuses is one
// on a machine where nothing can see at all. Asking takes a few small calls and
// is done off the UI thread.
func (a *App) refreshAttachAvailability() {
	if a.chat == nil {
		return
	}
	kind := a.chat.Chat().Kind
	if kind != store.KindDesigner && kind != store.KindAssistant {
		a.chat.SetCanAttachImages(false)
		return
	}
	model := a.cfg.Model
	if ch := a.chat.Chat(); ch.Model != "" {
		model = ch.Model
	}
	if model == "" {
		a.chat.SetCanAttachImages(false)
		return
	}
	client, chosen := a.client, a.cfg.VisionModel
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		can := scene.CanTakePictures(ctx, client, chosen, model)
		coreglib.IdleAdd(func() bool {
			if a.chat.Chat().Kind == kind {
				a.chat.SetCanAttachImages(can)
			}
			return false
		})
	}()
}

// buildCharacterFromChat turns the open design conversation into a character.
//
// The result opens in the editor rather than being saved straight away: the
// model is guessing at fields the user has only talked around, and reviewing
// it before it joins the cast is both safer and usually an improvement.
func (a *App) buildCharacterFromChat() {
	history := a.chat.History()
	if len(history) < 2 {
		a.toast("Talk it through a little first, then I can build the character.")
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
	opts := ollama.Options{
		TopP:          a.cfg.TopP,
		RepeatPenalty: a.cfg.RepeatPenalty,
		NumCtx:        a.cfg.NumCtx,
	}

	// A revision's build is shown the card as it stands, so what the
	// conversation did not touch is kept rather than written fresh; see
	// chars.ReviseFromConversation.
	existing, revising := a.revising()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		opts := fitBuild(ctx, client, model, store.KindDesigner, opts, history)
		build := chars.BuildFromConversation
		if revising {
			build = func(ctx context.Context, client *ollama.Client, model string, history []ollama.Message, opts ollama.Options) (chars.Character, error) {
				return chars.ReviseFromConversation(ctx, client, model, existing, history, opts)
			}
		}
		c, err := build(ctx, client, model, history, opts)

		coreglib.IdleAdd(func() bool {
			a.chat.SetBuilding(false)
			if err != nil {
				a.toast("Could not build the character: " + friendlyBuildError(err))
				return false
			}
			// A revision keeps the character it came from: the same row, the
			// same pictures, the same world, so every scene they are in carries
			// on with them in it. Anything the conversation did not cover is
			// kept as it was rather than blanked by a schema that has no field
			// for it.
			if existing, ok := a.revising(); ok {
				c = chars.Revise(existing, c)
			} else {
				c.Accent = ui.AccentFor(c.Name)
			}
			// A picture used as reference during the design is almost
			// certainly the picture of this character, so it is offered as the
			// portrait. Still editable before saving.
			if img := a.chat.LastImage(); img != "" {
				c.PortraitPath = img
				c.AvatarPath = img
			}
			// Opened for review, and on save it offers to play the scene it
			// was just designed for, which is the whole point of having made
			// it.
			if _, revising := a.revising(); revising {
				a.editCharacterWith(c, func(saved chars.Character) {
					a.toast(saved.Name + " is saved, and their scenes carry on with the new card.")
				})
				return false
			}
			a.editCharacterWith(c, func(saved chars.Character) {
				a.confirmStartScene(saved)
			})
			return false
		})
	}()
}

// revising is the character the open designer chat is about, when it is about
// one. A designer chat with no character is inventing a new one.
func (a *App) revising() (chars.Character, bool) {
	ch := a.chat.Chat()
	if ch.Kind != store.KindDesigner || ch.CharacterID == 0 {
		return chars.Character{}, false
	}
	ca, err := a.store.Character(ch.CharacterID)
	if err != nil {
		// Deleted while the conversation was open. What was written is still
		// worth keeping, so it becomes a new character rather than being lost.
		return chars.Character{}, false
	}
	return ca, true
}

// confirmStartScene offers to open a scene with a freshly designed character.
func (a *App) confirmStartScene(c chars.Character) {
	d := adw.NewAlertDialog(c.Name+" Is Ready", "You can start a scene with them now.")
	d.AddResponse("later", "Not Yet")
	d.AddResponse("play", "Start the Scene")
	d.SetResponseAppearance("play", adw.ResponseSuggested)
	d.SetDefaultResponse("play")
	d.SetCloseResponse("later")
	d.ConnectResponse(func(response string) {
		if response == "play" {
			a.newChat(c)
		}
	})
	d.Present(a.win)
}

// friendlyBuildError explains the failures this step actually hits.
func friendlyBuildError(err error) string {
	s := err.Error()
	switch {
	case contains(s, "context deadline exceeded"):
		return "the model took too long, and a smaller one would be quicker."
	case contains(s, "cannot reach Ollama"):
		return "Ollama stopped responding, so check it is still running."
	case contains(s, "format"), contains(s, "schema"):
		// Structured output needs a recent Ollama; an old one rejects the
		// schema outright, which is worth saying rather than blaming the model.
		return "this model or Ollama doesn't support structured output, so try updating Ollama."
	default:
		return s
	}
}

func contains(s, sub string) bool { return strings.Contains(strings.ToLower(s), sub) }
