package ui

import "strings"

// A model can collapse mid-reply and start cycling the same block of text
// forever. It is not rare, it is not recoverable, and left alone it runs until
// the token limit: a thousand words of "expanding extending enlarging
// broadening widening" where a reply should have been.
//
// The waste is the smaller half of the problem. The wall of text is saved to
// the transcript, and the transcript is the prompt for the next turn, so one
// collapse poisons every reply after it, and the recap and the lorebook then
// read it too. Catching it while it streams keeps it out of the record.

const (
	// loopWindow is how much of the tail must reappear earlier before a reply
	// is called a collapse, in characters. About thirty words, repeated
	// exactly.
	//
	// The test is "does the end of this text also occur earlier", which finds
	// a cycle of any length: text that repeats with period p contains every
	// window of length loopWindow twice, p characters apart. An earlier
	// version compared the tail against the block immediately before it, and
	// missed this entirely — the cycle that prompted all of this was nine
	// hundred characters long, larger than the periods it thought to try.
	loopWindow = 180

	// loopSearch bounds how far back the earlier occurrence may be, so that a
	// passage legitimately quoted back at the end of a long reply is not read
	// as a collapse. A model that has broken repeats itself immediately.
	loopSearch = 2600
)

// Looping reports whether a reply has collapsed into repeating itself.
func Looping(s string) bool {
	f := loopFold(s)
	if len(f) < loopWindow*2 {
		return false
	}
	tail := f[len(f)-loopWindow:]
	before := f[:len(f)-loopWindow]
	if len(before) > loopSearch {
		before = before[len(before)-loopSearch:]
	}
	return strings.Contains(before, tail)
}

// loopFold normalises for comparison: lowercase, with runs of whitespace
// flattened to one space. A model cycling a block does not reproduce its line
// breaks exactly, and a comparison that insisted on them would miss it.
func loopFold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range strings.ToLower(s) {
		switch r {
		case ' ', '\t', '\n', '\r':
			space = true
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	return b.String()
}
