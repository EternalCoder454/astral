package chars

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"astral/internal/ollama"
)

// The designer is a conversation whose product is a character.
//
// Writing a good card is genuinely hard — the fields interact, the opening
// message sets the style for everything after it, and a description that reads
// well to a person can still give a model nothing to act on. Being interviewed
// about it is far easier than facing eight empty text boxes, and the model is
// better at turning "a tired detective who doesn't trust anyone" into usable
// card fields than most people are on a first attempt.

// DesignerSystem is the framing for the interview. The constraints matter:
// small local models otherwise ask twelve questions at once, or skip straight
// to writing the character before the user has said anything about them.
const DesignerSystem = `You are a character designer helping someone create a roleplay character for a local AI chat app.

Your job is to interview them, not to lecture them. Follow these rules:
- Ask at most two questions per message. Never present a numbered list of more than two questions.
- Start from whatever they give you, however vague. If they say "a detective", run with it and ask what makes this one different.
- Offer concrete suggestions they can accept or reject, rather than open-ended prompts. "Is she bitter about it, or does she find it funny?" beats "What is her personality?"
- The person playing opposite this character is written {{user}}, and the character themselves {{char}}. You do not need to use those while talking, but the card you eventually produce will.
- Keep your messages short: a few sentences. This is a conversation, not a form.
- When you have enough for a rounded character (who they are, how they speak, the situation, and how a scene with them opens), say so plainly and tell them to press "Create character".

Do not write the character card yourself, and do not output JSON. That happens separately. Just talk it through with them.`

// AssistantSystem frames a plain chat. Short on purpose: a general-purpose
// conversation is the one case where the app should get out of the way.
const AssistantSystem = `You are a helpful assistant running locally on the user's own machine. Be direct and concise. Answer what was asked.`

// DesignerOpening is shown when a designer chat starts, so the blank page is
// never the user's problem to solve.
const DesignerOpening = `Let's build someone.

Tell me anything to start, a role, a setting, a line of dialogue you want to hear, or just a feeling. "A tired detective", "someone who runs a bookshop at the end of the world", "unbearably smug" all work.

If you'd rather I just invent one, say so and tell me what kind of story you're in the mood for.`

// characterSchema constrains the extraction call. Ollama restricts decoding to
// this schema, so the reply parses — the difference between this and asking
// for JSON in a prompt and hoping.
//
// The field names are the character-card spec's, which means the result is a
// flat V1 card and ParseCard already knows how to read it.
var characterSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name":        {"type": "string"},
    "description": {"type": "string"},
    "personality": {"type": "string"},
    "scenario":    {"type": "string"},
    "first_mes":   {"type": "string"},
    "mes_example": {"type": "string"},
    "tags":        {"type": "array", "items": {"type": "string"}}
  },
  "required": ["name", "description", "personality", "scenario", "first_mes"]
}`)

// extractInstruction is the turn appended to the conversation when the user
// asks for the character. It restates the field meanings because by this point
// the design talk is thousands of tokens back, and a small model will
// otherwise write a synopsis into every field.
const extractInstruction = `Now write the character we have designed as a character card.

Fill each field for its own purpose:
- name: just the name, nothing else.
- description: who they are, how they look, how they carry themselves. Written for a model that has to play them, so concrete details beat adjectives. A short paragraph.
- personality: a handful of traits, comma-separated.
- scenario: where the first scene takes place and what is happening as it opens.
- first_mes: their opening message, in their voice. Put actions and narration in *asterisks* and speech in "quotes". Two or three sentences, third person. This sets the style for the whole roleplay, so make it good.
- mes_example: one short exchange showing how they talk. Use {{user}}: and {{char}}: to mark who is speaking.
- tags: two to five short labels.

Use {{user}} wherever the other person would be named, and {{char}} where the character refers to themselves in a way that a rename should follow. Both are expanded when the scene runs, so a card written with them survives being renamed or played by someone with a different persona.

Base it on what we discussed, do not invent a different character.`

// BuildFromConversation turns a design conversation into a character.
func BuildFromConversation(ctx context.Context, client *ollama.Client, model string, history []ollama.Message, opts ollama.Options) (Character, error) {
	if len(history) == 0 {
		return Character{}, fmt.Errorf("there is nothing here to build a character from yet")
	}
	msgs := make([]ollama.Message, 0, len(history)+2)
	msgs = append(msgs, ollama.Message{Role: ollama.RoleSystem, Content: DesignerSystem})
	msgs = append(msgs, history...)
	msgs = append(msgs, ollama.Message{Role: ollama.RoleUser, Content: extractInstruction})

	// Low temperature: this step is transcription, not invention. The creative
	// work already happened in the conversation, and a high temperature here
	// mostly produces a character subtly different from the one agreed on.
	opts.Temperature = 0.3
	opts.NumPredict = 0 // never truncate a card halfway through a field

	raw, _, err := client.Structured(ctx, model, msgs, opts, characterSchema)
	if err != nil {
		return Character{}, err
	}
	c, err := ParseCard(raw)
	if err != nil {
		return Character{}, fmt.Errorf("the model's answer was not a usable character: %w", err)
	}
	return c, nil
}

// A writing style is harder to write than it looks. "Be more descriptive" is
// not an instruction a model can act on, and the difference between a style
// that works and one that does nothing is usually specificity — naming the
// sentence length, the tense, what to leave out. So styles get the same
// treatment characters do: a conversation, then a structured extraction.

// StyleDesignerSystem frames the interview.
const StyleDesignerSystem = `You are helping someone design a writing style for a roleplay chat app. The style controls how the prose sounds, sentence rhythm, how much description, how dialogue is written, what the scene dwells on.

