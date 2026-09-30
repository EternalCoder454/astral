package scene

import (
	"context"
	"fmt"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// ContinueTail is how many of a chat's last messages go on into its
// continuation word for word: enough for the new chat to pick up the thread
// and the voice, few enough that it starts with its memory nearly empty.
const ContinueTail = 6

// CanContinue reports whether a chat is one that can be carried on in a new
// chat: a scene, or a general chat, the conversations that keep a record.
func CanContinue(ch store.Chat) bool {
	switch ch.Kind {
	case store.KindRoleplay, store.KindAssistant, "":
		return ch.ID != 0
	}
	return false
}

// ContinueChat carries a chat on in a new one: it brings the chat's record up
// to the last ContinueTail messages, with model, and starts the continuation
// from it. See store.ContinueChat. The original is left as it was, record
// included: the record written here belongs to the new chat.
//
// The cast, and who you played the chat as, are read from the chat itself,
// so the window and the phone write the same record: a world scene's cast is
// its narrator, and your lines are labelled with the persona of this chat.
func ContinueChat(ctx context.Context, client *ollama.Client, model string, st *store.Store, cfg store.Config,
	ch store.Chat) (store.Chat, error) {
	if !CanContinue(ch) {
		return store.Chat{}, fmt.Errorf("only a scene or a general chat can be continued")
	}
	cast := castOf(st, ch)
	cfg = playedAs(st, cfg, ch)
	stored, err := st.MessagesAfter(ch.ID, ch.SummaryUpto)
	if err != nil {
		return store.Chat{}, err
	}
	if len(stored) == 0 {
		return store.Chat{}, fmt.Errorf("there is nothing to carry on from yet")
	}
	n := min(ContinueTail, len(stored))
	aged, from := stored[:len(stored)-n], stored[len(stored)-n].ID

	recap := ch.Summary
	if len(aged) > 0 {
		if model == "" {
			return store.Chat{}, fmt.Errorf("choose a model first")
		}
		byID := make(map[int64]string, len(cast))
		for _, c := range cast {
			byID[c.ID] = c.Name
		}
		// Labelled, as for the scene's own record, so it can say who did what.
		wire, _ := HistoryWithIDs(aged, func(id int64) string { return byID[id] })
		p := Persona(cfg)
		opts := OptionsFor(cfg, ch.Kind)
		switch {
		case ch.Kind == store.KindAssistant:
			recap, err = chars.CompactPlain(ctx, client, model, ch.Summary, wire, p, opts, PlainBudget(cfg))
		case len(cast) > 1:
			recap, err = chars.CompactFor(ctx, client, model, ch.Summary, wire, cast, p, opts,
				GroupBudget(cfg, cast, p, Relations(st, cast)))
		default:
			var one chars.Character
			if len(cast) == 1 {
				one = cast[0]
			}
			recap, err = chars.CompactFor(ctx, client, model, ch.Summary, wire, cast, p, opts, Budget(cfg, one, p))
		}
		if err != nil {
			return store.Chat{}, fmt.Errorf("writing the story so far: %w", err)
		}
	}
	return st.ContinueChat(ch.ID, from, recap, store.ContinueTitle(ch.Title))
}

// castOf is who is in a chat: its cast when it has more than one, else its
// character, or a world scene's narrator.
func castOf(st *store.Store, ch store.Chat) []chars.Character {
	var lead chars.Character
	switch {
	case ch.CharacterID != 0:
		lead, _ = st.Character(ch.CharacterID)
	case ch.WorldID != 0:
		if w, err := st.World(ch.WorldID); err == nil {
			lead = Narrator(w)
		}
	}
	cast, _ := st.Cast(ch.ID)
	// As the window opens it: a scene in a world is played by the place as
	// well as by anyone in it, so the narrator leads a cast that is stored
	// without it.
	if ch.WorldID != 0 && len(cast) > 0 && lead.Name != "" {
		cast = append([]chars.Character{lead}, cast...)
	}
	if len(cast) > 1 {
		return cast
	}
	if lead.Name != "" {
		return []chars.Character{lead}
	}
	return nil
}

// playedAs is cfg with the persona a chat is played as, when it has its own.
func playedAs(st *store.Store, cfg store.Config, ch store.Chat) store.Config {
	if ch.PersonaID != 0 {
		if p, err := st.Persona(ch.PersonaID); err == nil {
			cfg.PersonaName, cfg.PersonaDescription = p.Name, p.Description()
		}
	}
	return cfg
}
