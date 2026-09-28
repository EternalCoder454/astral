package scene

import (
	"fmt"
	"strings"

	"astral/internal/ollama"
	"astral/internal/prompts"
	"astral/internal/store"
)

// RecordSent keeps a copy of a request as it is about to go to the model, so
// the Prompt Optimizer can read a prompt in the company it is actually sent in.
// See prompts.Sent.
func RecordSent(ch store.Chat, group bool, msgs []ollama.Message) {
	kind, name := sentKind(ch.Kind, group)
	prompts.RecordSent(kind, name, RenderRequest(msgs))
}

func sentKind(kind string, group bool) (string, string) {
	switch kind {
	case store.KindAssistant:
		return "chat", "General Chat Request"
	case store.KindDesigner:
		return "designer", "Character Designer Request"
	case store.KindStyleDesigner:
		return "style", "Style Designer Request"
	case store.KindWorldDesigner:
		return "world", "World Designer Request"
	case store.KindPromptOptimizer:
		return "optimizer", "Prompt Optimizer Request"
	}
	if group {
		return "group", "Group Scene Request"
	}
	return "scene", "Scene Request"
}

// requestTail is how many of the last messages are kept whole. The middle of a
// long transcript is the conversation, not the prompt, and it is the end of a
// request that carries the reminders.
const requestTail = 3

// RenderRequest lays a request out as text: the system prompt whole, the
// middle of the conversation counted rather than copied, and the last few
// messages whole, each labelled with who it is from.
func RenderRequest(msgs []ollama.Message) string {
	var b strings.Builder
	start := 0
	if len(msgs) > 0 && msgs[0].Role == ollama.RoleSystem {
		b.WriteString("[system]\n")
		b.WriteString(msgs[0].Content)
		b.WriteString("\n\n")
		start = 1
	}
	rest := msgs[start:]
	if skipped := len(rest) - requestTail; skipped > 0 {
		fmt.Fprintf(&b, "[%d earlier messages of the conversation, left out here]\n\n", skipped)
		rest = rest[skipped:]
	}
	for _, m := range rest {
		fmt.Fprintf(&b, "[%s]\n%s\n\n", m.Role, m.Content)
	}
	return strings.TrimSpace(b.String())
}
