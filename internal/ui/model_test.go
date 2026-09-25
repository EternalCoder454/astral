package ui

import (
	"strings"
	"testing"
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
