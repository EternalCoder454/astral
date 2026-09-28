package scene

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// ChatFunc is one streamed request to the model, the shape of Client.Chat with
// the model and options already chosen.
type ChatFunc func(ctx context.Context, msgs []ollama.Message, onDelta func(ollama.Delta)) (ollama.Message, ollama.Stats, error)

// maxCuts bounds how often one reply is cut back. Each cut is a request, and
// a model that keeps finding a stock phrase is better left with one than made
// to write the same sentence ten times.
const maxCuts = 8

// holdBack is how much of a reply is kept back from the screen while it
// streams: long enough that a stock phrase is always complete, and caught,
// before any of it is shown. So a cut never has to take back text the reader
// has already seen, and neither the window nor the phone needs to know it
// happened.
const holdBack = chars.MaxStockChars + 16

// Unslop streams a reply, and when the model writes one of the stock phrases,
// cuts the reply back to just before it and has the model carry on from there
// with a note not to use it. See chars.FindStock.
//
// msgs is the request as it would otherwise be sent, which may end in an
// assistant turn the reply continues (a prefill, or the start of a reply being
// continued). The message returned holds only what came after it, as Chat's
// would.
func Unslop(ctx context.Context, chat ChatFunc, msgs []ollama.Message, onDelta func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
	return stream(ctx, chat, msgs, onDelta, len(chars.StockPhrases()) > 0)
}

// Continue streams a reply that carries on from a trailing assistant turn,
// keeping only what is new. A model whose chat template closes that turn
// writes the whole reply again instead of carrying it on; this takes the copy
// back off, so Continue works the same on every model.
func Continue(ctx context.Context, chat ChatFunc, msgs []ollama.Message, onDelta func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
	return stream(ctx, chat, msgs, onDelta, false)
}

func stream(ctx context.Context, chat ChatFunc, msgs []ollama.Message, onDelta func(ollama.Delta), unslop bool) (ollama.Message, ollama.Stats, error) {
	base := ""
	if n := len(msgs); n > 0 && msgs[n-1].Role == ollama.RoleAssistant {
		base = msgs[n-1].Content
		msgs = msgs[:n-1]
	}
	emit := func(d ollama.Delta) {
		if onDelta != nil {
			onDelta(d)
		}
	}

	allowed := map[string]bool{}
	hits := map[string]int{}
	var avoid []string
	reply := ""    // what the reply has kept so far, after base
	forwarded := 0 // how much of it has been passed on
	var thinking strings.Builder
	var total ollama.Stats

	for cuts := 0; ; {
		attemptCtx, cancel := context.WithCancel(ctx)
		prefix := base + reply
		// What this attempt has written, and the part of it that is new. A
		// model whose chat template continues a trailing reply writes only
		// the new part; one whose template closes it writes a fresh reply,
		// which in practice copies the one it was shown word for word before
		// carrying on. Either way only the new part is kept.
		var raw, fresh strings.Builder
		decided := prefix == ""
		redo := false
		checked := len(reply)
		cut, cutPhrase := -1, ""

		onChunk := func(d ollama.Delta) {
			if d.Thinking != "" {
				emit(ollama.Delta{Thinking: d.Thinking})
			}
			if d.Content == "" || cut >= 0 || redo {
				return
			}
			if decided {
				fresh.WriteString(d.Content)
			} else {
				raw.WriteString(d.Content)
				r := raw.String()
				switch {
				case len(r) <= len(prefix) && prefix[:len(r)] == r:
					return // copying so far; wait to see
				case len(r) > len(prefix) && r[:len(prefix)] == prefix:
					fresh.WriteString(r[len(prefix):]) // copied it all
				default:
					k := commonPrefix(r, prefix)
					switch {
					case k < minCopy:
						fresh.WriteString(r) // a true continuation
					case k-len(base) >= forwarded:
						// Copied part of it, then went its own way:
						// the same as a cut at that point.
						reply = prefix[len(base):k]
						fresh.WriteString(r[k:])
					default:
						// Rewrote something already shown. Asked again.
						redo = true
						cancel()
						return
					}
				}
				decided = true
			}
			text := reply + fresh.String()
			if unslop && cuts < maxCuts {
				from := max(0, checked-chars.MaxStockChars)
				if phrase, start, end := chars.FindStock(text[from:], allowed); start >= 0 {
					start, end = start+from, end+from
					switch {
					case end >= len(text):
						// At the very end, so perhaps the start of a longer
						// word. Looked at again once more has arrived.
						checked = start
					case start >= forwarded:
						cut, cutPhrase = start, phrase
						cancel()
						return
					default:
						// Already shown, which the hold back is there to
						// prevent; let it stand rather than take it back.
						allowed[phrase] = true
					}
				} else {
					checked = len(text)
				}
			}
			hold := 0
			if unslop {
				hold = holdBack
			}
			if safe := len(text) - hold; safe > forwarded {
				for safe > forwarded && safe < len(text) && !utf8.RuneStart(text[safe]) {
					safe--
				}
				emit(ollama.Delta{Content: text[forwarded:safe]})
				forwarded = safe
			}
		}

		send := withAvoid(msgs, avoid)
		if prefix != "" {
			send = append(send, ollama.Message{Role: ollama.RoleAssistant, Content: prefix})
		}
		msg, stats, err := chat(attemptCtx, send, onChunk)
		cancel()
		thinking.WriteString(msg.Thinking)
		total.Tokens += stats.Tokens
		total.Elapsed += stats.Elapsed
		total.PromptTokens, total.TokPerSec = stats.PromptTokens, stats.TokPerSec
		total.PromptTokPerSec, total.PromptElapsed = stats.PromptTokPerSec, stats.PromptElapsed
		total.DoneReason = stats.DoneReason

		if redo && ctx.Err() == nil && cuts < maxCuts {
			cuts++
			continue
		}
		text := reply + fresh.String()
		// A phrase that finished the reply was never confirmed while it
		// streamed. It is now.
		if cut < 0 && err == nil && cuts < maxCuts && unslop {
			from := max(0, checked-chars.MaxStockChars)
			if phrase, start, _ := chars.FindStock(text[from:], allowed); start >= 0 && start+from >= forwarded {
				cut, cutPhrase = start+from, phrase
			}
		}
		if cut >= 0 && ctx.Err() == nil {
			kept := strings.TrimRight(text[:cut], " \t")
			if len(kept) < forwarded {
				kept = text[:forwarded]
			}
			reply = kept
			cuts++
			hits[cutPhrase]++
			// Twice at the same kind of place is a model that means it:
			// the third time it may stand.
			if hits[cutPhrase] >= 2 {
				allowed[cutPhrase] = true
			}
			if !containsString(avoid, cutPhrase) {
				avoid = append(avoid, cutPhrase)
			}
			continue
		}

		if forwarded < len(text) {
			emit(ollama.Delta{Content: text[forwarded:]})
		}
		return ollama.Message{Role: ollama.RoleAssistant, Content: text, Thinking: thinking.String()}, total, err
	}
}