Your job is to interview them, not to lecture them. Follow these rules:
- Ask at most two questions per message. Never present a numbered list of more than two questions.
- Start from whatever they give you, however vague. "Like a horror novel" is enough to run with, ask whether the dread is in what's described or what isn't.
- Offer concrete alternatives they can pick between, rather than open questions. "Short, clipped sentences, or long ones that run on?" beats "What rhythm do you want?"
- Anchor on things a model can actually follow: paragraph count, sentence length, tense, how much interiority, how dialogue is punctuated, what to avoid.
- Keep your messages short. This is a conversation, not a form.
- When you have enough, say so plainly and tell them to press "Create style".

Do not write the style rules yourself yet, and do not output JSON. That happens separately. Just talk it through with them.

Two things the style never needs to handle:
- Formatting. The app puts narration in *asterisks* and speech in "quotes" already. The style is about voice, not markup.
- Names. A style is applied to every character, so it must never name one. If a rule needs to refer to someone, {{char}} means whichever character is being played and {{user}} means the person playing. So "keep {{char}}'s replies under three sentences", never "keep Sarah's replies short", even if Sarah is who we have been talking about.`

// StyleDesignerOpening starts the conversation, so a blank page is never the
// user's problem to solve.
const StyleDesignerOpening = `Let's build a writing style.

Name a book, a film, a genre, or just a feeling, "sparse and cold", "overwritten Victorian", "like a screenplay", "funny but never winking". Anything is enough to start from.

If you'd rather I suggest a few, say so.`

// styleSchema decomposes a style into one required field per aspect.
//
// An earlier version asked for a single free-form "instructions" string and
// told the model to put one rule per line. Models do not do that — they write
// a paragraph. Measured against a real model it returned seven perfectly good
// instructions run together on a single line, and asked less generously it
// returned one sentence and stopped.
//
// Structured output only guarantees that required fields *exist*, so the way
// to get six instructions is to ask for six things. Each field below is a
// question the model has to answer separately, and the prose is assembled from
// the answers rather than trusted to arrive pre-formatted.
var styleSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name":        {"type": "string"},
    "length":      {"type": "string"},
    "sentences":   {"type": "string"},
    "tense":       {"type": "string"},
    "description": {"type": "string"},
    "dialogue":    {"type": "string"},
    "avoid":       {"type": "string"}
  },
  "required": ["name", "length", "sentences", "tense", "description", "dialogue", "avoid"]
}`)

const styleExtractInstruction = `Now write the writing style we have designed.

Answer each field separately, as an instruction addressed to the model that will write the prose. Write in the imperative, be specific enough to act on, and keep each to one or two sentences.

- name: two or three words, the way someone would pick it from a list. Not a sentence.
- length: how long a reply should be, in paragraphs.
- sentences: the sentence rhythm. Length, variety, how they are built.
- tense: which tense and which person to write in.
- description: how much description, and what it should dwell on.
- dialogue: how spoken lines should sound.
- avoid: what this style should never do.

Base every answer on what we discussed.

Two hard rules:
- Do not mention asterisks, quotes or any formatting. The app handles that, and repeating it here only competes with it.
- Do not name any specific character or person, even one we discussed by name. This style will be applied to every character. Write {{char}} for whichever character is being played and {{user}} for the person playing them. "Keep {{char}} clipped under pressure" is correct; "Keep Sarah clipped" is not.`

// styleFields is the order the assembled instructions are written in, with the
// label each one gets.
var styleFields = []struct{ key, label string }{
	{"length", "Length"},
	{"sentences", "Sentences"},
	{"tense", "Tense and person"},
	{"description", "Description"},
	{"dialogue", "Dialogue"},
	{"avoid", "Avoid"},
}

// BuildStyleFromConversation turns a design conversation into a writing style.
func BuildStyleFromConversation(ctx context.Context, client *ollama.Client, model string, history []ollama.Message, opts ollama.Options) (WritingStyle, error) {
	if len(history) == 0 {
		return WritingStyle{}, fmt.Errorf("there is nothing here to build a style from yet")
	}
	msgs := make([]ollama.Message, 0, len(history)+2)
	msgs = append(msgs, ollama.Message{Role: ollama.RoleSystem, Content: StyleDesignerSystem})
	msgs = append(msgs, history...)
	msgs = append(msgs, ollama.Message{Role: ollama.RoleUser, Content: styleExtractInstruction})

	opts.Temperature = 0.3 // transcription, not invention
	opts.NumPredict = 0

	raw, _, err := client.Structured(ctx, model, msgs, opts, styleSchema)
	if err != nil {
		return WritingStyle{}, err
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		return WritingStyle{}, fmt.Errorf("the model's answer was not a usable style: %w", err)
	}
	w := WritingStyle{
		Name:         strings.TrimSpace(out["name"]),
		Instructions: assembleStyle(out),
	}
	if w.Name == "" || w.Instructions == "" {
		return WritingStyle{}, fmt.Errorf("the model returned an incomplete style")
	}
	return w, nil
}

// assembleStyle turns the answered fields into the labelled block the editor
// shows and the prompt carries. Missing fields are skipped rather than
// printed as empty headings — the schema requires them, but a model can still
// answer one with whitespace.
func assembleStyle(fields map[string]string) string {
	var b strings.Builder
	for _, f := range styleFields {
		v := strings.Join(strings.Fields(fields[f.key]), " ")
		if v == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(f.label)
		b.WriteString(": ")
		b.WriteString(v)
	}
	return b.String()
}
