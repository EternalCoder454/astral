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
// fresh install with no cast had no way to talk to the model at all — and no
// way to get help making the character it was asking for.
func (a *App) showNewChat() {
	d := adw.NewDialog()
	d.SetTitle("New chat")
	d.SetContentWidth(460)

	page := gtk.NewBox(gtk.OrientationVertical, 10)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)

	add := func(title, subtitle string, onClick func()) {
		btn := gtk.NewButton()
		btn.AddCSSClass("character-card")
		box := gtk.NewBox(gtk.OrientationVertical, 3)
		t := gtk.NewLabel(title)
		t.SetXAlign(0)
		t.AddCSSClass("character-card-name")
		box.Append(t)
		sub := gtk.NewLabel(subtitle)
		sub.SetXAlign(0)
		sub.SetWrap(true)
		sub.AddCSSClass("character-card-desc")
		box.Append(sub)
		btn.SetChild(box)
		btn.ConnectClicked(func() {
			d.Close()
			onClick()
		})
		page.Append(btn)
	}

	add("Design a character",
		"Describe what you want and the model interviews you, then writes the character for you.",
		a.newDesignerChat)

	n, _ := a.store.CountCharacters()
	if n > 0 {
		add("Play a scene",
			"Pick someone from your cast and start roleplaying.",
			a.showCharacters)
	}

	add("Just chat",
		"A plain conversation with the model. No character, no roleplay.",
		a.newAssistantChat)

	add("Design a writing style",
		"Change how the prose sounds — sparse, overwritten, screenplay-terse. The model interviews you the same way.",
		a.newStyleDesignerChat)

	add("Import a character card",
		"Load a .png or .json card you already have.",
		a.actionImportCharacter)

	tv := adw.NewToolbarView()
	tv.AddTopBar(adw.NewHeaderBar())
	tv.SetContent(page)
	d.SetChild(tv)
	d.Present(a.win)
}

// newDesignerChat opens a conversation whose product is a character.
func (a *App) newDesignerChat() {
	a.startPlainChat(store.KindDesigner, "Designing a character", chars.DesignerOpening)
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
}

// buildCharacterFromChat turns the open design conversation into a character.
//
// The result opens in the editor rather than being saved straight away: the
// model is guessing at fields the user has only talked around, and reviewing
// it before it joins the cast is both safer and usually an improvement.
func (a *App) buildCharacterFromChat() {
	history := a.chat.History()
	if len(history) < 2 {
		a.toast("Talk it through a little first — then I can build the character.")
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

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		c, err := chars.BuildFromConversation(ctx, client, model, history, opts)

		coreglib.IdleAdd(func() bool {
			a.chat.SetBuilding(false)
			if err != nil {
				a.toast("Could not build the character: " + friendlyBuildError(err))
				return false
			}
			c.Accent = ui.AccentFor(c.Name)
			// Opened for review, and on save it offers to play the scene it
			// was just designed for — which is the whole point of having made
			// it.
			a.editCharacterWith(c, func(saved chars.Character) {
				a.confirmStartScene(saved)
			})
			return false
		})
	}()
}

// confirmStartScene offers to open a scene with a freshly designed character.
func (a *App) confirmStartScene(c chars.Character) {
	d := adw.NewAlertDialog(c.Name+" is ready", "Start a scene with them now?")
	d.AddResponse("later", "Not yet")
	d.AddResponse("play", "Start the scene")
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
		return "the model took too long. A smaller model will be much quicker at this."
	case contains(s, "cannot reach Ollama"):
		return "Ollama stopped responding. Check it is still running."
	case contains(s, "format"), contains(s, "schema"):
		// Structured output needs a recent Ollama; an old one rejects the
		// schema outright, which is worth saying rather than blaming the model.
		return "this model or your version of Ollama doesn't support structured output. Updating Ollama usually fixes it."
	default:
		return s
	}
}

func contains(s, sub string) bool { return strings.Contains(strings.ToLower(s), sub) }
