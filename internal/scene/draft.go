package scene

import (
	"strings"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// Draft is the request for Write for Me: the scene's own prompt with its
// closing block swapped for one that writes your next message instead of the
// character's. idea is anything you had typed first.
//
// The rest of the prompt is left exactly as a reply would send it, so the
// server's cached prefix for the scene is used and the draft starts at once.
func Draft(st *store.Store, cfg store.Config, ch store.Chat, cast []chars.Character, hist []ollama.Message, idea string) []ollama.Message {
	msgs := BuildFor(st, cfg, ch, cast, hist)
	return chars.DraftMessages(msgs, strings.Join(chars.CastNames(cast), ", "), userNameOf(cfg), idea)
}

// DraftChars bounds a drafted message. Your turns are short beside the
// character's, and a draft that runs to a page has stopped being yours.
const DraftChars = 1500

// DraftOptions are the reply's own settings with the length held to a
// message's worth.
func DraftOptions(cfg store.Config, kind string) ollama.Options {
	opts := OptionsFor(cfg, kind)
	if opts.NumPredict <= 0 || opts.NumPredict > draftTokens {
		opts.NumPredict = draftTokens
	}
	return opts
}

// draftTokens is DraftChars in tokens, roughly, with room to finish a sentence.
const draftTokens = 400

// CanDraft reports whether Write for Me belongs in a conversation: a scene,
// with somebody in it to write to.
func CanDraft(ch store.Chat, cast []chars.Character) bool {
	if ch.Kind != store.KindRoleplay && ch.Kind != "" {
		return false
	}
	for _, c := range cast {
		if c.Name != "" {
			return true
		}
	}
	return false
}

// HistoryBefore is the conversation as the model would see it just before
// one of its messages: what the recap does not cover, up to but not including
// that message. It is what rewriting a message you sent is written against.
func HistoryBefore(st *store.Store, ch store.Chat, nameOf func(int64) string, messageID int64) ([]ollama.Message, error) {
	msgs, err := st.MessagesAfter(ch.ID, 0)
	if err != nil {
		return nil, err
	}
	var before []store.Message
	for _, m := range msgs {
		if m.ID >= messageID {
			break
		}
		// From the recap on when the message is after it. A message the recap
		// already covers is rewritten against the turns just before it.
		if m.ID > ch.SummaryUpto || messageID <= ch.SummaryUpto {
			before = append(before, m)
		}
	}
	return History(before, nameOf), nil
}
