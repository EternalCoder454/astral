package scene

import (
	"context"

	"astral/internal/chars"
	"astral/internal/ollama"
)

// Room for the whole conversation, in the chats that are not scenes.
//
// A scene keeps to its context window by folding old turns into a recap. The
// designers, the Prompt Optimizer and plain chats do not: their whole
// conversation is the material, and an interview summarised halfway through is
// an answer lost. They were sent in the window from Settings all the same,
// 8192 tokens by default, which is about 28,000 characters, so a long .md file
// attached to a designer did not fit, and Ollama quietly dropped the start of
// the conversation to make room: the file first.
//
// So their window grows to hold what is in it. In steps rather than to the
// token, because a model is loaded again whenever its window changes, and a
// conversation that grew by a line each turn would reload it every turn.

// contextSteps are the windows a conversation grows through.
var contextSteps = []int{16384, 32768, 65536, 131072}

// maxContext is the largest window asked for, whatever the model allows.
// Past it a model's cache for the window no longer fits beside the model on a
// 24 GB card, and much of it would be read back from the CPU.
const maxContext = 131072

// estimatedTokens is a conservative count of a conversation's tokens: markdown
// and prose run about four characters a token, and counting fewer characters
// per token errs towards a window too large rather than one that cuts.
func estimatedTokens(msgs []ollama.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)*10/32 + 8
	}
	return n
}

// FitContext widens opts' window to hold msgs and the reply, when this kind of
// chat keeps its whole conversation. A scene is left to its budget.
func FitContext(ctx context.Context, client *ollama.Client, model, kind string, opts ollama.Options, msgs []ollama.Message) ollama.Options {
	if !keepsWholeConversation(kind) {
		return opts
	}
	reply := opts.NumPredict
	if reply <= 0 {
		reply = chars.DefaultReplyTokens
	}
	need := estimatedTokens(msgs) + reply + 512
	have := opts.NumCtx
	if have <= 0 {
		have = chars.DefaultNumCtx
	}
	if need <= have {
		return opts
	}
	limit := maxContext
	if client != nil {
		if n, err := client.ContextLength(ctx, model); err == nil && n > 0 && n < limit {
			limit = n
		}
	}
	opts.NumCtx = limit
	for _, step := range contextSteps {
		if step >= need && step <= limit {
			opts.NumCtx = step
			break
		}
	}
	if opts.NumCtx < have {
		opts.NumCtx = have
	}
	return opts
}

// keepsWholeConversation is every kind of chat but a scene.
func keepsWholeConversation(kind string) bool {
	return CanSearch(kind)
}
