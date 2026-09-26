package world

import (
	"strings"
	"testing"

	"astral/internal/ollama"
)

// Playing a thirty-turn scene on a storm-lashed coast produced entries that
// triggered on "tide" and "the coast". Both are in almost every message, so
// both would fire on almost every turn and spend the budget other entries
// needed. What counts as a common word depends on the setting, so the scene is
// measured rather than checked against a list.
func TestCleanKeysRejectsWordsTheSceneIsFullOf(t *testing.T) {
	scene := []ollama.Message{
		{Role: ollama.RoleUser, Content: "The tide came in early and the ferry did not."},
		{Role: ollama.RoleAssistant, Content: "She watched the tide from the window, and said nothing."},
		{Role: ollama.RoleUser, Content: "\"The tide waits for no one.\""},
		{Role: ollama.RoleAssistant, Content: "The tide was already over the lower step."},
		{Role: ollama.RoleUser, Content: "I asked about Oren Vance."},
		{Role: ollama.RoleAssistant, Content: "\"Oren Vance drowned at the crossing.\""},
	}
	got := cleanKeys([]string{"tide", "Oren Vance", "the ferry"}, scene)

	for _, k := range got {
		if strings.EqualFold(k, "tide") {
			t.Error("kept \"tide\", which appears in four of six messages")
		}
	}
	var keptName bool
	for _, k := range got {
		if k == "Oren Vance" {
			keptName = true
		}
	}
	if !keptName {
		t.Errorf("dropped a specific name that appears twice: %v", got)
	}
}

// With almost nothing to measure, the frequency rule must not fire: two
// messages both mentioning a name is not evidence that the name is generic.
func TestCleanKeysNeedsEnoughSceneToJudge(t *testing.T) {
	scene := []ollama.Message{
		{Role: ollama.RoleUser, Content: "Tell me about Kestrel Bay."},
		{Role: ollama.RoleAssistant, Content: "\"Kestrel Bay is three days north.\""},
	}
	got := cleanKeys([]string{"Kestrel Bay"}, scene)
	if len(got) != 1 || got[0] != "Kestrel Bay" {
		t.Errorf("dropped a good key on a two-message scene: %v", got)
	}
}

// An entry is injected whenever one of its keys comes up, out of a budget the
// whole lore block shares, so one entry that ignores the word limit costs every
// other entry its place. A measured scene produced one at 1604 characters.
func TestTruncateEntryBoundsWhatTheModelIgnores(t *testing.T) {
	long := strings.Repeat("Kestrel Bay is a harbour town on the eastern reach. ", 30)
	got := truncateEntry(long)
	if len(got) > maxEntryChars {
		t.Errorf("entry is %d chars, over the %d cap", len(got), maxEntryChars)
	}
	if !strings.HasSuffix(got, ".") {
		t.Errorf("cut mid-fact, not at a sentence end: %q", got[max(0, len(got)-40):])
	}
}

func TestTruncateEntryLeavesAShortEntryAlone(t *testing.T) {
	short := "Kestrel Bay is a harbour town. The ferry from it is often late."
	if got := truncateEntry(short); got != short {
		t.Errorf("a short entry was changed:\n in:  %s\n out: %s", short, got)
	}
}

// One clause longer than the cap with no sentence end in it must still come back
// bounded, and must not be cut through the middle of a word.
func TestTruncateEntryWithoutASentenceEnd(t *testing.T) {
	got := truncateEntry(strings.Repeat("harbour ", 200))
	if len(got) > maxEntryChars {
		t.Errorf("entry is %d chars, over the %d cap", len(got), maxEntryChars)
	}
	if strings.HasSuffix(got, "harbou") || strings.HasSuffix(got, " ") {
		t.Errorf("cut mid-word or left trailing space: %q", got[max(0, len(got)-20):])
	}
}
