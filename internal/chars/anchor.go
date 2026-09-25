package chars

import (
	"strings"

	"astral/internal/ollama"
)

// A model weights the end of its context far above the middle. By turn thirty
// a system prompt thousands of tokens back is competing with everything that
// has happened since — and losing, because the strongest instruction in the
// context is not an instruction at all. It is the transcript: twenty replies
// the model wrote itself, demonstrating what is acceptable here.
//
// That is why changing the writing style used to do so little. The style sat
// once, near the front, while the scene below it held twenty worked examples
// of the old one. And it is why narration drifts out of *asterisks*: the rule
// is stated once at the top, and the first reply that ignores it becomes
// precedent for every reply after.
//
// The anchor is the counterweight. It restates the three things that actually
// drift — who is speaking, how the prose should sound, and how it is marked up
// — in the last position before the model writes. It costs its own length in
// tokens every turn, which is the price of the only position that works.

// anchorFormat is the markup rule, restated with the example that makes it
// unambiguous. The example matters more than the rule: a model shown the shape
// reproduces it, where a model told about it often does not.
const anchorFormat = `FORMAT — every sentence is one of exactly two things, and there is no third kind: spoken aloud in "double quotes", or everything else in *single asterisks*. Never write an unmarked sentence; every paragraph starts with a quote or an asterisk. Example:
*She did not look up from the chart.* "You're late."
*A pin went into the table rather than the map.* "Sit."`

// anchorFormatFirm is used when the recent transcript shows the rule has
// already slipped. Restating it more forcefully only where it is being
// disobeyed keeps the usual case cheap, and stops as soon as the model
// complies.
const anchorFormatFirm = `FORMAT — your recent replies have been getting this wrong, so correct it now. Every sentence is one of exactly two things and there is no third kind: spoken aloud in "double quotes", or everything else — narration, action, body language, thought — wrapped in *single asterisks*. Go paragraph by paragraph: each one must start with a quote or an asterisk, and no sentence may be left unmarked. Example:
*She did not look up from the chart. The rain had found the window again, and she let it.* "You're late."
*A pin went into the table rather than the map, a small and deliberate violence.* "Sit. You're dripping on the Sever."`

// Anchor builds the closing block: the last thing in the context before the
// model writes its reply.
func Anchor(c Character, sc Scene, userName string) string {
	if c.Name == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Before you write, re-read this. It applies to your next reply and outranks the pattern of the messages above.\n\n")

	b.WriteString("You are ")
	b.WriteString(c.Name)
	b.WriteString(". Write only ")
	b.WriteString(c.Name)
	b.WriteString("'s words and actions. Never write, decide or narrate ")
	b.WriteString(userName)
	b.WriteString("'s words, thoughts or actions.\n\n")

	if sc.NarrationDrifted {
		b.WriteString(anchorFormatFirm)
	} else {
		b.WriteString(anchorFormat)
	}
	b.WriteString("\n\n")

	// The style, in full, in the position where it is actually obeyed. Stating
	// it once at the front and hoping was the whole problem.
	if sc.StyleChanged {
		// The single most useful sentence in the block. Without it the model
		// reads twenty of its own replies as the house style and writes a
		// twenty-first to match, whatever the instructions say.
		b.WriteString("STYLE — this has changed. The messages above were written to a different style; do not imitate them. From this reply on, write like this:\n")
	} else {
		b.WriteString("STYLE — follow this exactly, even where the messages above do not:\n")
	}
	b.WriteString(Substitute(sc.Persona.Style.Resolved(), c.Name, userName))

	if ins := allInstructions(c, sc.Persona); ins != "" {
		b.WriteString("\n\nThe user's own instructions, which outrank everything else here:\n")
		b.WriteString(Substitute(ins, c.Name, userName))
	}

	// Last of all, and so weighted most. A direction is about where the scene
	// is going rather than how it is written, which is why it sits apart from
	// the instructions above it.
	//
	// The wording works hard on two failure modes. A model handed "she is
	// about to realise he lied" will otherwise write exactly that sentence, in
	// narration, this turn — announcing the thing instead of playing it — and
	// will treat the whole direction as something to finish within one reply.
	if d := strings.TrimSpace(sc.Direction); d != "" {
		b.WriteString("\n\nDIRECTION — where the user wants this scene to go. Your next reply " +
			"must take a visible step toward it: have the character say or do something that " +
			"moves it along, in this reply, not a later one. Do not state the direction itself " +
			"and do not have anyone name it outright, and do not resolve the whole thing at " +
			"once. One step, now:\n")
		b.WriteString(Substitute(d, c.Name, userName))
	}
	b.WriteString("]")
	return b.String()
}

