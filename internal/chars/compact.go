package chars

import (
	"context"
	"fmt"
	"strings"

	"astral/internal/ollama"
)

// A long scene outgrows the context window. What happened before was simply
// dropped: the oldest turns fell off the front and, as far as the model was
// concerned, had never happened. Names, promises and everything established in
// the first hour of a roleplay quietly stopped existing.
//
// Compaction replaces that with a running recap. The aged-out turns are
// summarized into a factual record, the recap is carried at the top of every
// subsequent turn, and only the recent transcript is sent word-for-word. The
// scene keeps its history; it just stops paying full price for it.

// The thresholds are no longer constants. They come from Plan, which divides
// the context window between the parts of a prompt, because a fixed 20,000
// characters is either wasteful in a 32k window or a silent overflow in a 4k
// one. See budget.go.

// compactSystem frames the summarizer. It is deliberately not asked for prose:
// a recap written as narration reads like part of the scene and the model
// continues it, which is not what it is for.
const compactSystem = `You maintain a running record of a roleplay scene, so the story can continue after older messages are dropped from the model's memory.

Write a factual record, not prose. No scene-setting, no dialogue, no style. This is notes, and it will be read by a model that needs to know what is true.

Preserve, in this order of importance:
- Names, and who anyone is to anyone else.
- Anything established as fact about a character: appearance, history, what they can and cannot do, what they have admitted or hidden.
- Anything concealed, refused, avoided, or asked not to be mentioned. These are the details a story later turns on, and they are the easiest to mistake for unimportant.
- The state of the relationship, and how it got there.
- Promises, threats, plans, and anything left unresolved.
- Where everyone is, and what they were doing when this record ends.

A detail that happened once is far more likely to matter than one that repeats. If a character spent twenty messages working at a desk and one message hiding something, the hiding is the part worth recording.

Drop: exact wording, repeated description, and atmosphere. Never drop a specific fact to make room for a general one.

Write in past tense, third person, as short declarative statements. Use the characters' names, never "he" or "she" where it could be ambiguous. Be specific: "Maya admitted she has never left the city" is useful, "they talked about the past" is not.`

// Compact folds a batch of aged-out turns into the recap.
//
// previous is the recap so far, and may be empty on the first compaction. The
// result replaces it — this is a rolling summary, so detail from much earlier
// in the scene survives by being carried forward through each pass rather than
// by keeping the original turns.
func Compact(ctx context.Context, client *ollama.Client, model, previous string, aged []ollama.Message, c Character, p Persona, opts ollama.Options, budget Budget) (string, error) {
	if len(aged) == 0 {
		return previous, nil
	}
	if model == "" {
		return previous, fmt.Errorf("no model selected")
	}

	msgs := []ollama.Message{
		{Role: ollama.RoleSystem, Content: compactSystem},
		{Role: ollama.RoleUser, Content: compactPrompt(previous, aged, c, p)},
	}

	// Low temperature: this is bookkeeping. A summary that invents a detail is
	// worse than no summary, because it becomes fact for the rest of the scene.
	opts.Temperature = 0.2
	// repeat_last_n is deliberately left as it comes.
	//
	// Widening it to cover the whole record was the obvious answer to a
	// summariser that circles, and it does nothing: eighteen runs across two
	// models, nine a side, gave mean records of 2143 characters at the default
	// and 2219 widened, with the same two runs in six circling either way.
	// TestMeasureCompaction is the instrument. What actually pays is throwing
	// the repeats away afterwards, below.
	// The recap is bounded afterwards anyway, so the reply limit only has to
	// stop a model that will not stop on its own. Leaving it unset let a
	// verbose model spend minutes writing a record that was then cut to a
	// fraction of its length.
	opts.NumPredict = recapReplyTokens

	noThink := false
	reply, _, err := client.Chat(ctx, model, msgs, opts, &noThink, nil)
	if err != nil {
		return previous, err
	}
	out := strings.TrimSpace(reply.Content)
	if out == "" {
		return previous, fmt.Errorf("the model returned an empty record")
	}
	return truncateRecap(dedupeRecap(out), budget.Recap), nil
}

// compactPrompt writes the summariser's instructions: who is in the scene, the
// record so far, and the turns to fold into it.
//
// Separate from Compact so that a measurement run can send the same prompt with
// different sampler settings and compare what comes back, which is the only way
// to find out whether a setting helped.
func compactPrompt(previous string, aged []ollama.Message, c Character, p Persona) string {
	userName := p.Name
	if userName == "" {
		userName = DefaultPersonaName
	}

	var b strings.Builder
	b.WriteString("The two characters are ")
	b.WriteString(c.Name)
	b.WriteString(" and ")
	b.WriteString(userName)
	b.WriteString(".\n\n")
	if prev := strings.TrimSpace(previous); prev != "" {
		b.WriteString("Record so far:\n")
		b.WriteString(prev)
		b.WriteString("\n\nWhat happened next:\n")
	} else {
		b.WriteString("What happened:\n")
	}
	for _, m := range aged {
		who := c.Name
		if m.Role == ollama.RoleUser {
			who = userName
		}
		b.WriteString(who)
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString("\n\n")
	}
	b.WriteString("Now write the updated record, covering everything above. ")
	b.WriteString("Cover every specific fact, however small, and leave out the atmosphere. ")
	b.WriteString("Keep it under 400 words. Output only the record.")
	return b.String()
}

