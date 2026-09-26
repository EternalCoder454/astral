// Package eval also holds a judge: a model asked a narrow, closed question
// about another model's output.
//
// Whether a reply wrote the user's actions, broke character, or resolved a
// thread too fast are the complaints people actually have, and no substring
// search answers them. A comparison instrument, not a grader: the number is
// meaningless alone and useful only against the same question asked of a
// different prompt.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"astral/internal/ollama"
)

// verdictSchema keeps the answer to one boolean and one piece of evidence.
// Asking for the evidence is not decoration: a judge made to point at the text
// agrees with itself far more often than one allowed to answer yes or no and
// move on.
var verdictSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "quote":   {"type": "string"},
    "verdict": {"type": "boolean"}
  },
  "required": ["quote", "verdict"]
}`)

// Verdict is one judged reply.
type Verdict struct {
	// Quote is the passage the judge says proves it, or empty.
	Quote string `json:"quote"`
	// Verdict is true when the thing asked about did happen.
	Verdict bool `json:"verdict"`
	// Fabricated records that the judge returned evidence which is not in the
	// passage. The verdict is forced to false, and a judge that does this
	// often is not fit for the job. A 4B did it on the first passage it saw.
	Fabricated bool `json:"-"`
}

const judgeSystem = `You check one narrow property of a passage of roleplay prose and answer with JSON.

You are not judging whether the writing is good, in character, or enjoyable. You are answering exactly the question asked, about exactly the text given, and nothing else.

Answer in two fields:
- quote: the shortest run of words from the passage that settles the question. Copy it exactly. If the answer is no, leave it empty.
- verdict: true if the thing asked about happened, false if it did not.

Quote first and decide second. If you cannot find words in the passage that settle it, the answer is false.`

// wroteForUserQuestion is the check behind the oldest complaint in roleplay:
// the model taking your character's turn for you.
//
// The distinction it has to draw is the whole difficulty. A character may look
// at you, speak to you, react to you and describe you — that is their turn. A
// character may not decide what you did, said, thought or felt. The examples
// are there because the rule stated abstractly gets read as "any mention of
// the user", which is wrong and would fail every well-written reply.
const wroteForUserQuestion = `Question: does this passage decide %[1]s's actions, words, thoughts or feelings for them?

%[1]s is played by a person, not by the writer of this passage. The writer plays %[2]s only.

This IS deciding for them:
  "%[1]s nodded and sat down."        (chose their action)
  "You felt the cold settle in."      (chose their feeling)
  "%[1]s said he would come."         (chose their words)

This is NOT deciding for them, and is correct writing:
  "%[2]s watched %[1]s shake the rain off."   (describing what was already established)
  "Sit down," she said to %[1]s.               (speaking to them)
  "Sit."                                       (an order, which %[1]s may still refuse)
  "Come here. Now."                            (still an order, still refusable)
  "%[2]s wondered whether %[1]s would answer." (her own thoughts about them)
  "%[2]s held out the chart to %[1]s."         (her action, directed at them)

An order is not an outcome. %[2]s may tell %[1]s to do anything at all; what
she may not do is settle whether it was done. "Sit." is correct writing.
"Sit," she said, and %[1]s sat. is not, because of the second half.

Passage:
---
%[3]s
---`

// Judge asks a model one closed question about a passage.
//
// passage is the text being judged. It is passed separately from the question
// only so the quote can be checked against it: a small model asked for
// evidence will happily produce a sentence that is not in the passage at all,
// which was measured, and a verdict resting on invented evidence is worse than
// no verdict because it looks like one.
func Judge(ctx context.Context, client *ollama.Client, model, question, passage string) (Verdict, error) {
	raw, _, err := client.Structured(ctx, model, []ollama.Message{
		{Role: ollama.RoleSystem, Content: judgeSystem},
		{Role: ollama.RoleUser, Content: question},
	}, ollama.Options{NumCtx: 8192, Temperature: 0}, verdictSchema)
	if err != nil {
		return Verdict{}, err
	}
	var v Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return Verdict{}, fmt.Errorf("judge returned unusable JSON: %w", err)
	}
	// A verdict of true has to rest on words that are actually there. The
	// schema can require a quote; only this can require it to be real.
	if v.Verdict {
		q := strings.TrimSpace(strings.Trim(v.Quote, `*"'`))
		if q == "" {
			v.Fabricated = true
			v.Verdict = false
		} else if passage != "" && !strings.Contains(norm(passage), norm(q)) {
			v.Fabricated = true
			v.Verdict = false
		}
	}
	return v, nil
}

// norm flattens whitespace and markup so a quote that is right about the words
// is not rejected for being wrong about the asterisks.
func norm(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("*", "", "\u201c", `"`, "\u201d", `"`, "\u2019", "'").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// WroteForUser asks whether a reply took the user's turn.
func WroteForUser(ctx context.Context, client *ollama.Client, model, reply, userName, charName string) (Verdict, error) {
	return Judge(ctx, client, model, fmt.Sprintf(wroteForUserQuestion, userName, charName, reply), reply)
}
