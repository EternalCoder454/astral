package ollama

import "strings"

// A reasoning model's deliberation is not always in the field Ollama keeps for
// it. When it arrives in the reply instead, SplitThinking moves it once the
// reply is complete — which is too late for a transcript you are watching
// being written. What the model thinks about first is its own instructions, so
// what streams past is the character sheet and the formatting rules read back
// aloud, and the scene only appears once it has finished.
//
// ThinkStream does the same job a chunk at a time, so it never reaches the
// screen at all.

// maxUndecided bounds how much is held back while deciding whether a reply
// opens with a think tag. Longer than any tag plus the whitespace a model
// might open with, and short enough that nothing perceptible is delayed.
const maxUndecided = 64

type thinkState int

const (
	undecided thinkState = iota
	inThinking
	inReply
)

// ThinkStream routes a streaming reply into speech and deliberation.
//
// The zero value is ready. Feed it each chunk as it arrives and write what it
// returns; call Done at the end to release anything still held.
type ThinkStream struct {
	state thinkState
	buf   string
	close string
}

// Next takes the next chunk and returns what may be shown now.
func (t *ThinkStream) Next(chunk string) (reply, thinking string) {
	switch t.state {
	case inReply:
		return chunk, ""
	case inThinking:
		return t.consumeThinking(chunk)
	}

	t.buf += chunk
	trimmed := strings.TrimLeft(t.buf, " \t\r\n")
	if trimmed == "" {
		if len(t.buf) > maxUndecided {
			// Nothing but whitespace, and a lot of it. It is not a tag.
			out := t.buf
			t.buf, t.state = "", inReply
			return out, ""
		}
		return "", ""
	}
	low := asciiLower(trimmed)
	for _, tag := range thinkTags {
		if strings.HasPrefix(low, tag[0]) {
			t.state, t.close = inThinking, tag[1]
			rest := trimmed[len(tag[0]):]
			t.buf = ""
			return t.consumeThinking(rest)
		}
		// Still short of a decision: what has arrived could yet become this
		// tag, so hold it rather than showing half of one.
		if len(low) < len(tag[0]) && strings.HasPrefix(tag[0], low) {
			if len(t.buf) <= maxUndecided {
				return "", ""
			}
		}
	}
	out := t.buf
	t.buf, t.state = "", inReply
	return out, ""
}

// consumeThinking takes text known to be inside a block and returns whatever
// followed its end.
func (t *ThinkStream) consumeThinking(chunk string) (reply, thinking string) {
	t.buf += chunk
	if i := strings.Index(asciiLower(t.buf), t.close); i >= 0 {
		thinking = t.buf[:i]
		rest := t.buf[i+len(t.close):]
		t.buf, t.state = "", inReply
		return strings.TrimLeft(rest, " \t\r\n"), thinking
	}
	// Emit all but a possible partial closing tag, so a tag split across two
	// chunks is never shown as deliberation.
	keep := len(t.close) - 1
	if len(t.buf) <= keep {
		return "", ""
	}
	thinking = t.buf[:len(t.buf)-keep]
	t.buf = t.buf[len(t.buf)-keep:]
	return "", thinking
}

// Done releases whatever is still held back, at the end of a stream.
//
// An unclosed block is deliberation: it happens when the reply limit runs out
// mid-thought, and showing it as the scene is the thing this exists to stop.
func (t *ThinkStream) Done() (reply, thinking string) {
	out := t.buf
	t.buf = ""
	switch t.state {
	case inThinking:
		return "", out
	default:
		t.state = inReply
		return out, ""
	}
}

// Thinking reports whether the stream is currently inside a block, so a caller
// can say so on screen rather than looking stalled.
func (t *ThinkStream) Thinking() bool { return t.state == inThinking }
