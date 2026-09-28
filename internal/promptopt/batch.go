package promptopt

import (
	"context"
	"errors"
	"strings"

	"astral/internal/ollama"
	"astral/internal/prompts"
	"astral/internal/websearch"
)

// Optimizing every prompt in one go.
//
// One prompt at a time, with one model: the prompts are rewritten in turn, not
// together, so only the model that is already doing the chatting is ever in
// memory. Each rewrite is one request with the same system prompt the chat
// uses, and the same tool to read the other prompts, and nothing is saved:
// the rewrites are reviewed first, because a rewrite that looks better and
// quietly drops a rule is exactly what a small model produces.

// batchRequest is the turn that asks for a rewrite with nobody there to answer
// questions.
const batchRequest = `Review this prompt now, on your own: there is nobody here to answer questions.
Most of Astral's prompts were measured and tuned already. Change this one only where a change clearly makes a model follow it better. If you cannot find such a change, say in one sentence that it is already good and give it back unchanged: that is a good answer, not a failure.
Otherwise say in one or two sentences where it is weakest, then give the complete rewrite in one ` + "```prompt" + ` block, then list what you changed, one short line each.
Give exactly one prompt block, and do not think aloud or try again in the reply.`

// Rewrite is what the optimizer made of one prompt.
type Rewrite struct {
	ID, Name string
	// Before is the prompt as it was sent when the rewrite was made; After is
	// the rewrite, empty when the reply had none.
	Before, After string
	// Reply is everything the model said, for the review.
	Reply string
	// Problems are the checks it failed; see Problems.
	Problems []string
	// Unchanged says the model judged it already as good as it could make it.
	Unchanged bool
	Err       error
}

// Summary is the first thing the model said about the prompt, for a list.
func (r Rewrite) Summary() string {
	text := r.Reply
	if i := strings.Index(text, "```"); i >= 0 {
		text = text[:i]
	}
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 220 {
		text = string(r[:220]) + "…"
	}
	return text
}

// ErrNoRewrite is a reply without a rewrite in it.
var ErrNoRewrite = errors.New("the reply had no rewrite in it")

// RewriteOne asks model to rewrite one of Astral's prompts.
func RewriteOne(ctx context.Context, client *ollama.Client, model string, opts ollama.Options, id string) Rewrite {
	p, ok := prompts.Get(id)
	if !ok {
		return Rewrite{ID: id, Err: errors.New("there is no prompt called " + id)}
	}
	r := Rewrite{ID: id, Name: p.Name, Before: prompts.Text(id)}
	think := false
	runner := &websearch.Runner{
		Client: client, Model: model, Options: opts, Think: &think,
		Extras: []websearch.Extra{ReadExtra()},
	}
	msg, _, _, err := runner.Run(ctx, []ollama.Message{
		{Role: ollama.RoleSystem, Content: System(id)},
		{Role: ollama.RoleUser, Content: batchRequest},
	}, nil)
	if err != nil {
		r.Err = err
		return r
	}
	_, r.Reply = ollama.SplitThinking(msg.Content)
	r.Reply = strings.TrimSpace(r.Reply)
	after, ok := Proposal(r.Reply)
	if !ok {
		r.Err = ErrNoRewrite
		return r
	}
	r.After = after
	r.Unchanged = normalise(after) == normalise(r.Before)
	// Checked against Astral's own version, which is what knows which slots
	// the code fills in.
	r.Problems = ProblemsFor(p, after)
	return r
}

func normalise(s string) string { return strings.Join(strings.Fields(s), " ") }
