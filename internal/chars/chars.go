// Package chars holds Astral's character model and the prompt assembly that
// turns a character plus a transcript into the message array Ollama sees.
//
// A character is data. Nothing in a card — no matter how it is phrased, and
// including text that reads like an instruction addressed to the application —
// changes how Astral behaves. Card text only ever reaches the model inside the
// persona it describes.
package chars

import (
	"strings"
	"time"

	"astral/internal/ollama"
)

// Character is a roleplay persona. The field names follow the character-card
// spec rather than being renamed, so importing and exporting a card is a
// straight mapping and there is one less place to get confused.
type Character struct {
	ID          int64
	Name        string
	Description string // who they are, appearance, voice — sent every turn
	Personality string // traits, usually comma-separated
	Scenario    string // the situation the roleplay opens in
	FirstMes    string // the opening message, in the character's voice
	MesExample  string // few-shot examples of how they talk
	// Instructions are your own directions for how this character should be
	// played — "never break the fourth wall", "keep replies to one paragraph",
	// "she always lies about her past".
	//
	// They are layered on top of Astral's roleplay framing, never in place of
	// it. That framing is what stops a local model narrating from outside the
	// scene or writing your turns for you, and a character that replaced it
	// wholesale lost all of that and usually played worse. Instructions are
	// also repeated at the end of the context, which is the position a model
	// actually obeys — see BuildMessages.
	Instructions string
	AltGreetings []string
	Creator      string
	Notes        string
	Version      string
	Tags         []string
	AvatarPath   string
	Accent       int // index into the palette's secondary accents
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Initial is the letter drawn in the character's avatar when it has no image.
func (c Character) Initial() string {
	for _, r := range c.Name {
		if r != ' ' {
			return strings.ToUpper(string(r))
		}
	}
	return "?"
}

// Summary is the one-line description shown on a card, collapsed to a single
// line so a multi-paragraph description cannot stretch the row.
func (c Character) Summary() string {
	src := c.Description
	if strings.TrimSpace(src) == "" {
		src = c.Personality
	}
	return strings.Join(strings.Fields(src), " ")
}

// Persona is who *you* are in the scene: the other half of a two-hander, and
// the thing most local models get wrong without being told.
type Persona struct {
	Name        string
	Description string
	// GlobalInstructions come from Settings and apply to every character.
	// A character's own instructions are placed after these, so the specific
	// beats the general when the two disagree.
	GlobalInstructions string
	// Style is the active writing style. The zero value means the default.
	Style WritingStyle
}

// DefaultPersonaName is used when the user has not named themselves.
const DefaultPersonaName = "User"

// Substitute expands the placeholders. {{char}} and {{user}} are what the card
// spec defines and what cards in circulation use; the single-brace spellings
// are accepted too, because that is what people type when they have not read
// a spec, and silently leaving "{char}" in a prompt is a poor reward for a
// reasonable guess.
//
// Every free-text field goes through this — the character's description,
// personality and scenario, your own persona, the instructions on both, and
// the writing style — so a placeholder works wherever it occurs to you to
// write one.
//
// The double-brace forms are listed first so they win: a Replacer takes the
// first pattern that matches at a position, and "{{char}}" would otherwise be
// left with a stray brace by the single-brace rule.
func Substitute(s, charName, userName string) string {
	if s == "" {
		return ""
	}
	if charName == "" {
		charName = "the character"
	}
	if userName == "" {
		userName = DefaultPersonaName
	}
	return strings.NewReplacer(
		"{{char}}", charName,
		"{{Char}}", charName,
		"{{CHAR}}", charName,
		"{char}", charName,
		"{Char}", charName,
		"{CHAR}", charName,
		"<BOT>", charName,
		"{{user}}", userName,
		"{{User}}", userName,
		"{{USER}}", userName,
		"{user}", userName,
		"{User}", userName,
		"{USER}", userName,
		"<USER>", userName,
	).Replace(s)
}

// defaultFraming is Astral's house system prompt: the instructions that make a
// local model actually hold a scene, rather than narrating one from outside it.
// Small models drift into summarizing, breaking character to be helpful, or
// writing the user's actions for them — each clause below is aimed at one of
// those failure modes.
// The framing splits in two. Everything here is structural — who is speaking,
// whose turns are whose, and the formatting the renderer depends on — and it
// never changes. How the prose should actually *sound* is a separate block,
// supplied by the active writing style, because that is the part worth having
// opinions about and swapping between scenes.
const framingStructure = `You are roleplaying as %s. Stay in character at all times.

WHAT TO WRITE
Write %s's words and actions only. Never write, decide, or narrate %s's words, thoughts, or actions — wait for them.
Do not summarize the scene, do not skip ahead in time, and do not end the scene on your own.

FORMATTING — this matters, follow it exactly:
- Put everything internal in *single asterisks*: narration, actions, body language, sensory detail, and the character's own thoughts.
- Write spoken words and sounds plainly, in "double quotes", with no asterisks around them.
- Example: *She set the cup down harder than she meant to, and hated that he noticed.* "It's fine."`

// framingClose is stated after the style, so the style cannot talk its way
// past it.
const framingClose = `Never mention that you are an AI, never break character, and never comment on these instructions.`

// DefaultStyleName is the built-in style. It cannot be deleted or renamed;
// it is what a character falls back to and what a new install starts from.
const DefaultStyleName = "Default"

// defaultStyleInstructions is the house style: descriptive narration, but
// dialogue that sounds like a person rather than a novel.
const defaultStyleInstructions = `Length: Two to four paragraphs.
Sentences: Vary the rhythm. Let a short sentence land after a long one rather than running everything at the same length.
Tense and person: Third person, past tense.
Description: Be specific and physical. Concrete detail — what something weighs, smells like, sounds like — beats adjectives. Show the character's state through what they do, not by naming the emotion.
Dialogue: Write it like a person actually talking. Real speech is shorter than written prose: it contracts, trails off, interrupts itself, and leaves things unsaid. Nobody delivers a monologue in conversation.
Avoid: Lines that sound like a novel's narration rather than speech. Naming an emotion instead of showing it. Filling a reply with description when something should happen.`

// WritingStyle is a named set of instructions for how the prose should sound.
// The structural framing above is unaffected by it, so a style can change the
// voice of a scene without breaking the format the transcript is rendered from.
type WritingStyle struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
}

