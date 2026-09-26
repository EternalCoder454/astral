package ui

import "strings"

// Ollama puts a reasoning model's deliberation in a field of its own, and
// Astral renders that in a block you can fold away. Some models do not get that
// treatment: a finetune whose template does not declare the tags, or one Ollama
// does not know reasons at all, emits them into the reply instead, and what
// arrives is a message whose text begins "<think>".
//
// Rendered as-is that is bad in three ways. The tag shows. The deliberation is
// styled as prose and read as part of the scene. And it is saved as the reply,
// so the next turn's prompt contains the model talking to itself about its
// instructions, which is the strongest possible invitation to do it again.

// thinkTags are the open and close markers models use for this, lower-cased.
// Each pair is tried in order.
var thinkTags = [][2]string{
	{"<think>", "</think>"},
	{"<thinking>", "</thinking>"},
	{"<reasoning>", "</reasoning>"},
}

// SplitThinking separates a leading block of deliberation from the reply.
//
// Only leading: a tag that appears in the middle of a reply is far more likely
// to be someone's dialogue about thinking, or a character named in angle
// brackets, than a reasoning block, and cutting the middle out of a reply is
// worse than leaving a tag visible in it.
//
// An unclosed opening tag takes the rest, which is the right way round. It
// happens when the reply limit runs out mid-deliberation, and the alternative is
// showing the whole of it as the scene.
func SplitThinking(s string) (thinking, reply string) {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	lower := asciiLower(trimmed)
	for _, tag := range thinkTags {
		open, close := tag[0], tag[1]
		if !strings.HasPrefix(lower, open) {
			continue
		}
		rest := trimmed[len(open):]
		end := strings.Index(lower[len(open):], close)
		if end < 0 {
			return strings.TrimSpace(rest), ""
		}
		return strings.TrimSpace(rest[:end]), strings.TrimSpace(rest[end+len(close):])
	}
	return "", s
}

// asciiLower folds A-Z and nothing else.
//
// strings.ToLower would be the obvious call and is a bug here: it can change a
// string's length in bytes, because some characters lower-case to a different
// number of them. An offset found in the folded copy then does not mean the
// same place in the original, and slicing the original with it panics. A fuzz
// run found that within a second of being asked.
//
// The tags are ASCII, so an ASCII fold is all that is needed, and it is
// guaranteed to keep every byte where it was.
func asciiLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
