package app

import (
	"strings"
	"testing"

	"astral/internal/ollama"
)

// The bug this guards: indexOf answers 0 for a miss, which is the right
// fallback for the scene's model and the wrong one for a dropdown whose row 0
// already means "same as the scene". Left that way, an unset background model
// silently selected the first installed one.
func TestHousekeepingRow(t *testing.T) {
	models := []string{"qwen3.6:27b", "qwen3.5:4b", "llama3.2:3b"}
	tests := []struct {
		name, want string
		row        int
	}{
		{"unset means same as the scene", "", 0},
		{"whitespace is unset", "   ", 0},
		{"first model is row one", "qwen3.6:27b", 1},
		{"last model", "llama3.2:3b", 3},
		{"a model since uninstalled falls back", "gone:7b", 0},
	}
	for _, tt := range tests {
		if got := housekeepingRow(models, tt.want); got != tt.row {
			t.Errorf("%s: housekeepingRow(%q) = %d, want %d", tt.name, tt.want, got, tt.row)
		}
	}
	if got := housekeepingRow(nil, "anything"); got != 0 {
		t.Errorf("with no models installed, got row %d, want 0", got)
	}
}

func TestNotificationPreview(t *testing.T) {
	long := "*She turns the chart over.* " + strings.Repeat("The coastline does not hold. ", 10)
	got := notificationPreview(long + "\n\nA second paragraph.")
	if strings.Contains(got, "*") || strings.Contains(got, "second paragraph") {
		t.Errorf("preview %q", got)
	}
	if n := len([]rune(got)); n > 141 || !strings.HasSuffix(got, "…") {
		t.Errorf("preview is %d runes: %q", n, got)
	}
	if got := notificationPreview("Short."); got != "Short." {
		t.Errorf("a short reply became %q", got)
	}
}

// A rewrite that drops a name Astral fills in is caught before it is saved.
func TestMissingPlaceholders(t *testing.T) {
	orig := "You are {{char}}. Never write {{user}}'s actions. %[1]s: and %[3]s"
	if got := missingPlaceholders(orig, "You are {{char}}. Keep to %[1]s and %[3]s."); len(got) != 1 || got[0] != "{{user}}" {
		t.Errorf("got %v", got)
	}
	if got := missingPlaceholders(orig, orig); len(got) != 0 {
		t.Errorf("the original itself is missing %v", got)
	}
}

// Save Prompt takes the newest reply that has a finished prompt, skipping a
// later reply that is only talk.
func TestLatestProposal(t *testing.T) {
	hist := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: "```prompt\nold\n```"},
		{Role: ollama.RoleUser, Content: "shorter"},
		{Role: ollama.RoleAssistant, Content: "```prompt\nnew\n```"},
		{Role: ollama.RoleUser, Content: "thanks"},
		{Role: ollama.RoleAssistant, Content: "Glad it helps."},
	}
	if got, ok := latestProposal(hist); !ok || got != "new" {
		t.Errorf("got %q %v", got, ok)
	}
}
