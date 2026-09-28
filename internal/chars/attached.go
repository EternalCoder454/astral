package chars

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Files attached to a message.
//
// A text file sent to a designer goes into the message itself, between two
// marker lines, so the model reads it on every later turn, a revision reads it,
// and building the result from the chat reads it, all without anything but the
// transcript to go on. On screen the block is folded to a line naming the file.

// MaxAttachedChars is how much of one file is sent: about ten thousand tokens,
// a long document that still leaves the conversation room in the context.
const MaxAttachedChars = 40000

const (
	fileOpen  = "<<<file: "
	fileClose = "<<<end of "
	fileEnd   = ">>>"
)

// AttachedFile is the block a file travels in.
func AttachedFile(name, text string) string {
	name = strings.NewReplacer("\n", " ", fileEnd, "").Replace(strings.TrimSpace(name))
	return fileOpen + name + fileEnd + "\n" + strings.TrimRight(text, "\n") + "\n" + fileClose + name + fileEnd
}

// ReadAttachable turns a file's bytes into text to attach, or says why it
// cannot be. Text is anything that is UTF-8 with no NUL in it; a file longer
// than MaxAttachedChars is cut there, with a line saying so.
func ReadAttachable(data []byte) (text string, cut bool, err error) {
	sniff := data
	if len(sniff) > 8192 {
		sniff = sniff[:8192]
	}
	if strings.ContainsRune(string(sniff), 0) || !utf8.Valid(data) {
		return "", false, fmt.Errorf("it is not a text file")
	}
	text = strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return "", false, fmt.Errorf("it is empty")
	}
	if r := []rune(text); len(r) > MaxAttachedChars {
		left := len(r) - MaxAttachedChars
		text = string(r[:MaxAttachedChars]) +
			fmt.Sprintf("\n[the rest of the file, %d more characters, was left out]", left)
		cut = true
	}
	return text, cut, nil
}

// HideAttachedFiles folds every attached file in a message to one line naming
// it, for showing the message rather than sending it.
func HideAttachedFiles(content string) string {
	if !strings.Contains(content, fileOpen) {
		return content
	}
	var b strings.Builder
	rest := content
	for {
		i := strings.Index(rest, fileOpen)
		if i < 0 {
			break
		}
		head := rest[i+len(fileOpen):]
		nl := strings.Index(head, fileEnd+"\n")
		if nl < 0 {
			break
		}
		name := head[:nl]
		body := head[nl+len(fileEnd)+1:]
		end := strings.Index(body, "\n"+fileClose+name+fileEnd)
		if end < 0 {
			break
		}
		b.WriteString(rest[:i])
		words := len(strings.Fields(body[:end]))
		fmt.Fprintf(&b, "[attached %s, %s]", name, wordCount(words))
		rest = body[end+len("\n"+fileClose+name+fileEnd):]
	}
	b.WriteString(rest)
	return b.String()
}

func wordCount(n int) string {
	if n == 1 {
		return "1 word"
	}
	s := fmt.Sprint(n)
	// Thousands separated, since these run long.
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s + " words"
}
