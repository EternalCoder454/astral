package chars

import (
	"context"
	"strings"

	"astral/internal/ollama"
)

// A plain conversation is not a degenerate roleplay, and treating it as one
// showed. It had a single sentence of framing, no idea who it was talking to,
// and — because every path that keeps a long conversation alive was written for
// a scene with a character in it — no recap at all. A general chat that outgrew
// the window simply lost its beginning, which for a conversation that had been
// working through something is the part worth keeping.
//
// This file gives it the two things it was missing: the user, and a memory.

// AssistantSystemFor frames a plain conversation.
//
// It carries who the user is and the rules they set, which the bare
// AssistantSystem did not. Standing rules called standing that stopped applying
// the moment you opened a general chat were a promise the app was not keeping:
// "be concise" and "British spelling" are exactly the rules someone writes once
// and wants everywhere.
//
// The writing style is deliberately not sent. A style describes prose — tense,
// dialogue, what to avoid — and a plain answer is not prose.
func AssistantSystemFor(p Persona) string {
	var b strings.Builder
	b.WriteString(AssistantSystem)

	name := strings.TrimSpace(p.Name)
	desc := strings.TrimSpace(p.Description)
	if name != "" || desc != "" {
		b.WriteString("\n\n## Who you are talking to\n")
		switch {
		case name != "" && desc != "":
			b.WriteString(name)
			b.WriteString(". ")
			b.WriteString(desc)
		case name != "":
			b.WriteString(name)
			b.WriteString(".")
		default:
			b.WriteString(desc)
		}
	}

	if rules := strings.TrimSpace(p.GlobalInstructions); rules != "" {
		b.WriteString("\n\n## Instructions\nThese come from the user and take priority over the guidance above. Follow them exactly.\n")
		b.WriteString(rules)
	}
	return b.String()
}

// compactPlainSystem frames the summarizer for a conversation rather than a
// scene.
//
// It is a separate prompt because the roleplay one asks for the wrong things.
// That one preserves who anyone is to anyone else, the state of the
// relationship, and where everyone is standing, which is exactly right for a
// scene and useless for an hour spent working through a problem. What matters
// here is what was decided, what was established, and what is still open.
const compactPlainSystem = `You maintain a running record of a conversation, so it can continue after older messages are dropped from the model's memory.

Write a factual record, not prose. No preamble, no summary of the summary. This is notes, and it will be read by a model that needs to know what has already been said.

Preserve, in this order of importance:
- Anything decided, chosen, or agreed, and the reason for it. A decision whose reason is lost gets reopened.
- Anything established as fact: names, numbers, versions, paths, constraints, what was tried and what it did.
- Anything the user asked for and has not had yet, and anything left unfinished or unanswered.
- Corrections. If the user said something was wrong, the correction is what is true, and the original is not worth keeping.
- What the conversation is for, if that ever became clearer than it was at the start.

Drop: pleasantries, restatements, and anything the user has since replaced. Never drop a specific value to make room for a general statement. Keep exact names, numbers and paths exactly as they were written; they are the part that cannot be reconstructed.

Write in past tense, as short declarative statements. Be specific: "chose SQLite over a flat file because the index has to survive a crash" is useful, "discussed storage options" is not.`

// CompactPlain folds the aged-out turns of a plain conversation into a recap.
//
// Everything about it is the roleplay version's, except which questions the
// summarizer is asked. That includes the second attempt when the record comes
// back nearly as long as what it replaced, which is a failure mode of the
// summarizer rather than of the material.
func CompactPlain(ctx context.Context, client *ollama.Client, model, previous string, aged []ollama.Message, p Persona, opts ollama.Options, budget Budget) (string, error) {
	if len(aged) == 0 {
		return previous, nil
	}
	if model == "" {
		return previous, errNoModel
	}

	prompt := compactPlainPrompt(previous, aged, p)
	opts.Temperature = 0.2
	opts.NumPredict = recapReplyTokens

	out, err := askForRecord(ctx, client, model, compactPlainSystem, prompt, opts)
	if err != nil {
		return previous, err
	}
	if len(out)*2 >= totalChars(aged) {
		again, err := askForRecord(ctx, client, model, compactPlainSystem+"\n"+recordAgain, prompt, opts)
		if err == nil && len(again) > 0 && len(again) < len(out) {
			out = again
		}
	}
	return truncateRecap(out, budget.Recap), nil
}

// compactPlainPrompt lays out the record so far and the turns to fold into it.
func compactPlainPrompt(previous string, aged []ollama.Message, p Persona) string {
	userName := strings.TrimSpace(p.Name)
	if userName == "" {
		userName = DefaultPersonaName
	}

	var b strings.Builder
	if prev := strings.TrimSpace(previous); prev != "" {
		b.WriteString(markerRecord)
		b.WriteString("\n")
		b.WriteString(prev)
		b.WriteString("\n\n")
		b.WriteString(markerNext)
		b.WriteString("\n")
	} else {
		b.WriteString(markerFirst)
		b.WriteString("\n")
	}
	for _, m := range aged {
		who := "Assistant"
		if m.Role == ollama.RoleUser {
			who = userName
		}
		b.WriteString(who)
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString("\n\n")
	}
	b.WriteString("Now write the updated record, covering everything above. ")
	b.WriteString("Replace the earlier record rather than adding to it: what you write is all that will be kept.")
	return b.String()
}
