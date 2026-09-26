package chars

import "strings"

// A scene with more than one character comes back from the model as one reply
// with the speakers marked in it:
//
//	Vesper: *She did not look up from the chart.* "You're late."
//
//	Kestrel: "She's been saying that since noon."
//
// One call, not one per character. A model given the whole cast writes them
// reacting to each other, which is the entire point of putting them in a room
// together; a call per character produces five monologues aimed at you.
//
// This file turns that reply back into separate turns, both in one pass and as
// it streams, so a beat becomes its own message with its own name and avatar
// rather than a label in the middle of someone else's paragraph.

// Beat is a run of prose by one speaker. An empty Name means the model started
// writing before it named anyone, which the caller resolves to whoever it
// expects to be speaking.
type Beat struct {
	Name string
	Text string
}

// labelPrefixes are the decorations a model puts around a name. A model told to
// write "Vesper:" will often write "**Vesper:**" instead, because that is what
// its training data looks like, and a label it wrote in good faith should not
// end up as prose.
var labelPrefixes = []string{"", "*", "**", "###", "##", "#", "-"}

// labelSpace is the whitespace allowed before a label and after its decoration.
// Carriage returns are in it because a model that answers with CRLF line
// endings would otherwise have no labels recognised at all.
const labelSpace = " \t\v\f\r"

// matchLabel reports whether line opens with one of names as a speaker label,
// returning the name as the cast spells it and whatever followed the colon.
//
// line is one line with no trailing newline.
func matchLabel(line string, names []string) (name, rest string, ok bool) {
	t := strings.TrimLeft(line, labelSpace)
	for _, pre := range labelPrefixes {
		if pre != "" && !strings.HasPrefix(t, pre) {
			continue
		}
		// Whitespace after the decoration as well as before it: a model writing
		// a heading writes "## Vesper:", not "##Vesper:".
		body := strings.TrimLeft(t[len(pre):], labelSpace)
		// Asterisks are only stripped around a label that opened with them.
		// Otherwise "Vesper:*She looked up.*" loses the asterisk that starts
		// the narration, and the renderer is handed markup with one end
		// missing.
		starred := strings.HasPrefix(pre, "*")
		for _, n := range names {
			if n == "" || !hasPrefixFold(body, n) {
				continue
			}
			after := body[len(n):]
			if starred {
				after = trimStars(after)
			}
			if !strings.HasPrefix(after, ":") {
				continue
			}
			after = after[1:]
			if starred {
				after = trimStars(after)
			}
			return n, strings.TrimLeft(after, labelSpace), true
		}
	}
	return "", "", false
}

func trimStars(s string) string {
	for strings.HasPrefix(s, "*") {
		s = s[1:]
	}
	return s
}

// labelSettled reports whether a label at the start of partial is finished, or
// whether more of it may still be arriving.
//
// The decoration closes after the colon, so "**Vesper:" reads as a complete
// label while two asterisks are still on their way. Committing there put those
// asterisks into the prose as the first thing the character said.
func labelSettled(partial string, names []string) bool {
	_, rest, ok := matchLabel(partial, names)
	if !ok {
		return false
	}
	if rest != "" {
		return true
	}
	// Nothing after the label yet, and the last byte could be part of its
	// closing decoration.
	switch partial[len(partial)-1] {
	case ':', '*':
		return false
	}
	return true
}

// couldGrowIntoLabel reports whether partial, which is the start of a line that
// has not ended yet, might still turn into a speaker label once more arrives.
//
// This is what lets the stream hold back four characters instead of a
// paragraph: while "Ves" could still become "Vesper:" it is not shown, and the
// moment it cannot it is released.
//
// It follows matchLabel step for step rather than comparing against built-up
// candidate strings, because the two have to agree about what a label looks
// like and a second spelling of the same rules is a second thing to forget.
func couldGrowIntoLabel(partial string, names []string) bool {
	t := strings.TrimLeft(partial, labelSpace)
	if t == "" {
		return true // a line that has not started could start with anything
	}
	for _, pre := range labelPrefixes {
		if pre != "" && !strings.HasPrefix(t, pre) {
			continue
		}
		body := strings.TrimLeft(t[len(pre):], labelSpace)
		starred := strings.HasPrefix(pre, "*")
		for _, n := range names {
			if n == "" {
				continue
			}
			if len(body) < len(n) {
				// Still partway through the name.
				if hasPrefixFold(n, body) {
					return true
				}
				continue
			}
			if !hasPrefixFold(body, n) {
				continue
			}
			after := body[len(n):]
			if starred {
				after = trimStars(after)
			}
			// The name is complete and the colon has not arrived yet.
			if after == "" {
				return true
			}
		}
	}
	return false
}

// BeatStream folds a streaming reply into beats.
//
// The problem it solves is that a name arrives a few characters at a time like
// everything else, so by the time "Vesper:" is recognisable the "V" has already
// been shown. It holds back only a line that could still become a label, which
// is a handful of characters and never a visible pause.
//
// The zero value is not usable: Names must be set. A stream with no names
// passes everything through unchanged, which is the right behaviour for a scene
// with one character in it.
type BeatStream struct {
	// Names is the cast, spelled as it should appear.
	Names []string

	cur  string // who is speaking now
	pend string // the start of a line, not yet shown to anyone
	open bool   // the current line has already been released, so pass it through
	// skipWS means a label has just been committed and only whitespace has
	// followed it. The one-pass path trims that whitespace off as part of
	// reading the label, so the stream has to swallow it too — including the
	// newline of a label sitting alone on its line, which contributes nothing.
	skipWS bool
}

