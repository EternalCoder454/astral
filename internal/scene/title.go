package scene

import (
	"context"
	"strings"

	"astral/internal/ollama"
)

// SuggestTitle asks the model for a short title for a conversation, from its
// first message and the reply to it: General Chat's and Novel Chat's, which
// were titled with the first line of what was typed.
func SuggestTitle(ctx context.Context, client *ollama.Client, model, first, reply string) (string, error) {
	clip := func(s string, n int) string {
		if r := []rune(s); len(r) > n {
			return string(r[:n])
		}
		return s
	}
	msgs := []ollama.Message{
		{Role: ollama.RoleSystem, Content: "Write a title of two to six words for this conversation or story. " +
			"Reply with the title only: no quotes, no full stop, no explanation."},
		{Role: ollama.RoleUser, Content: "First message:\n" + clip(first, 1200) + "\n\nReply:\n" + clip(reply, 1200)},
	}
	think := false
	msg, _, err := client.Chat(ctx, model, msgs, ollama.Options{Temperature: 0.3, NumPredict: 24}, &think, nil)
	if err != nil {
		return "", err
	}
	_, out := ollama.SplitThinking(msg.Content)
	return cleanSuggestedTitle(out), nil
}

// cleanSuggestedTitle is the first line of a model's title, without quotes or
// a closing full stop, and no longer than a sidebar row has room for.
func cleanSuggestedTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(strings.TrimSpace(s), "Title:")
	s = strings.Trim(strings.TrimSpace(s), `"'*.“”`)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 48 {
		s = string(r[:48])
	}
	return s
}
