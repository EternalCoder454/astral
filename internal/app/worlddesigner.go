package app

import (
	"context"
	"fmt"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"astral/internal/chars"
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
	a.startPlainChat(store.KindWorldDesigner, "Designing a World", world.DesignerOpening)
}

// reviseWorld opens a design conversation about a world that already exists.
//
// The world rides on the chat, the same way a character does: a world design chat
// that names one is revising it, and the column was already there.
func (a *App) reviseWorld(w world.World) {
	if w.ID == 0 {
		a.toast("Save this world first, then the designer can revise it.")
		return
	}
	a.chat.Clear()
	a.chat.LoadChat(store.Chat{
		Model:   a.cfg.Model,
		Kind:    store.KindWorldDesigner,
		WorldID: w.ID,
		Title:   "Revising " + w.Name,
	}, chars.Character{}, nil)
	a.chat.ShowGreeting(world.ReviseOpening(w))
	a.showChat()
	a.sidebar.Select(0)
	a.setTitle(store.Chat{Title: "Revising " + w.Name}, chars.Character{})
	a.chat.FocusComposer()
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

	// A revision's build is shown the world as it stands; see
	// world.ReviseFromConversation.
	existingWorld, revising := a.revisingWorld()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		opts := fitBuild(ctx, client, model, store.KindWorldDesigner, opts, history)
		var draft world.Draft
		var err error
		if revising {
			draft, err = world.ReviseFromConversation(ctx, client, model, existingWorld, history, opts)
		} else {
			draft, err = world.BuildFromConversation(ctx, client, model, history, opts)
		}

		coreglib.IdleAdd(func() bool {
			a.chat.SetBuilding(false)
			if err != nil {
				a.toast("Could not build the world: " + friendlyBuildError(err))
				return false
			}
			// A revision keeps the world it came from, and its lorebook with it:
			// the entries are the world's memory and most of them were learned
			// from play, which a rewrite would throw away.
			if existing, ok := a.revisingWorld(); ok {
				merged := world.Revise(existing, draft.World)
				if _, err := a.store.SaveWorld(merged); err != nil {
					a.toast("Could not save the world: " + err.Error())
					return false
				}
				a.toast(merged.Name + " is saved, with its lorebook untouched.")
				a.showWorld(merged)
				return false
			}
			a.saveWorldDraft(draft)
			return false
		})
	}()
}

// revisingWorld is the world the open design chat is about, when it is about one.
func (a *App) revisingWorld() (world.World, bool) {
	ch := a.chat.Chat()
	if ch.Kind != store.KindWorldDesigner || ch.WorldID == 0 {
		return world.World{}, false
	}
	w, err := a.store.World(ch.WorldID)
	if err != nil {
		// Deleted while the conversation was open. What was written is still
		// worth keeping, so it becomes a new world rather than being lost.
		return world.World{}, false
	}
	return w, true
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
		a.toast(draft.World.Name + " is saved, with an empty lorebook to fill.")
	case 1:
		a.toast(draft.World.Name + " is saved, with one thing in its lorebook.")
	default:
		a.toast(fmt.Sprintf("%s is saved, with %d things in its lorebook.", draft.World.Name, kept))
	}
	a.showWorld(draft.World)
}
