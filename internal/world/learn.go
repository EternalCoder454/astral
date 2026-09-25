package world

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"astral/internal/ollama"
)

// Lore is only worth having if it keeps up with the story. Written by hand it
// goes stale the moment a scene establishes something new, and nobody stops
// mid-roleplay to file an encyclopedia entry.
//
// So the model keeps it. After every few turns it is asked what has become
// permanently true, and the answers are merged into the lorebook. The result
// is a world that remembers what happened in it without anyone maintaining it.

// LearnEveryTurns is how many turns pass between learning passes. It is not
// every turn: this is a second model call, and most turns establish nothing
// that outlives them.
const LearnEveryTurns = 6

// AutoApplyConfidence is the bar an automatically learned entry must clear to
// be switched on by itself.
//
// Auto-written lore is permanent and is injected into every later scene that
// mentions its subject, so a wrong fact does lasting damage in a way a wrong
// reply does not: a bad reply is one message, a bad entry quietly shapes every
// message after it. Anything the model is less than fairly sure of is stored
// disabled and waits to be looked at instead.
const AutoApplyConfidence = 0.75

// learnSystem frames the extraction. The distinction it works hardest to draw
// is between a fact and an event, because a model asked to "record what
// happened" will happily fill a world bible with a minute-by-minute account of
// one conversation, which is both useless later and expensive forever.
const learnSystem = `You maintain the world bible for a roleplay: a reference of things that are permanently true, which will be consulted whenever they come up again.

Record only what outlives this scene:
- People: who they are, what they do, what they look like, what they are known for.
- Places: what they are, where they are, what they are like.
- Objects, organisations, customs, history, rules of how this world works.
- Relationships and standing facts: who owes whom, who is forbidden from where.

Do NOT record:
- What happened in this conversation. Events belong to the scene, not the world.
- Anything temporary: moods, positions, what someone is holding right now.
- Anything already covered by an existing entry unless you are adding to it.
- Speculation. Only what the scene established as true.

For each subject, give:
- name: what the subject is called. Use the same name as an existing entry when you are updating one, exactly as written.
- keys: the words a conversation would use to refer to it. Include the name itself, plus any alias or shorter form. Keep them specific: a key that is a common word will match everything and is worse than no entry.
- content: the facts, in plain declarative sentences. Under sixty words. If you are updating an existing entry, restate it in full including what was already there.
- confidence: how certain you are that the scene actually established this, from 0 to 1. Be honest and be strict. Use 0.9 or above only when it was stated outright. Use 0.5 or below when you are inferring it, when it might have been figurative, or when a character might have been lying.

Do not write an entry about the world as a whole. Its name and description are already sent with every entry, so an entry about the setting itself is the same text twice, and it will match every turn.

If nothing in the exchange established anything permanent, return an empty list. That is a normal and common answer.`

var learnSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "entries": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name":       {"type": "string"},
          "keys":       {"type": "array", "items": {"type": "string"}},
          "content":    {"type": "string"},
          "confidence": {"type": "number"}
        },
        "required": ["name", "keys", "content", "confidence"]
      }
    }
  },
  "required": ["entries"]
}`)

// existingBudgetChars bounds how much of the current lorebook is shown to the
// model when it is asked to extend it. Enough to stop it duplicating what is
// already there, not so much that a large world makes every pass expensive.
const existingBudgetChars = 4000

// Learn asks the model what the exchange established about the world.
//
// existing is the current lorebook, used so the model extends entries instead
// of duplicating them. turns is the part of the conversation to examine. The
// returned entries are candidates: they carry Auto, and the caller decides
// whether each one may overwrite what is already stored.
func Learn(ctx context.Context, client *ollama.Client, model string, w World, existing []Entry, turns []ollama.Message, charName, userName string, opts ollama.Options) ([]Entry, error) {
	if model == "" {
		return nil, fmt.Errorf("no model selected")
	}
	if len(turns) == 0 {
		return nil, nil
	}

	var b strings.Builder
	if n := strings.TrimSpace(w.Name); n != "" {
		b.WriteString("World: ")
		b.WriteString(n)
		b.WriteString("\n\n")
	}
	if summary := summarizeExisting(existing, existingBudgetChars); summary != "" {
		b.WriteString("Entries that already exist. Extend one by reusing its name exactly; do not repeat what it already says as something new:\n")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	b.WriteString("The exchange to examine. ")
	b.WriteString(charName)
	b.WriteString(" is a character; ")
	b.WriteString(userName)
	b.WriteString(" is the person playing opposite them.\n\n")
	for _, m := range turns {
		who := charName
		if m.Role == ollama.RoleUser {
			who = userName
		}
		b.WriteString(who)
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString("\n\n")
	}
	b.WriteString("List what this established that is permanently true about the world. Return an empty list if that is nothing.")

	msgs := []ollama.Message{
		{Role: ollama.RoleSystem, Content: learnSystem},
		{Role: ollama.RoleUser, Content: b.String()},
	}
	// Low temperature: this is transcription of what was said, and an invented
	// fact here becomes permanent and is injected into every later scene that
	// touches the subject.
	opts.Temperature = 0.2
	opts.NumPredict = 0

	raw, _, err := client.Structured(ctx, model, msgs, opts, learnSchema)
	if err != nil {
		return nil, err
	}
	var out struct {
		Entries []struct {
			Name       string   `json:"name"`
			Keys       []string `json:"keys"`
			Content    string   `json:"content"`
			Confidence float64  `json:"confidence"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("the model's answer was not a usable lorebook update: %w", err)
	}

	entries := make([]Entry, 0, len(out.Entries))
	for _, e := range out.Entries {
		name := strings.TrimSpace(e.Name)
		content := strings.Join(strings.Fields(e.Content), " ")
		if name == "" || content == "" {
			continue
		}
		keys := cleanKeys(append(e.Keys, name), turns)
		if len(keys) == 0 {
			continue // an entry nothing can trigger is dead weight
		}
		entries = append(entries, Entry{
			WorldID:    w.ID,
			Name:       name,
			Keys:       keys,
			Content:    content,
			Enabled:    e.Confidence >= AutoApplyConfidence,
			Auto:       true,
			Confidence: e.Confidence,
		})
	}
	return entries, nil
}

// summarizeExisting lists what the lorebook already holds, newest first, up to
// a budget.
func summarizeExisting(entries []Entry, budget int) string {
	var b strings.Builder
	for _, e := range entries {
		line := "- " + e.Name + ": " + e.Content + "\n"
		if b.Len()+len(line) > budget {
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// minKeyLen is the shortest a trigger word may be.
//
// A one or two letter key matches constantly, and an entry that injects itself
// into every turn costs its budget forever while telling the model nothing it
// asked about.
const minKeyLen = 3

// commonKeys are words a model reaches for that would match almost any scene.
var commonKeys = map[string]bool{
	"the": true, "and": true, "her": true, "him": true, "his": true, "she": true,
	"they": true, "them": true, "this": true, "that": true, "with": true,
	"man": true, "woman": true, "girl": true, "boy": true, "people": true,
	"place": true, "thing": true, "time": true, "day": true, "night": true,
	"city": true, "town": true, "house": true, "room": true, "door": true,
	"world": true, "story": true, "scene": true, "character": true,
}

// keySaturation is the share of the examined messages a key may appear in
// before it is rejected as a trigger.
//
// A key is only useful if it tells one turn apart from another. Playing a
// thirty-turn scene on a storm-lashed coast produced entries triggering on
// "tide" and "the coast", which is every other sentence: they would fire
// constantly, spend the lore budget other entries needed, and tell the model
// nothing it had not just read.
const keySaturation = 0.5

// cleanKeys trims, deduplicates and rejects keys that would fire on anything.
//
// The static list below cannot carry this on its own, because what counts as a
// common word depends on the setting: "tide" is a specific noun in most
// stories and wallpaper in this one. So the scene it was learned from is
// measured too, which needs no list and works in any world.
func cleanKeys(keys []string, scene []ollama.Message) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		k = strings.Join(strings.Fields(k), " ")
		low := strings.ToLower(k)
		switch {
		case len(low) < minKeyLen, seen[low], commonKeys[low], saturated(low, scene):
			continue
		}
		seen[low] = true
		out = append(out, k)
	}
	return out
}

// saturated reports whether a key appears in so many of the examined messages
// that it cannot discriminate between them.
func saturated(low string, scene []ollama.Message) bool {
	if len(scene) < 4 {
		return false // too little to judge by; the static list still applies
	}
	hits := 0
	for _, m := range scene {
		if strings.Contains(strings.ToLower(m.Content), low) {
			hits++
		}
	}
	return float64(hits) > float64(len(scene))*keySaturation
}
