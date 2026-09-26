package ui

import "strings"

// A model can collapse mid-reply and cycle the same block forever. It does not
// recover, and the wall of text is saved to the transcript, which is the next
// turn's prompt, so one collapse poisons every reply after it. Catching it
// while it streams keeps it out of the record.

const (
	// loopWindow is how much of the tail must reappear earlier, in characters.
	//
	// Asking whether the end also occurs earlier finds a cycle of any length,
	// because text repeating with period p contains every window of this size
	// twice. Comparing against the block immediately before instead only finds
	// the periods you thought to try.
	loopWindow = 180

	// loopSearch bounds how far back to look, so a passage legitimately quoted
	// back at the end of a long reply is not read as a collapse.
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

// The other way a reply comes apart has no cycle in it: the model stops
// writing sentences and chains associations instead.
//
//	...mission accomplished goal achieved target met objective fulfilled
//	purpose served function executed duty performed task finished...
//
// Every phrase differs, so Looping finds nothing. What it lacks is an end to
// the sentence, so that is what is counted. Blunt on purpose: a false positive
// costs one reply, a miss costs a wall of nonsense in the transcript that every
// later turn is built on.

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