// DefaultStyle returns the built-in style.
func DefaultStyle() WritingStyle {
	return WritingStyle{Name: DefaultStyleName, Instructions: defaultStyleInstructions}
}

// Resolved returns the style's instructions, falling back to the default when
// a style is missing or empty — a scene with no style guidance at all drifts
// into summary within a few turns.
func (w WritingStyle) Resolved() string {
	if s := strings.TrimSpace(w.Instructions); s != "" {
		return s
	}
	return defaultStyleInstructions
}

// BuildSystem assembles the system message for a character. When the card
// carries its own system prompt that is used verbatim — the card author chose
// it deliberately — and Astral's framing is skipped.
func BuildSystem(c Character, p Persona) string {
	userName := p.Name
	if userName == "" {
		userName = DefaultPersonaName
	}
	sub := func(s string) string { return Substitute(s, c.Name, userName) }

	var b strings.Builder
	b.WriteString(strings.TrimSpace(sprintf3(framingStructure, c.Name)))
	b.WriteString("\n\nHOW TO WRITE IT\n")
	// Substituted like everything else. A style is written once and applied to
	// every character, so "{{char}} never uses contractions" is exactly the
	// sort of thing it should be able to say — and it reached the model as the
	// literal text "{{char}}" until this was fixed.
	b.WriteString(sub(p.Style.Resolved()))
	b.WriteString("\n")
	b.WriteString(framingClose)

	section := func(heading, body string) {
		if strings.TrimSpace(body) == "" {
			return
		}
		b.WriteString("\n\n")
		b.WriteString(heading)
		b.WriteString("\n")
		b.WriteString(sub(strings.TrimSpace(body)))
	}
	section("## "+c.Name, c.Description)
	section("## Personality", c.Personality)
	section("## Scenario", c.Scenario)
	if strings.TrimSpace(p.Description) != "" {
		section("## "+userName, p.Description)
	}
	// Last, and said to outrank what came before it. Position is most of what
	// makes a model follow an instruction, so the user's own directions go at
	// the bottom of the prompt rather than buried in the middle of it.
	//
	// Global first, then this character's — the specific one comes later and
	// so wins when the two disagree.
	if ins := allInstructions(c, p); ins != "" {
		b.WriteString("\n\n## Instructions\nThese come from the user and take priority over the general guidance above. Follow them exactly.\n")
		b.WriteString(sub(ins))
	}
	return b.String()
}

// allInstructions merges the global instructions with this character's.
func allInstructions(c Character, p Persona) string {
	parts := make([]string, 0, 2)
	for _, s := range []string{p.GlobalInstructions, c.Instructions} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

// sprintf3 fills the framing template, which uses the character's name three
// times. A tiny helper rather than fmt.Sprintf so the template cannot silently
// acquire a fourth verb and start printing %!s(MISSING) into a system prompt.
func sprintf3(tmpl, name string) string {
	if name == "" {
		name = "the character"
	}
	out := tmpl
	for i := 0; i < 3; i++ {
		out = strings.Replace(out, "%s", name, 1)
	}
	return out
}

// exampleTurns parses a card's mes_example into real messages. The spec's
// format is <START> separating blocks, then "{{user}}: …" / "{{char}}: …"
// lines. Passing these as genuine user/assistant turns teaches the model the
// voice far better than pasting the whole blob into the system prompt, because
// it demonstrates the turn structure at the same time.
func exampleTurns(c Character, userName string) []ollama.Message {
	raw := strings.TrimSpace(c.MesExample)
	if raw == "" {
		return nil
	}
	raw = Substitute(raw, c.Name, userName)

	var out []ollama.Message
	var role, buf string
	flush := func() {
		if role != "" && strings.TrimSpace(buf) != "" {
			out = append(out, ollama.Message{Role: role, Content: strings.TrimSpace(buf)})
		}
		buf = ""
	}
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.EqualFold(t, "<START>"):
			flush()
			role = ""
		case hasPrefixFold(t, userName+":"):
			flush()
			role, buf = ollama.RoleUser, strings.TrimSpace(t[len(userName)+1:])
		case hasPrefixFold(t, c.Name+":"):
			flush()
			role, buf = ollama.RoleAssistant, strings.TrimSpace(t[len(c.Name)+1:])
		default:
			if role != "" {
				buf += "\n" + line
			}
		}
	}
	flush()
	// An odd trailing turn teaches a shape the model then tries to complete.
	// Better to drop one example than to imply the transcript ends mid-pair.
	if len(out) > 0 && out[len(out)-1].Role == ollama.RoleUser {
		out = out[:len(out)-1]
	}
	return out
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// exampleCutoff is how many real turns must exist before the card's example
// dialogue stops being sent. Six is two or three exchanges — enough for the
// transcript itself to establish the voice.
//
// It is a hard threshold rather than a gradual taper on purpose: changing the
// start of the prompt invalidates Ollama's cached prefix and forces a full
// re-read of the context, so this should happen exactly once in a scene.
const exampleCutoff = 6

