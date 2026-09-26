package scene

import (
	"strings"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// A scene with a cast goes through the same assembler as a two-hander, for the
// same reason: the window and the phone have to produce the same prompt for the
// same scene, or the characters answer differently depending on which screen you
// are looking at.

// BuildFor assembles the messages for one turn of a scene with any number of
// characters in it.
//
// A cast of one, or none, is an ordinary scene and goes through Build unchanged.
// That is not a special case bolted on: most scenes have one character, and they
// must keep producing exactly the prompt they produced before, down to the byte,
// or every existing conversation loses its cached prefix the first time it is
// reopened.
func BuildFor(st *store.Store, cfg store.Config, ch store.Chat, cast []chars.Character, hist []ollama.Message) []ollama.Message {
	if len(cast) < 2 {
		var one chars.Character
		if len(cast) == 1 {
			one = cast[0]
		}
		return Build(st, cfg, ch, one, hist)
	}
	switch ch.Kind {
	case store.KindDesigner, store.KindStyleDesigner, store.KindAssistant:
		// These have no cast by construction. Answered here rather than left to
		// fall through, so a stray cast row on one of them cannot turn the
		// character designer into a roleplay.
		return Build(st, cfg, ch, chars.Character{}, hist)
	}

	p := Persona(cfg)
	sc := chars.Scene{
		Persona:          p,
		Recap:            ch.Summary,
		History:          hist,
		Budget:           GroupBudget(cfg, cast, p),
		StyleChanged:     StyleChanged(cfg, ch, len(hist)),
		Direction:        ch.Note,
		NarrationDrifted: chars.NarrationDrifted(hist),
		RollCall:         chars.RollCall(assistantTurns(hist), chars.CastNames(cast)),
	}
	sc.Lore = GroupLore(st, cast, hist, sc.Budget.Lore)
	return chars.BuildGroupMessages(cast, sc)
}

// GroupBudget divides the context window for a cast.
//
// The system prompt is measured rather than estimated, and for a group that
// matters more than it does for one character: five cards' descriptions are
// thousands of characters that have to come out of the transcript rather than out
// of the window.
func GroupBudget(cfg store.Config, cast []chars.Character, p chars.Persona) chars.Budget {
	numCtx := cfg.NumCtx
	if numCtx <= 0 {
		numCtx = chars.DefaultNumCtx
	}
	return chars.Plan(numCtx, cfg.NumPredict, len(chars.BuildGroupSystem(cast, p)))
}

// GroupLore is the world block for a scene with a cast: the setting, plus
// whichever entries the conversation is currently touching.
//
// The world comes from the first member that has one. A cast drawn from two
// worlds is a scene that has to happen in one of them, and the alternative —
// concatenating two lorebooks — spends the budget twice to describe a place that
// does not exist.
func GroupLore(st *store.Store, cast []chars.Character, hist []ollama.Message, budget int) string {
	var host chars.Character
	for _, c := range cast {
		if c.WorldID != 0 {
			host = c
			break
		}
	}
	if host.WorldID == 0 {
		return ""
	}
	// Every member's own text is scanned for keywords, not just the host's: a
	// scene that has only just opened has almost no transcript, and the people
	// in it are the best clue to which parts of the world are about to matter.
	var b strings.Builder
	for _, c := range cast {
		b.WriteString(c.Description)
		b.WriteByte(' ')
		b.WriteString(c.Scenario)
		b.WriteByte(' ')
	}
	host.Description, host.Scenario = b.String(), ""
	return Lore(st, host, hist, budget)
}

// History turns stored turns into the conversation the model sees, putting each
// speaker's name back on their words.
//
// The name is stored beside a message rather than inside it, so it has to go
// back on here. It is not optional: the transcript is the strongest instruction
// in the context, and a scene whose history arrives unlabelled is a scene
// teaching the model that replies carry no labels.
//
// Consecutive beats by the cast are merged into one assistant message. They were
// one reply when the model wrote them, several chat turns would imply the
// characters took turns being prompted, and a few model templates require the
// roles to alternate at all.
//
// Only beats, which is to say only messages that carry a speaker. Two adjacent
// replies in a scene with one character happen when you delete your own message
// between them, and merging those would change the prompt of a conversation that
// has nothing to do with groups.
func History(msgs []store.Message, nameOf func(int64) string) []ollama.Message {
	out := make([]ollama.Message, 0, len(msgs))
	merged := false // the previous message was a beat, so a beat can join it
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		beat := m.Role == ollama.RoleAssistant && m.CharacterID != 0 && nameOf != nil
		if beat {
			content = chars.Label(nameOf(m.CharacterID), content)
		}
		if beat && merged && len(out) > 0 {
			out[len(out)-1].Content += "\n\n" + content
			continue
		}
		out = append(out, ollama.Message{Role: m.Role, Content: content})
		merged = beat
	}
	return out
}

// assistantTurns is the cast's replies from a history, oldest last, for the
// checks that look at how the scene has been going.
func assistantTurns(hist []ollama.Message) []string {
	out := make([]string, 0, 4)
	for _, m := range hist {
		if m.Role == ollama.RoleAssistant {
			out = append(out, m.Content)
		}
	}
	if len(out) > 4 {
		out = out[len(out)-4:]
	}
	return out
}
