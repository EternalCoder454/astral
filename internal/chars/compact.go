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

const (
	// CompactThresholdChars is the transcript size above which a scene is
	// compacted. Measured in characters because counting real tokens would
	// mean shipping a tokenizer per model; four characters per token is the
	// usual rough ratio, so this is on the order of 5k tokens.
	CompactThresholdChars = 20000

	// KeepVerbatimChars is how much of the recent transcript stays
	// word-for-word after a compaction. The gap between this and the
	// threshold is what one compaction reclaims — wide enough that it does
	// not have to run again on the very next turn.
	KeepVerbatimChars = 12000

	// recapBudgetChars bounds the recap itself. A recap that grows without
	// limit just becomes the problem it was introduced to solve.
	recapBudgetChars = 2400
)

// compactSystem frames the summarizer. It is deliberately not asked for prose:
// a recap written as narration reads like part of the scene and the model
// continues it, which is not what it is for.
const compactSystem = `You maintain a running record of a roleplay scene, so the story can continue after older messages are dropped from the model's memory.

Write a factual record, not prose. No scene-setting, no dialogue, no style — this is notes, and it will be read by a model that needs to know what is true.

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
func Compact(ctx context.Context, client *ollama.Client, model, previous string, aged []ollama.Message, c Character, p Persona, opts ollama.Options) (string, error) {
	if len(aged) == 0 {
		return previous, nil
	}
	if model == "" {
		return previous, fmt.Errorf("no model selected")
	}

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

	msgs := []ollama.Message{
		{Role: ollama.RoleSystem, Content: compactSystem},
		{Role: ollama.RoleUser, Content: b.String()},
	}

	// Low temperature: this is bookkeeping. A summary that invents a detail is
	// worse than no summary, because it becomes fact for the rest of the scene.
	opts.Temperature = 0.2
	opts.NumPredict = 0

	noThink := false
	reply, _, err := client.Chat(ctx, model, msgs, opts, &noThink, nil)
	if err != nil {
		return previous, err
	}
	out := strings.TrimSpace(reply.Content)
	if out == "" {
		return previous, fmt.Errorf("the model returned an empty record")
	}
	return truncateRecap(out), nil
}

// truncateRecap bounds the recap, cutting at a sentence end so it does not
// stop mid-fact.
func truncateRecap(s string) string {
	if len(s) <= recapBudgetChars {
		return s
	}
	cut := s[:recapBudgetChars]
	if i := strings.LastIndexAny(cut, ".!?"); i > recapBudgetChars/2 {
		return cut[:i+1]
	}
	return strings.TrimSpace(cut)
}

// NeedsCompaction reports whether a transcript has outgrown the threshold.
func NeedsCompaction(history []ollama.Message) bool {
	return totalChars(history) > CompactThresholdChars
}

// SplitForCompaction divides a transcript into the part to be folded into the
// recap and the part that stays verbatim.
//
// The split lands on a turn boundary, and never leaves the recent half empty:
// a scene whose most recent single turn is larger than the whole budget still
// has to be answerable.
func SplitForCompaction(history []ollama.Message) (aged, recent []ollama.Message) {
	if !NeedsCompaction(history) {
		return nil, history
	}
	kept := 0
	split := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		if kept+len(history[i].Content) > KeepVerbatimChars && split < len(history) {
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
