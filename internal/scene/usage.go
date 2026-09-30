package scene

import (
	"strings"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// How full the model's memory is, in parts: what Character.AI shows as a
// memory meter. Measured from the request the next turn would send, so it is
// the real thing and not an estimate of it, apart from counting characters
// rather than tokens, as the rest of Astral does.

// UsagePart is one part of what a turn sends, and roughly how many tokens it
// takes.
type UsagePart struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

// Usage is the next turn's share of the model's memory.
type Usage struct {
	// Window is how many tokens the model holds at once, and Reply how many
	// of them are kept for the reply.
	Window int `json:"window"`
	Reply  int `json:"reply"`
	// Used is everything the next turn sends, and Parts what it is made of.
	Used  int         `json:"used"`
	Parts []UsagePart `json:"parts"`
	// Conversation is the part of the transcript sent word for word, and
	// FoldsAt how large it may grow before the oldest turns are folded into
	// the record. Zero FoldsAt means the conversation is never folded.
	Conversation int `json:"conversation"`
	FoldsAt      int `json:"folds_at"`
}

// tokensPerChar matches the planner's estimate; see chars.Plan.
const charsPerToken = 3.5

func toTokens(chars int) int { return int(float64(chars)/charsPerToken + 0.5) }

// MeasureUsage builds the next turn's request and says what it is made of.
func MeasureUsage(st *store.Store, cfg store.Config, ch store.Chat, cast []chars.Character, hist []ollama.Message) Usage {
	msgs := BuildFor(st, cfg, ch, cast, hist)
	opts := OptionsFor(cfg, ch.Kind)
	u := Usage{Window: opts.NumCtx, Reply: opts.NumPredict}
	if u.Window <= 0 {
		u.Window = chars.DefaultNumCtx
	}
	if u.Reply <= 0 {
		u.Reply = chars.DefaultReplyTokens
	}
	sizes := map[string]int{}
	var order []string
	add := func(name string, n int) {
		if _, ok := sizes[name]; !ok {
			order = append(order, name)
		}
		sizes[name] += n
	}
	for i, m := range msgs {
		n := len(m.Content)
		switch {
		case i == 0 && m.Role == ollama.RoleSystem:
			add("Characters and Rules", n)
		case m.Role != ollama.RoleSystem:
			add("Conversation", n)
		case i == len(msgs)-1 && (strings.HasPrefix(m.Content, "[") || m.Content == chars.NovelReminder()):
			add("Closing Rules", n)
		case strings.HasPrefix(m.Content, "Earlier in this scene"),
			strings.HasPrefix(m.Content, "Earlier in this conversation"),
			strings.HasPrefix(m.Content, "The story so far"):
			add("Record", n)
		case strings.HasPrefix(m.Content, "Reference for this world"):
			add("World and Lorebook", n)
		case strings.HasPrefix(m.Content, chars.MemoryHeading):
			add("Recalled and Pinned", n)
		default:
			add("Other", n)
		}
	}
	for _, name := range order {
		t := toTokens(sizes[name])
		u.Parts = append(u.Parts, UsagePart{Name: name, Tokens: t})
		u.Used += t
	}
	u.Conversation = toTokens(sizes["Conversation"])
	if ch.Kind == store.KindRoleplay || ch.Kind == store.KindAssistant || ch.Kind == store.KindNovel || ch.Kind == "" {
		var b chars.Budget
		switch {
		case ch.Kind == store.KindAssistant || ch.Kind == store.KindNovel:
			b = BudgetForPlain(cfg, ch.Kind)
		case len(cast) > 1:
			b = GroupBudget(cfg, cast, Persona(cfg), Relations(st, cast))
		case len(cast) == 1:
			b = Budget(cfg, cast[0], Persona(cfg))
		}
		u.FoldsAt = toTokens(b.Compact)
	}
	return u
}