// Next folds the next chunk of the reply, returning whatever can now be shown.
func (b *BeatStream) Next(chunk string) []Beat {
	if len(b.Names) == 0 {
		if chunk == "" {
			return nil
		}
		return []Beat{{Name: b.cur, Text: chunk}}
	}
	var out []Beat
	emit := func(text string) {
		if text == "" {
			return
		}
		// Merged as they are produced. A row only cares where a speaker
		// changes, and one beat per chunk would be thousands of them.
		if n := len(out); n > 0 && out[n-1].Name == b.cur {
			out[n-1].Text += text
			return
		}
		out = append(out, Beat{Name: b.cur, Text: text})
	}

	for chunk != "" {
		if b.skipWS {
			i := 0
			for i < len(chunk) && strings.IndexByte(labelSpace, chunk[i]) >= 0 {
				i++
			}
			chunk = chunk[i:]
			if chunk == "" {
				break
			}
			if chunk[0] == '\n' {
				chunk = chunk[1:]
				b.skipWS, b.open = false, false
				continue
			}
			b.skipWS = false
			continue
		}
		if b.open {
			// Mid-line, already committed to being prose: nothing here can be
			// a label, so it goes straight out.
			if i := strings.IndexByte(chunk, '\n'); i >= 0 {
				emit(chunk[:i+1])
				chunk = chunk[i+1:]
				b.open = false
				continue
			}
			emit(chunk)
			break
		}

		if i := strings.IndexByte(chunk, '\n'); i >= 0 {
			b.pend += chunk[:i+1]
			chunk = chunk[i+1:]
			line := strings.TrimSuffix(b.pend, "\n")
			b.pend = ""
			if name, rest, ok := matchLabel(line, b.Names); ok {
				b.cur = name
				// The newline after a label that carried text belongs to the
				// text. A label alone on its line contributes nothing.
				if rest != "" {
					emit(rest + "\n")
				}
				continue
			}
			emit(line + "\n")
			continue
		}

		b.pend += chunk
		chunk = ""
		if labelSettled(b.pend, b.Names) {
			name, rest, _ := matchLabel(b.pend, b.Names)
			b.cur = name
			b.pend = ""
			b.open = true
			b.skipWS = rest == ""
			emit(rest)
			continue
		}
		// Held only while it could still be, or still become, a label. That is
		// a handful of characters, never a paragraph, so there is no visible
		// pause in the stream.
		if _, _, isLabel := matchLabel(b.pend, b.Names); isLabel {
			continue
		}
		if !couldGrowIntoLabel(b.pend, b.Names) {
			text := b.pend
			b.pend = ""
			b.open = true
			emit(text)
		}
	}
	return out
}

// Done releases whatever is still held back, at the end of the reply.
func (b *BeatStream) Done() []Beat {
	if b.pend == "" {
		return nil
	}
	text := b.pend
	b.pend = ""
	// A reply that ends on a bare label named a speaker and then stopped. There
	// is nothing to attribute, so the name is taken and the text is dropped
	// rather than shown as prose.
	if name, rest, ok := matchLabel(text, b.Names); ok {
		b.cur = name
		if rest == "" {
			return nil
		}
		return []Beat{{Name: b.cur, Text: rest}}
	}
	return []Beat{{Name: b.cur, Text: text}}
}

// Start says who is already speaking, before any label has arrived. A
// continuation resumes somebody's reply, and the rest of it carries no label
// because the model is finishing a sentence rather than beginning a turn.
func (b *BeatStream) Start(name string) { b.cur = name }

// Speaker is who the stream is currently attributing text to.
func (b *BeatStream) Speaker() string { return b.cur }

// SplitBeats parses a whole reply into beats, merged by speaker and trimmed.
//
// Defined in terms of BeatStream rather than beside it, so the two can never
// disagree about what a label is — which they did, twice, when the streaming
// splitter for reasoning tags was written separately from the one that ran at
// the end.
func SplitBeats(reply string, names []string) []Beat {
	var bs BeatStream
	bs.Names = names
	out := append(bs.Next(reply), bs.Done()...)
	return MergeBeats(out)
}

// MergeBeats joins neighbouring beats by the same speaker and trims them,
// dropping any that turn out to be empty.
func MergeBeats(in []Beat) []Beat {
	// Joined before trimming, because within one speaker's run the text is
	// contiguous and trimming first would eat the space between two chunks.
	joined := make([]Beat, 0, len(in))
	for _, b := range in {
		if n := len(joined); n > 0 && joined[n-1].Name == b.Name {
			joined[n-1].Text += b.Text
			continue
		}
		joined = append(joined, b)
	}
	// Then trimmed, joining again where dropping a whitespace-only beat has
	// left two runs by the same speaker next to each other.
	out := make([]Beat, 0, len(joined))
	for _, b := range joined {
		b.Text = strings.TrimSpace(b.Text)
		if b.Text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Name == b.Name {
			out[n-1].Text += "\n\n" + b.Text
			continue
		}
		out = append(out, b)
	}
	return out
}

// Label puts a speaker's name back on their words, for sending a group
// transcript to the model.
//
// The name is stored on the message rather than in it, so this is where it goes
// back on. It has to: the transcript is the strongest instruction in the
// context, and a scene whose history arrives unlabelled teaches the model that
// replies do not carry labels, which is exactly the thing that must not drift.
func Label(name, text string) string {
	text = strings.TrimSpace(text)
	if name == "" {
		return text
	}
	if text == "" {
		return ""
	}
	return name + ": " + text
}
