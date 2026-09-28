package ui

import (
	"strings"
	"testing"
	"time"
)

// The composer chip is a small control, and a model tag is a path. These are
// the shapes that actually appear in an `ollama list`.
func TestShortModel(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"qwen3:8b", "qwen3:8b"},
		{"llama3.2:latest", "llama3.2:latest"},
		{"huihui_ai/qwen3-abliterated:27b", "qwen3-abliterated:27b"},
		{"registry.example.com/team/model:v1", "model:v1"},
		{"", ""},
		// Trailing slash: nothing after it to take, so the tag is left alone
		// rather than becoming empty.
		{"org/", "org/"},
	}
	for _, tt := range tests {
		if got := shortModel(tt.in); got != tt.want {
			t.Errorf("shortModel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A long name is trimmed, but never at the cost of the tag: qwen3:8b and
// qwen3:32b differ only at the end, so losing the end loses the distinction
// the chip exists to make.
func TestShortModelKeepsTheTag(t *testing.T) {
	const long = "some-extremely-long-finetune-name-here:q4_K_M"
	got := shortModel(long)
	if n := len([]rune(got)); n > 28 {
		t.Errorf("shortModel(%q) = %q, %d runes long, want at most 28", long, got, n)
	}
	if want := ":q4_K_M"; !strings.HasSuffix(got, want) {
		t.Errorf("shortModel(%q) = %q, want it to end in %q", long, got, want)
	}
}

func TestWhenLabel(t *testing.T) {
	now := time.Date(2026, 9, 27, 21, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 9, 27, 9, 5, 0, 0, time.Local), "09:05"},
		{time.Date(2026, 9, 20, 9, 5, 0, 0, time.Local), "20 Sep, 09:05"},
		{time.Date(2025, 12, 31, 23, 59, 0, 0, time.Local), "31 Dec 2025, 23:59"},
	} {
		if got := whenLabel(tc.at, now); got != tc.want {
			t.Errorf("whenLabel(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
}

// A search snippet bolds the words that matched, and nothing in a message can
// become markup on the way.
func TestSnippetMarkup(t *testing.T) {
	got := snippetMarkup("…expelled from the \x01Guild\x02 for a <coast> & more")
	want := "…expelled from the <b>Guild</b> for a &lt;coast&gt; &amp; more"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := snippetMarkup("an \x01open match"); got != "an <b>open match</b>" {
		t.Errorf("an unclosed match was left open: %q", got)
	}
}