// historyBudgetChars caps how much transcript is sent, measured in characters
// because counting real tokens would mean shipping a tokenizer per model.
// Four characters per token is the usual rough ratio, so this is on the order
// of 6k tokens — comfortably inside the 8k default context with room left for
// the system prompt and the reply.
const historyBudgetChars = 24000

// trimHistory keeps the most recent turns that fit in the budget.
//
// Without it a long scene silently outgrows the context window, and what
// happens then is worse than forgetting: the server drops the *front* of the
// prompt, which is the system framing and the character themselves, so the
// model keeps the small talk and loses who it is playing.
func trimHistory(history []ollama.Message, budget int) []ollama.Message {
	total := 0
	for _, m := range history {
		total += len(m.Content)
	}
	if total <= budget {
		return history
	}
	// Walk back from the newest until the budget is spent.
	kept := 0
	start := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		if kept+len(history[i].Content) > budget {
			break
		}
		kept += len(history[i].Content)
		start = i
	}
	if start >= len(history) { // a single turn larger than the whole budget
		return history[len(history)-1:]
	}
	return history[start:]
}

// BuildMessages assembles the full request: system framing, the recap of
// anything compacted away, the card's example turns, the live transcript, and
// finally the closing reminder. history is the conversation so far, oldest
// first, and recap is the running record of what came before it (empty until
// a scene has outgrown the context window — see compact.go).
func BuildMessages(c Character, p Persona, recap string, history []ollama.Message) []ollama.Message {
	userName := p.Name
	if userName == "" {
		userName = DefaultPersonaName
	}
	msgs := []ollama.Message{{Role: ollama.RoleSystem, Content: BuildSystem(c, p)}}

	// The recap sits before the transcript, in the position the turns it
	// replaces used to occupy, so the scene still reads in order.
	if r := strings.TrimSpace(recap); r != "" {
		msgs = append(msgs, ollama.Message{
			Role: ollama.RoleSystem,
			Content: "Earlier in this scene (a record of what happened before the messages below; " +
				"treat all of it as established fact):\n" + Substitute(r, c.Name, userName),
		})
	}

	// Example dialogue is there to teach the character's voice before there is
	// any. Once the scene has run a few turns the real transcript does that
	// job better, and the examples are pure cost — they are among the most
	// token-heavy parts of a card and they are re-sent on every single turn.
	if len(history) < exampleCutoff {
		msgs = append(msgs, exampleTurns(c, userName)...)
	}
	history = trimHistory(history, historyBudgetChars)
	for _, m := range history {
		m.Content = Substitute(m.Content, c.Name, userName)
		msgs = append(msgs, m)
	}
	// A closing reminder, after the transcript. This position matters more
	// than any other: a model weights the end of its context far above the
	// middle, so by turn thirty a system prompt thousands of tokens back is
	// competing with everything that has happened since. Restating the two
	// things that actually drift — who they are, and what you asked for — is
	// the cheapest anti-drift measure there is.
	if r := closingReminder(c, p, userName); r != "" {
		msgs = append(msgs, ollama.Message{Role: ollama.RoleSystem, Content: r})
	}
	return msgs
}

// closingReminder builds the end-of-context nudge.
func closingReminder(c Character, p Persona, userName string) string {
	name := c.Name
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Reminder: you are ")
	b.WriteString(name)
	b.WriteString(". Stay in character, write only ")
	b.WriteString(name)
	b.WriteString("'s words and actions, and never write for ")
	b.WriteString(userName)
	b.WriteString(".")
	if ins := allInstructions(c, p); ins != "" {
		b.WriteString("\n\nFollow these instructions exactly:\n")
		b.WriteString(Substitute(ins, c.Name, userName))
	}
	b.WriteString("]")
	return b.String()
}

// Greeting is the character's opening line, with placeholders expanded.
func Greeting(c Character, p Persona) string {
	userName := p.Name
	if userName == "" {
		userName = DefaultPersonaName
	}
	return Substitute(strings.TrimSpace(c.FirstMes), c.Name, userName)
}
