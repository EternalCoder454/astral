package app

import (
	"context"
	"fmt"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// A conversation whose product is a world.
//
// Characters and writing styles each had one of these and worlds did not, which
// is the wrong way round: a world is the hardest of the three to start from
// nothing. A character is one person you can picture and a style is how you want
// prose to sound, but a world is a description, a set of rules about what can
// happen, and a lorebook whose entries need keys a conversation will actually
// say out loud. Facing that as empty boxes is where worlds stop being made.

// newWorldDesignerChat opens the interview.
func (a *App) newWorldDesignerChat() {
	a.startPlainChat(store.KindWorldDesigner, "Designing a world", world.DesignerOpening)
}

// buildWorldFromChat turns the open design conversation into a world.
//
// Saved outright rather than opened in an editor for review, unlike a character
// or a style. A world is not one form: it is a world plus a lorebook of entries,
// and there is no single dialog that could show all of it. So it is written and
// then opened, where every part of it can be edited in the place that part lives.
func (a *App) buildWorldFromChat() {
	history := a.chat.History()
	if len(history) < 2 {
		a.toast("Talk it through a little first, then I can build the world.")
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
		draft, err := world.BuildFromConversation(ctx, client, model, history, opts)

		coreglib.IdleAdd(func() bool {
			a.chat.SetBuilding(false)
			if err != nil {
				a.toast("Could not build the world: " + friendlyBuildError(err))
				return false
			}
			a.saveWorldDraft(draft)
			return false
		})
	}()
}

// saveWorldDraft writes the world and its first lorebook, then opens it.
func (a *App) saveWorldDraft(draft world.Draft) {
	id, err := a.store.SaveWorld(draft.World)
	if err != nil {
		a.toast("Could not save the world: " + err.Error())
		return
	}
	draft.World.ID = id

	kept := 0
	for _, e := range draft.Entries {
		e.WorldID = id
		if _, err := a.store.SaveLoreEntry(e); err != nil {
			// One entry failing is not worth losing the world over, and the
			// lorebook is editable by hand afterwards.
			continue
		}
		kept++
	}
	switch kept {
	case 0:
		a.toast(draft.World.Name + " is saved. Its lorebook is empty, so add what should be in it.")
	case 1:
		a.toast(draft.World.Name + " is saved, with one thing in its lorebook.")
	default:
		a.toast(fmt.Sprintf("%s is saved, with %d things in its lorebook.", draft.World.Name, kept))
	}
	a.showWorld(draft.World)
}
