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
	Description string // who they are, appearance, voice, sent every turn
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
	// AvatarPath is the small square shown beside every message and in lists.
	// PortraitPath is the larger image shown alongside a scene. They are
	// separate because the crops want different things: an avatar is a face at
	// 28px, a portrait is the whole figure.
	AvatarPath   string
	PortraitPath string
	// WorldID is the setting this character belongs to, or 0 for none. A scene
	// inherits its lorebook from here.
	WorldID   int64
	Accent    int // index into the palette's secondary accents
	CreatedAt time.Time
	UpdatedAt time.Time
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
	// GlobalInstructions are the standing rules from Settings, rendered as a
	// numbered list, and apply to every character. A character's own
	// instructions are placed after these, so the specific beats the general
	// when the two disagree. See store.Config.RulesText.
	GlobalInstructions string
	// Style is the active writing style. The zero value means the default.
	Style WritingStyle
}

// DefaultPersonaName is used when the user has not named themselves.
const DefaultPersonaName = "User"

// Substitute expands {{char}} and {{user}}, which the card spec defines, plus
// the single-brace spellings people type when they have not read one. Every
// free-text field goes through it.
//
// The double-brace forms are listed first so they win: a Replacer takes the
// first pattern matching at a position, so "{{char}}" would otherwise be left
// with a stray brace.
func Substitute(s, charName, userName string) string {
	if s == "" {
		return ""
	}
	// Every placeholder form starts with one of two bytes, and ordinary
	// roleplay prose contains neither. Checking first matters because the work
	// avoided is not the replacement but the Replacer: it builds a trie over
	// fourteen patterns, and this is called once per message, so a forty-turn
	// scene was building forty tries per turn to change nothing.
	if !strings.ContainsAny(s, "{<") {
		return s
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
Write %s's words and actions only. Never write, decide, or narrate %s's words, thoughts, or actions, wait for them.
Do not summarize the scene, do not skip ahead in time, and do not end the scene on your own.

FORMATTING. Every sentence you write is one of exactly two things, and there is no third kind:
1. Spoken aloud, in "double quotes". Nothing else goes inside quotes.
2. Everything else, meaning narration, action, body language, sensory detail and %s's own thoughts, inside *single asterisks*.
Never write an unmarked sentence. Every paragraph must start with either a quote or an asterisk.
Put a blank line between beats. A reply is two or more short paragraphs, never one unbroken block.
Example of a full reply:
*She did not look up from the chart. The rain had found the window again, and she let it.* "You're late."
*A pin went into the table rather than the map, a small and deliberate violence.* "Sit. You're dripping on the Sever."`

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
Description: Be specific and physical. Concrete detail (what something weighs, smells like, sounds like) beats adjectives. Show the character's state through what they do, not by naming the emotion.
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
	b.WriteString(strings.TrimSpace(fillName(framingStructure, c.Name)))
	b.WriteString("\n\nHOW TO WRITE IT\n")
	// Substituted like everything else. A style is written once and applied to
	// every character, so "{{char}} never uses contractions" is exactly the
	// sort of thing it should be able to say — and it reached the model as the
	// literal text "{{char}}" until this was fixed.
	b.WriteString(sub(p.Style.Resolved()))
	b.WriteString("\n\n")
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

// fillName puts the character's name everywhere the framing template asks for
// it. A tiny helper rather than fmt.Sprintf so the template can gain or lose a
// slot without anyone having to remember to change a count, and so a mismatch
// can never print %!s(MISSING) into a system prompt.
func fillName(tmpl, name string) string {
	if name == "" {
		name = "the character"
	}
	return strings.ReplaceAll(tmpl, "%s", name)
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

// DefaultNumCtx is the context window assumed when a caller has no
// configuration to hand. It matches the application's own default.
const DefaultNumCtx = 8192

// trimHistory keeps the most recent turns that fit, so the server never drops
// the front of the prompt instead, which is the framing and the character.
//
// The fallback, not the plan: trimming moves every token after the cut, so a
// scene that trims every turn re-reads its whole prompt every turn. Compaction
// keeps it rare, folding a scene into its recap at three quarters of
// Budget.History.
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

// Scene is everything a turn needs besides the character: who you are, what
// is true in this world, what has already happened, and the conversation so
// far.
//
// It is a struct rather than four parameters because it has grown twice
// already, and each time every call site and test had to be rewritten for
// something that was only ever additive.
type Scene struct {
	Persona Persona
	// Lore is the block of world facts the conversation has triggered, from
	// internal/world. Empty when a character has no world.
	Lore string
	// Recap is the running record of turns compacted out of the context, from
	// compact.go. Empty until a scene has outgrown the window.
	Recap string
	// History is the conversation so far, oldest first.
	History []ollama.Message
	// Budget divides the context window between the parts of this prompt. The
	// zero value means DefaultBudget, so a caller with no configuration to
	// hand still gets a plan rather than an unbounded prompt.
	Budget Budget
	// StyleChanged says the transcript was written under a different writing
	// style. The anchor then tells the model not to imitate it, which is the
	// difference between switching styles mid-scene and merely hoping.
	StyleChanged bool
	// NarrationDrifted says the recent replies have stopped marking narration
	// with asterisks, so the format rule is restated more firmly.
	NarrationDrifted bool
	// RollCall says the recent replies in a group scene have had every
	// character speak once each, in cast order, which is what a scene with a
	// cast degenerates into. The anchor argues against it only when it is
	// happening.
	RollCall bool
	// Direction is where the user wants this scene to go next: "she is about
	// to realise he lied", "move them toward the docks". It is not a standing
	// rule like a character's instructions, it is a nudge for the next few
	// turns, and it is expected to be rewritten or cleared as the scene moves.
	Direction string
}

// BuildMessages assembles the full request.
//
// The order is for the server's prefix cache: Ollama reuses what it computed
// for however much of the prompt is byte-identical to last turn, so what does
// not change goes first and what does goes last.
//
// Lore goes last despite being true before the scene starts, because it is
// matched against what was recently said and so changes most turns. Measured
// on a twenty-turn scene, putting it first dropped the reusable prefix from
// 96%% to 12%%. At the end it costs nothing and is better obeyed.
func BuildMessages(c Character, sc Scene) []ollama.Message {
	p := sc.Persona
	history := sc.History
	userName := p.Name
	if userName == "" {
		userName = DefaultPersonaName
	}
	budget := sc.Budget
	system := BuildSystem(c, p)
	if budget == (Budget{}) {
		budget = Plan(DefaultNumCtx, 0, len(system))
	}

	// --- stable prefix: identical from turn to turn, so cached ---
	msgs := []ollama.Message{{Role: ollama.RoleSystem, Content: system}}

	// The recap sits before the transcript, in the position the turns it
	// replaces used to occupy, so the scene still reads in order. It changes
	// only when a compaction runs, which is rare enough to belong here.
	//
	// It says outright that it is not an example of how to write, and so does
	// the lore below, because both are thousands of characters of flat
	// declarative prose with no asterisks in them and the model reads
	// everything in its context as a model for what to produce. Measured over
	// twenty-turn scenes, which are the only ones that have a recap at all,
	// around seventy per cent of replies came back with unmarked narration;
	// short scenes with no recap and little lore measured none.
	if r := strings.TrimSpace(truncateTo(sc.Recap, budget.Recap)); r != "" {
		msgs = append(msgs, ollama.Message{
			Role: ollama.RoleSystem,
			Content: "Earlier in this scene. These are notes, not prose: they are written plainly " +
				"on purpose and are not an example of how to write. Treat all of it as " +
				"established fact, and do not copy the way it is written.\n" +
				Substitute(r, c.Name, userName),
		})
	}

	// Example dialogue is there to teach the character's voice before there is
	// any. Once the scene has run a few turns the real transcript does that
	// job better, and the examples are pure cost — they are among the most
	// token-heavy parts of a card and they are re-sent on every single turn.
	if len(history) < exampleCutoff {
		msgs = append(msgs, exampleTurns(c, userName)...)
	}

	// The transcript is append-only, which is the best possible shape for a
	// prefix cache: every turn adds to the end and disturbs nothing before it.
	history = trimHistory(history, budget.History)
	for _, m := range history {
		m.Content = Substitute(m.Content, c.Name, userName)
		msgs = append(msgs, m)
	}

	// --- volatile suffix: re-read every turn either way ---
	if lore := strings.TrimSpace(truncateTo(sc.Lore, budget.Lore)); lore != "" {
		msgs = append(msgs, ollama.Message{
			Role: ollama.RoleSystem,
			Content: "Reference for this world. These are established facts, true throughout, " +
				"not something that has just been said. Like the record above they are " +
				"notes rather than prose, and are not an example of how to write.\n" +
				Substitute(lore, c.Name, userName),
		})
	}
	if a := Anchor(c, sc, userName); a != "" {
		msgs = append(msgs, ollama.Message{Role: ollama.RoleSystem, Content: a})
	}
	return msgs
}

// truncateTo bounds a block to a character budget, cutting at a line break so
// a lore entry or a recap does not stop mid-fact.
func truncateTo(s string, budget int) string {
	if budget <= 0 || len(s) <= budget {
		if budget <= 0 && s != "" {
			return "" // no room planned for this part at all
		}
		return s
	}
	cut := s[:budget]
	if i := strings.LastIndexByte(cut, '\n'); i > budget/2 {
		return cut[:i]
	}
	return cut
}

// Greeting is the character's opening line, with placeholders expanded.
func Greeting(c Character, p Persona) string {
	return GreetingAt(c, p, 0)
}

// Greetings is every opening a character has: the card's first message, then
// its alternates.
//
// Cards in circulation carry several, and Astral has always imported, stored
// and exported them while only ever showing the first. A character written
// with four ways into a scene was one with one.
func Greetings(c Character) []string {
	out := make([]string, 0, 1+len(c.AltGreetings))
	if g := strings.TrimSpace(c.FirstMes); g != "" {
		out = append(out, g)
	}
	for _, g := range c.AltGreetings {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// GreetingAt is the nth opening, with the placeholders expanded. Out of range
// wraps, so a caller stepping through them needs no bounds of its own.
func GreetingAt(c Character, p Persona, n int) string {
	all := Greetings(c)
	if len(all) == 0 {
		return ""
	}
	userName := p.Name
	if userName == "" {
		userName = DefaultPersonaName
	}
	n = ((n % len(all)) + len(all)) % len(all)
	return Substitute(all[n], c.Name, userName)
}
