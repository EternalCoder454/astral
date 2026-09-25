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
	if cast > 0 {
		add(ui.IconCharacters, "Play a scene", "with someone from your cast", true, a.showCharacters)
	}
	add(ui.IconChat, "General chat", "", cast == 0, a.newAssistantChat)

	heading("Make something")
	add(ui.IconDesigner, "New character", "the model interviews you", false, a.newDesignerChat)
	add(ui.IconEdit, "New writing style", "changes how the prose sounds", false, a.newStyleDesignerChat)
	add(ui.IconFolder, "Import a character", "from a .png or .json card", false, a.actionImportCharacter)

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
	a.refreshAttachAvailability()
}

// refreshAttachAvailability decides whether the attach control is offered.
//
// Only a character design chat, and only when the model can actually see: a
// text-only model handed an image either ignores it, which looks like the
// feature is broken, or rejects the whole request and loses the message with
// it. Asking costs one small call and is done off the UI thread.
func (a *App) refreshAttachAvailability() {
	if a.chat == nil {
		return
	}
	if a.chat.Chat().Kind != store.KindDesigner {
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
	client := a.client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		can, err := client.CanSee(ctx, model)
		coreglib.IdleAdd(func() bool {
			if a.chat.Chat().Kind == store.KindDesigner {
				a.chat.SetCanAttachImages(err == nil && can)
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
