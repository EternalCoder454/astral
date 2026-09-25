package app

import "testing"

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
