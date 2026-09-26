package world

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"astral/internal/ollama"
)

// The world designer is a conversation whose product is a setting.
//
// Characters and writing styles both had one and worlds did not, which is the
// wrong way round: a world is the hardest of the three to start. A character is
// one person you can picture, a style is how you want prose to sound, and a
// world is a name, a description, a set of rules about what can happen, and a
// lorebook whose entries need keys that a conversation will plausibly say out
// loud. Facing that as empty boxes is where a world stops being made.
//
// The interview is the same shape as the other two, and the extraction is the
// same trick: a schema Ollama constrains decoding to, so the answer parses
// instead of being asked for JSON and hoped over.

// DesignerSystem frames the interview.
//
// The constraints are the ones the other designers needed. A small model asked
// to help build a world will otherwise produce a gazetteer in its first reply,
// which is both unusable and the opposite of being interviewed.
const DesignerSystem = `You are a world designer helping someone build the setting for a roleplay chat app.

Your job is to interview them, not to lecture them. Follow these rules:
- Ask at most two questions per message. Never present a numbered list of more than two questions.
- Start from whatever they give you, however vague. "A city on a river" is enough to run with: ask what the river is used for and who controls it.
- Offer concrete alternatives they can pick between rather than open questions. "Is the harbour thriving, or is everyone pretending it still is?" beats "Tell me about the economy."
- Push on what makes a scene happen here rather than anywhere else: what is scarce, who holds power, what a person here takes for granted, what would get someone killed or ruined.
- Keep your messages short, a few sentences. This is a conversation, not a questionnaire.
- Do not invent a long list of names. Two or three that matter beat twenty that do not.
- When you have enough for a place someone could play a scene in, say so plainly and tell them to press "Create world".

Do not write the world out yourself, and do not output JSON. That happens separately. Just talk it through with them.`

// DesignerOpening starts the conversation, so a blank page is never the user's
// problem to solve.
const DesignerOpening = `Let's build a world.

Anything is enough to start from: a place, a period, a single image, a rule you want to be true. "A port city where the tide is wrong", "post-war countryside", "everyone can hear one other person's thoughts" all work.

If you would rather I invented one, say so and tell me roughly what kind of story you want to set in it.`

// designerSchema decomposes a world into one required field per part, and asks
// for the lorebook as a list.
//
// Structured output only guarantees that required fields exist, so the way to get
// entries with keys is to ask for objects with keys. Asked for a freeform block
// and told to use a format, a model writes prose with headings.
var designerSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name":        {"type": "string"},
    "description": {"type": "string"},
    "rules":       {"type": "string"},
    "entries": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name":    {"type": "string"},
          "keys":    {"type": "array", "items": {"type": "string"}},
          "content": {"type": "string"}
        },
        "required": ["name", "keys", "content"]
      }
    }
  },
  "required": ["name", "description", "rules", "entries"]
}`)

// extractInstruction is the turn that asks for the world itself.
//
// It spells out what a key is for, because that is the part a model gets wrong
// in a way nothing downstream can repair: an entry whose keys are abstractions
// is an entry that never fires, and a world made of those looks like it is
// working right up until it is needed.
const extractInstruction = `Now write the world out, using everything we agreed.

name: two or three words, the way it would be listed.
description: what this place is, in a short paragraph. Setting, period, atmosphere.
rules: what is always true here, one per line. What can and cannot happen, who holds power, what a person here takes for granted. These are sent to the model on every single turn, so keep it to the handful that change how a scene plays.
entries: the specific things in this world. One per subject: a person, a place, a faction, an object, a piece of history.

For each entry:
- name is its label.
- content is what is true about it, written as fact, a few sentences at most.
- keys are the words a conversation would have to mention for this entry to be needed. Use the words people actually say: names, places, nicknames, plural and singular. "harbour", "the docks", "harbourmaster" for the harbour. Never use abstractions like "politics" or "history" as a key, because nobody says them out loud and the entry would never appear.

Write between three and ten entries. Only things we actually discussed or that follow directly from it. Do not invent a cast of characters nobody mentioned.`

// Draft is what the designer produces: a world and a first lorebook for it.
type Draft struct {
	World   World
	Entries []Entry
}

// BuildFromConversation turns a design conversation into a world.
func BuildFromConversation(ctx context.Context, client *ollama.Client, model string, history []ollama.Message, opts ollama.Options) (Draft, error) {
	if len(history) == 0 {
		return Draft{}, fmt.Errorf("there is nothing here to build a world from yet")
	}
	msgs := make([]ollama.Message, 0, len(history)+2)
	msgs = append(msgs, ollama.Message{Role: ollama.RoleSystem, Content: DesignerSystem})
	msgs = append(msgs, history...)
	msgs = append(msgs, ollama.Message{Role: ollama.RoleUser, Content: extractInstruction})

	opts.Temperature = 0.3 // transcription, not invention
	opts.NumPredict = 0

	raw, _, err := client.Structured(ctx, model, msgs, opts, designerSchema)
	if err != nil {
		return Draft{}, err
	}
	return ParseDraft(raw)
}

// ParseDraft reads the model's answer into a world and its entries.
//
// Separate from the call so it can be tested against a fixed answer, which is
// the only way to have an opinion about the cleaning below without a model in
// the loop.
func ParseDraft(raw []byte) (Draft, error) {
	var out struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Rules       string `json:"rules"`
		Entries     []struct {
			Name    string   `json:"name"`
			Keys    []string `json:"keys"`
			Content string   `json:"content"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Draft{}, fmt.Errorf("the model's answer was not a usable world: %w", err)
	}

	d := Draft{World: World{
		Name:        strings.TrimSpace(out.Name),
		Description: strings.TrimSpace(out.Description),
		Rules:       strings.TrimSpace(out.Rules),
	}}
	if d.World.Name == "" {
		return Draft{}, fmt.Errorf("the model returned a world with no name")
	}

	seen := make(map[string]bool, len(out.Entries))
	for _, e := range out.Entries {
		name := strings.TrimSpace(e.Name)
		content := strings.TrimSpace(e.Content)
		if name == "" || content == "" {
			continue
		}
		// Identity in a lorebook is (world, name), so two entries with one name
		// would be one entry that the second overwrote.
		if key := strings.ToLower(name); seen[key] {
			continue
		} else {
			seen[key] = true
		}
		// The same key cleaner the learning pass uses, so a hand-designed world
		// and a learned one agree about what a usable key is. No scene to
		// measure saturation against yet, which is the right answer here: there
		// is no transcript for a world that has not been played.
		keys := cleanKeys(append(e.Keys, name), nil)
		if len(keys) == 0 {
			// An entry nothing can trigger would never be sent, and would sit in
			// the lorebook looking like it worked.
			continue
		}
		d.Entries = append(d.Entries, Entry{
			Name:    name,
			Keys:    keys,
			Content: content,
			Enabled: true,
			// Written with the user in the room rather than learned behind their
			// back, so it needs no review and is not marked as the model's.
			Auto: false,
		})
	}
	return d, nil
}
