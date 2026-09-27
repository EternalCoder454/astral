package chars

import (
	"strings"
	"testing"
)

// Written as escapes so this file, which asserts the prompt contains neither,
// does not contain them itself.
const (
	emDash = "\u2014"
	enDash = "\u2013"
)

// The general chat prompt tells the model not to use em dashes, so it had better
// not contain any: a model shown one writes it back, which is how the rule gets
// broken by the thing that states it.
func TestAssistantPromptPractisesWhatItSays(t *testing.T) {
	if strings.ContainsAny(AssistantSystem, emDash+enDash) {
		t.Error("the prompt that bans em and en dashes contains one")
	}
	low := strings.ToLower(AssistantSystem)
	for _, banned := range []string{"leverage", "delve", "seamless", "robust"} {
		// Named in the avoid list, which is the one place they may appear.
		if strings.Count(low, banned) > 1 {
			t.Errorf("%q appears in the prompt outside its own avoid list", banned)
		}
	}
}

// The clauses worth guarding, because each one was earned by watching a model do
// the opposite.
func TestAssistantPromptKeepsItsRules(t *testing.T) {
	low := strings.ToLower(AssistantSystem)
	for _, want := range []string{
		"lead with the answer",
		"match the length to the question",
		"do not restate the question",
		"never invent a name",
		"no em dashes",
		"never start a bullet or a numbered item with a bold word",
		"no disclaimers",
	} {
		if !strings.Contains(low, want) {
			t.Errorf("the prompt no longer says %q", want)
		}
	}
}

// The bold rule has to stay out of the end of a list. It sat last under SHAPE and
// was ignored twelve times across three replies; moved up and given its own line,
// the same rule gave three.
func TestBoldRuleIsNotBuried(t *testing.T) {
	lines := strings.Split(AssistantSystem, "\n")
	shapeAt, boldAt, endAt := -1, -1, len(lines)
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "SHAPE"):
			shapeAt = i
		case strings.Contains(strings.ToLower(l), "never start a bullet or a numbered item"):
			boldAt = i
		case shapeAt >= 0 && boldAt < 0 && strings.TrimSpace(l) == "":
			endAt = i
		}
	}
	if shapeAt < 0 || boldAt < 0 {
		t.Fatal("the shape section or the bold rule has gone")
	}
	if boldAt == endAt-1 {
		t.Error("the bold rule is the last line of its section again, where it was ignored")
	}
}