// dedupeRecap drops a statement the record has already made.
//
// This is the cheap half of the repetition problem. The sampler setting above
// makes circling less likely; this makes the circling that still happens cost
// nothing, because the recap is carried in the prompt on every later turn, so
// a sentence written twice is paid for on every turn until the scene ends.
//
// Only exact repeats go, compared with case and spacing ignored. Two
// statements of the same fact in different words are not touched: telling them
// apart needs to understand them, and getting it wrong loses a fact, which is
// the one thing a recap must not do.
func dedupeRecap(s string) string {
	parts := splitStatements(s)
	if len(parts) < 2 {
		return s
	}
	seen := make(map[string]bool, len(parts))
	var b strings.Builder
	b.Grow(len(s))
	// carry remembers a line break that belonged to a statement being dropped,
	// so removing a repeat cannot also remove the shape of the list it was in.
	carry, last := "", ""
	for _, part := range parts {
		key := recapKey(part.text)
		if key == "" || seen[key] {
			if part.before == "\n" {
				carry = "\n"
			}
			continue
		}
		seen[key] = true
		if b.Len() > 0 {
			b.WriteString(joinWith(part.before, carry, last))
		}
		carry, last = "", part.text
		b.WriteString(part.text)
	}
	return b.String()
}

// joinWith picks the separator between two kept statements.
//
// A line break wins over a space, whether it was this statement's own or one
// inherited from a repeat that was dropped between them. And a statement that
// does not end in a full stop gets a line break after it regardless, because a
// space there would run it into the next one and the two would afterwards read
// as a single statement. Real records end their sentences, so this only fires on
// text that was never a record.
func joinWith(before, carry, last string) string {
	if before == "\n" || carry == "\n" || !endsStatement(last) {
		return "\n"
	}
	return " "
}

func endsStatement(s string) bool {
	if s == "" {
		return false
	}
	switch s[len(s)-1] {
	case '.', '!', '?', '"', ')', '\'':
		return true
	}
	return false
}

// recapKey is what two statements are compared by: their words, lower case,
// without the punctuation that ends or bullets them. A statement that is
// nothing but punctuation has no key and is dropped.
func recapKey(s string) string {
	return strings.Trim(strings.ToLower(strings.Join(strings.Fields(s), " ")), ".!?-• ")
}

// statement is one comparable unit of a record, and what separated it from the
// one before. The separator is carried so that a record written as a list is
// still a list afterwards: joining everything with spaces would fold a set of
// bullets into one paragraph.
type statement struct {
	text   string
	before string
}

// splitStatements cuts a record into the units that can be compared: one
// sentence, or one line of a list.
//
// It splits on a full stop followed by whitespace, which will also cut an
// abbreviation in half. That is harmless here: the pieces are only ever used to
// find exact duplicates, so a wrong boundary means a duplicate is missed, not
// that anything is lost.
func splitStatements(s string) []statement {
	var out []statement
	start, sep := 0, ""

	// cut closes the statement ending at textEnd, then steps over the
	// whitespace after it, noting whether a line ended there.
	cut := func(textEnd int) int {
		if piece := strings.TrimSpace(s[start:textEnd]); piece != "" {
			out = append(out, statement{text: piece, before: sep})
			sep = " "
		}
		j, newline := textEnd, false
		for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
			if s[j] == '\n' {
				newline = true
			}
			j++
		}
		if newline {
			sep = "\n"
		}
		start = j
		return j
	}

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '.', '!', '?':
			if i+1 < len(s) && s[i+1] != ' ' && s[i+1] != '\n' && s[i+1] != '\t' && s[i+1] != '\r' {
				continue // a decimal point, or the first dot of an ellipsis
			}
			if i > 0 && s[i-1] == '.' {
				continue // the last dot of an ellipsis, which ends nothing
			}
			i = cut(i+1) - 1
		case '\n':
			i = cut(i) - 1
		}
	}
	cut(len(s))
	return out
}

// recapReplyTokens caps the summariser's own reply. The prompt asks for under
// four hundred words, which is well inside this; the limit is only there so a
// model that ignores the word count cannot run for minutes producing text that
// is about to be truncated anyway.
const recapReplyTokens = 700

// truncateRecap bounds the recap, cutting at a sentence end so it does not
// stop mid-fact.
func truncateRecap(s string, budget int) string {
	if budget <= 0 {
		budget = DefaultBudget().Recap
	}
	if len(s) <= budget {
		return s
	}
	cut := s[:budget]
	if i := strings.LastIndexAny(cut, ".!?"); i > budget/2 {
		return cut[:i+1]
	}
	return strings.TrimSpace(cut)
}

// NeedsCompaction reports whether a transcript has outgrown its budget.
func NeedsCompaction(history []ollama.Message, b Budget) bool {
	if b.Compact <= 0 {
		b = DefaultBudget()
	}
	return totalChars(history) > b.Compact
}

// SplitForCompaction divides a transcript into the part to be folded into the
// recap and the part that stays verbatim.
//
// The split lands on a turn boundary, and never leaves the recent half empty:
// a scene whose most recent single turn is larger than the whole budget still
// has to be answerable.
func SplitForCompaction(history []ollama.Message, b Budget) (aged, recent []ollama.Message) {
	if b.Compact <= 0 {
		b = DefaultBudget()
	}
	if !NeedsCompaction(history, b) {
		return nil, history
	}
	kept := 0
	split := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		if kept+len(history[i].Content) > b.Keep && split < len(history) {
			break
		}
		kept += len(history[i].Content)
		split = i
	}
	if split <= 0 {
		return nil, history // nothing old enough to be worth folding away
	}
	return history[:split], history[split:]
}

func totalChars(msgs []ollama.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
	}
	return n
}