// Stream is how a turn's reply is fetched: with stock phrases cut out in a
// scene, and with Continue's copy taken off in any conversation. Anything
// else goes straight to the model.
func Stream(ctx context.Context, chat ChatFunc, kind string, msgs []ollama.Message, onDelta func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
	if kind == store.KindRoleplay || kind == "" {
		return Unslop(ctx, chat, msgs, onDelta)
	}
	if n := len(msgs); n > 0 && msgs[n-1].Role == ollama.RoleAssistant {
		return Continue(ctx, chat, msgs, onDelta)
	}
	return chat(ctx, msgs, onDelta)
}

// minCopy is how much of the reply sent back an attempt has to repeat before
// it counts as copying it rather than carrying straight on.
const minCopy = 8

// commonPrefix is how many bytes a and b share from the start.
func commonPrefix(a, b string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// withAvoid tells the closing block which stock phrases this reply has already
// been cut back from, so the carrying on does not walk straight back into one.
// Only those: naming the whole list would plant the rest.
func withAvoid(msgs []ollama.Message, avoid []string) []ollama.Message {
	out := append([]ollama.Message(nil), msgs...)
	if len(avoid) == 0 {
		return out
	}
	quoted := make([]string, len(avoid))
	for i, a := range avoid {
		quoted[i] = `"` + strings.ReplaceAll(a, "*", "…") + `"`
	}
	note := fmt.Sprintf("Do not write %s or any stock phrase like it; say it your own way.", strings.Join(quoted, " or "))
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == ollama.RoleSystem && strings.HasPrefix(out[i].Content, "[") && strings.HasSuffix(out[i].Content, "]") {
			out[i].Content = strings.TrimSuffix(out[i].Content, "]") + "\n\n" + note + "]"
			return out
		}
	}
	return append(out, ollama.Message{Role: ollama.RoleSystem, Content: note})
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
