package ollama

import (
	"strings"
	"unicode"
)

// Stray characters from another script.
//
// A model trained heavily on Chinese will now and then, in the middle of an
// English sentence, emit the Chinese for the word it meant: "her weight
// shifting重心 onto her back foot". It is one token from the tail of a flat
// distribution, it happens on the MoE models more than the dense ones, and it
// cannot be prompted away because nothing chose it. What can be done is to
// never show it: the run is taken out of the text as it streams, which leaves
// "her weight shifting onto her back foot", a sentence missing one word rather
// than a sentence with a foreign one in it.
//
// Only in a conversation that is otherwise not written in those scripts. A
// scene in Chinese, or a character whose name is written in kanji, is left
// alone entirely: the filter switches itself off when anything in the request
// uses them.

// scriptMinLatin is how much ordinary text a reply must already contain before
// a run of Han, kana or hangul in it counts as stray. Twenty letters is a few
// words: enough to know the reply is in English, too few to let a stray token
// in the opening words through.
const scriptMinLatin = 20

// strayFilter removes stray runs from a reply as it streams.
type strayFilter struct {
	latin int
	// dropSpace is set after a run is removed, so that if a space was already
	// written before it the one after it is not written too.
	dropSpace bool
	// lastSpace says the last character written, across every piece of the
	// stream so far, was a space. Judged per piece it cannot see that the
	// space before a removed run arrived in the previous one.
	lastSpace bool
}

// needsScriptGuard reports whether a request is written without the scripts
// the filter removes. When it is not, the filter is not used at all.
func needsScriptGuard(msgs []Message) bool {
	for _, m := range msgs {
		if containsCJK(m.Content) {
			return false
		}
	}
	return true
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) ||
		(r >= 0x3000 && r <= 0x303f) || // CJK punctuation
		(r >= 0xff00 && r <= 0xffef) // full-width forms
}

func containsCJK(s string) bool {
	for _, r := range s {
		if isCJK(r) {
			return true
		}
	}
	return false
}

// fullWidth maps the full-width punctuation a stray run often ends with to the
// ordinary mark it stands for, so "then，she" becomes "then, she" rather than
// "thenshe".
var fullWidth = map[rune]string{
	'，': ",", '。': ".", '！': "!", '？': "?", '：': ":", '；': ";",
	'（': "(", '）': ")", '“': "\"", '”': "\"", '、': ",", '「': "\"", '」': "\"",
}

// Next filters one piece of the stream.
func (f *strayFilter) Next(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isCJK(r) && f.latin >= scriptMinLatin {
			if mark, ok := fullWidth[r]; ok {
				b.WriteString(mark)
				f.dropSpace, f.lastSpace = false, false
				continue
			}
			f.dropSpace = true
			continue
		}
		if r == ' ' && f.dropSpace && f.lastSpace {
			// The run replaced a word, and the space before it is already
			// written. A second one would show as a gap.
			f.dropSpace = false
			continue
		}
		f.dropSpace = false
		if r < unicode.MaxASCII && unicode.IsLetter(r) {
			f.latin++
		}
		f.lastSpace = r == ' '
		b.WriteRune(r)
	}
	return b.String()
}
