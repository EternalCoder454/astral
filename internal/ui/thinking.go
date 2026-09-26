package ui

import "strings"

// Ollama puts deliberation in a field of its own, which Astral folds away. A
// finetune whose template does not declare the tags emits it into the reply
// instead, and a message beginning "<think>" is then styled as prose, read as
// part of the scene, and saved as the reply — so the next prompt contains the
// model talking to itself about its instructions.

// thinkTags are the open and close markers models use for this, lower-cased.
// Each pair is tried in order.
var thinkTags = [][2]string{
	{"<think>", "</think>"},
	{"<thinking>", "</thinking>"},
	{"<reasoning>", "</reasoning>"},
}

// SplitThinking separates a leading block of deliberation from the reply.
//
// Only leading: a tag mid-reply is far more likely to be dialogue about
// thinking than a reasoning block, and cutting the middle out of a reply is
// worse than leaving a tag visible. An unclosed tag takes the rest, which is
// what the reply limit running out mid-deliberation produces.
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
// strings.ToLower is a bug here: it can change a string's length in bytes, so
// an offset from the folded copy does not mean the same place in the original
// and slicing with it panics. The tags are ASCII, and this keeps every byte
// where it was.
func asciiLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
