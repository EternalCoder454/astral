package scene

import (
	"context"
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

// Suggest is the request for three suggestions of what you could say next.
func Suggest(st *store.Store, cfg store.Config, ch store.Chat, cast []chars.Character, hist []ollama.Message) []ollama.Message {
	msgs := BuildFor(st, cfg, ch, cast, hist)
	return chars.SuggestMessages(msgs, strings.Join(chars.CastNames(cast), ", "), userNameOf(cfg))
}

// SuggestOptions leave room for three messages.
func SuggestOptions(cfg store.Config, kind string) ollama.Options {
	opts := OptionsFor(cfg, kind)
	if opts.NumPredict <= 0 || opts.NumPredict > 3*draftTokens {
		opts.NumPredict = 3 * draftTokens
	}
	return opts
}

// SuggestSetting is the request for where and when the scene is now.
func SuggestSetting(st *store.Store, cfg store.Config, ch store.Chat, cast []chars.Character, hist []ollama.Message) []ollama.Message {
	msgs := BuildFor(st, cfg, ch, cast, hist)
	return chars.SettingMessages(msgs, strings.Join(chars.CastNames(cast), ", "), userNameOf(cfg))
}

// TrackSetting asks where and when the scene is now, after a reply, for a
// chat whose setting Astral keeps up to date. It answers the new line, or ""
// when there is nothing to change.
//
// The scene's own model and prompt, with the closing block swapped, so the
// prefix the reply just computed is used again and this costs about a second
// rather than another read of the whole scene.
func TrackSetting(ctx context.Context, client *ollama.Client, st *store.Store, cfg store.Config, ch store.Chat,
	cast []chars.Character, hist []ollama.Message, model string) string {
	if !ch.SettingAuto || !CanDraft(ch, cast) || len(hist) < 2 {
		return ""
	}
	noThink := false
	msg, _, err := client.Chat(ctx, model, SuggestSetting(st, cfg, ch, cast, hist), DraftOptions(cfg, ch.Kind), &noThink, nil)
	if err != nil {
		return ""
	}
	_, text := ollama.SplitThinking(msg.Content)
	line := chars.CleanSetting(text)
	if line == "" || line == strings.TrimSpace(ch.Setting) {
		return ""
	}
	return line
}
