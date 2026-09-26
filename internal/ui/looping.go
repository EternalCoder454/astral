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

// Repetition is not the only way a reply comes apart. The other way has no
// cycle in it at all: the model stops writing sentences and starts chaining
// associations, one phrase suggesting the next, for as long as it is allowed.
//
//	...mission accomplished goal achieved target met objective fulfilled
//	purpose served function executed duty performed task finished work ended
//	process terminated operation concluded...
//
// Every phrase there is different, so Looping cannot see it: there is nothing
// repeated to find. What it does have is the thing the prose it replaced always
// has, and this does not: an end to the sentence. Thousands of characters went
// by in the measured case without a single full stop.
//
// So that is what is counted. It is a blunt signal and deliberately so, because
// the cost of a false positive is one stopped reply and the cost of a miss is a
// wall of nonsense saved into the transcript that every later turn is built on.

// rambleRun is how many characters may pass with no end to a sentence before a
// reply is called broken.
//
// Ordinary prose ends a sentence every eighty to a hundred and fifty characters,
// and a long one that runs to three hundred is remarkable. Seven hundred is
// beyond anything a model writing English produces on purpose, which is the
// point: this has to be a number no working reply reaches.
const rambleRun = 700

// Rambling reports whether text has stopped forming sentences.
//
// A line break counts as an ending. A list, a stanza, or a line of dialogue
// without punctuation is not a collapse, and treating a newline as a full stop
// costs nothing: text that has genuinely come apart does not produce them.
func Rambling(s string) bool {
	run := 0
	for _, r := range s {
		switch r {
		case '.', '!', '?', '\n', '\r', '…',
			// The same stops as written in Chinese, Japanese and Arabic, so a
			// scene played in one of them is not called broken for using its
			// own punctuation.
			'。', '！', '？', '؟':
			run = 0
		default:
			run++
			if run >= rambleRun {
				return true
			}
		}
	}
	return false
}