// narrationScanTurns is how many of the character's recent replies are checked
// for the markup rule. Three is enough to tell a drift from a reply that was
// legitimately all dialogue.
const narrationScanTurns = 3

// unmarkedRunChars is how much unquoted, unasterisked prose a reply may carry
// before it counts as unmarked narration, counted without spaces.
//
// Forty is about eight words: past anything a stray connective or a piece of
// punctuation between two quoted lines could account for, and short of a
// sentence of narration. It was sixty, which let a full sentence of unmarked
// prose through — the exact case this is for.
const unmarkedRunChars = 40

// NarrationDrifted reports whether the character's recent replies have stopped
// marking narration with asterisks.
//
// It is deliberately not "does this reply contain an asterisk". A reply that
// is entirely dialogue is correct and contains none, and punishing it would
// make the prompt nag at exactly the wrong moment. What is measured instead is
// prose that is neither spoken nor marked — which is the actual mistake.
func NarrationDrifted(history []ollama.Message) bool {
	checked, bad := 0, 0
	for i := len(history) - 1; i >= 0 && checked < narrationScanTurns; i-- {
		if history[i].Role != ollama.RoleAssistant {
			continue
		}
		body := strings.TrimSpace(history[i].Content)
		if body == "" {
			continue
		}
		checked++
		if unmarkedProse(body) >= unmarkedRunChars {
			bad++
		}
	}
	// Every recent reply, not merely most: one slip is noise, and escalating
	// the prompt on noise would leave the firmer wording switched on forever.
	return checked > 0 && bad == checked
}

// unmarkedProse returns how many characters of a reply sit outside both
// *asterisks* and "quotes".
func unmarkedProse(s string) int {
	n := 0
	inStars, inQuotes := false, false
	for _, r := range s {
		switch {
		case r == '*':
			inStars = !inStars
			continue
		case r == '"' || r == '“' || r == '”':
			inQuotes = !inQuotes
			continue
		}
		if inStars || inQuotes {
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' {
			continue
		}
		n++
	}
	return n
}

// NarrationPrefill is an assistant turn left open mid-narration.
//
// Appending it to the messages hands the model a reply that has already begun
// inside an asterisk span, so the next token it writes is narration whether or
// not it intended to mark any. It is the one measure that recovers a scene
// whose own transcript has taught it otherwise: on a 24B roleplay finetune
// with four unmarked replies behind it, the firmer wording alone recovered
// nothing and this recovered all of it.
//
// It is used only when drift has been detected. A prefilled turn costs a
// little of the model's freedom over how to open, and that is not a trade
// worth making on a scene that is behaving.
const NarrationPrefill = "*"

// RestorePrefill puts the prefill back on the front of a reply that continued
// it, and leaves the reply alone when it did not.
//
// Not every chat template continues a trailing assistant turn; some close it
// off, and the model then writes a fresh, already-marked reply. Prepending in
// that case would produce one stray asterisk and a transcript full of broken
// markup, so the two cases are told apart by counting: a reply that continued
// an open span has an odd number of asterisks, and restoring the prefill makes
// it even. A reply that started fresh is already balanced, and is returned
// untouched.
func RestorePrefill(reply string) string {
	if reply == "" {
		return reply
	}
	if strings.Count(reply, "*")%2 == 0 {
		return reply // already balanced: the template closed the prefill off
	}
	return NarrationPrefill + reply
}
