package chars

import (
	"strings"
	"testing"
)

func TestAttachedFilesFoldOnScreen(t *testing.T) {
	msg := "Use these notes.\n\n" + AttachedFile("notes.md", "# Vesper\nA cartographer, impatient.\n") +
		"\n\n" + AttachedFile("map.txt", "north")
	got := HideAttachedFiles(msg)
	want := "Use these notes.\n\n[attached notes.md, 5 words]\n\n[attached map.txt, 1 word]"
	if got != want {
		t.Errorf("folded to\n%q\nwant\n%q", got, want)
	}
	// The model still gets every word.
	if !strings.Contains(msg, "A cartographer, impatient.") {
		t.Error("the file's text is not in the message")
	}
	// Text that merely mentions a marker is left alone.
	if s := "a <<<file: that never closes"; HideAttachedFiles(s) != s {
		t.Errorf("an unclosed marker was folded: %q", HideAttachedFiles(s))
	}
}

func TestReadAttachable(t *testing.T) {
	if _, _, err := ReadAttachable([]byte("\x89PNG\r\n\x1a\n\x00\x00")); err == nil {
		t.Error("a PNG was read as text")
	}
	text, cut, err := ReadAttachable([]byte("\ufeffline one\r\nline two\r\n"))
	if err != nil || cut || text != "line one\nline two\n" {
		t.Errorf("got %q, %v, %v", text, cut, err)
	}
	long := strings.Repeat("é", MaxAttachedChars+10)
	text, cut, err = ReadAttachable([]byte(long))
	if err != nil || !cut || !strings.Contains(text, "10 more characters") {
		t.Errorf("a long file: cut=%v err=%v tail=%q", cut, err, text[len(text)-60:])
	}
}

func TestSummaryForFillsInNames(t *testing.T) {
	c := Character{Name: "Vesper", Description: "{{char}} owes {{user}} money."}
	if got := c.SummaryFor("Wren"); got != "Vesper owes Wren money." {
		t.Errorf("SummaryFor = %q", got)
	}
}

func TestAttachedTextIsCompacted(t *testing.T) {
	text, _, err := ReadAttachable([]byte("# Notes   \r\n\r\n\r\n\r\n  - indented item\t\r\nend"))
	if err != nil || text != "# Notes\n\n  - indented item\nend" {
		t.Errorf("got %q, %v", text, err)
	}
}
