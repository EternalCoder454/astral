package store

import (
	"strings"
	"testing"
)

func TestRulesTextNumbersWhatIsOn(t *testing.T) {
	c := Config{Rulebook: []Rule{
		{Text: "Keep replies to two paragraphs.", Enabled: true},
		{Text: "Never skip ahead in time.", Enabled: false},
		{Text: "Nobody explains their own feelings out loud.", Enabled: true},
	}}
	got := c.RulesText()
	if !strings.Contains(got, "1. Keep replies to two paragraphs.") {
		t.Errorf("the first enabled rule is not numbered 1:\n%s", got)
	}
	// Numbered by position among the enabled ones, not by position in the list:
	// a gap in the numbering reads as a missing instruction.
	if !strings.Contains(got, "2. Nobody explains their own feelings out loud.") {
		t.Errorf("the second enabled rule is not numbered 2:\n%s", got)
	}
	if strings.Contains(got, "Never skip ahead") {
		t.Error("a switched-off rule reached the model")
	}
}

func TestRulesTextIsEmptyWhenNothingIsOn(t *testing.T) {
	c := Config{Rulebook: []Rule{{Text: "A rule.", Enabled: false}}}
	if got := c.RulesText(); got != "" {
		t.Errorf("got %q, want nothing: a rulebook switched off should cost no tokens", got)
	}
}

func TestRulesSkipBlanks(t *testing.T) {
	c := Config{Rulebook: []Rule{
		{Text: "   ", Enabled: true},
		{Text: "A real rule.", Enabled: true},
	}}
	if n := len(c.Rules()); n != 1 {
		t.Errorf("got %d rules, want 1", n)
	}
	if got := c.RulesText(); !strings.HasPrefix(got, "1. A real rule.") {
		t.Errorf("a blank rule took a number: %q", got)
	}
}

func TestAddRuleStopsAtTheLimit(t *testing.T) {
	var c Config
	for i := 0; i < MaxRules; i++ {
		if !c.AddRule("rule") {
			t.Fatalf("refused rule %d, below the limit of %d", i+1, MaxRules)
		}
	}
	if c.AddRule("one too many") {
		t.Errorf("accepted rule %d, past the limit of %d", MaxRules+1, MaxRules)
	}
}

func TestMoveRule(t *testing.T) {
	c := Config{Rulebook: []Rule{{Text: "a"}, {Text: "b"}, {Text: "c"}}}
	if !c.MoveRule(2, -1) {
		t.Fatal("refused a legal move")
	}
	if c.Rulebook[1].Text != "c" || c.Rulebook[2].Text != "b" {
		t.Errorf("after moving c up: %v", texts(c.Rulebook))
	}
	// Off either end does nothing rather than panicking, because the buttons
	// that call this are on every row including the first and last.
	if c.MoveRule(0, -1) || c.MoveRule(2, 1) {
		t.Error("a move off the end of the list was accepted")
	}
	if got := texts(c.Rulebook); got != "a c b" {
		t.Errorf("the list changed on a refused move: %q", got)
	}
}

func TestRemoveRule(t *testing.T) {
	c := Config{Rulebook: []Rule{{Text: "a"}, {Text: "b"}, {Text: "c"}}}
	if !c.RemoveRule(1) {
		t.Fatal("refused a legal removal")
	}
	if got := texts(c.Rulebook); got != "a c" {
		t.Errorf("got %q, want \"a c\"", got)
	}
	if c.RemoveRule(5) {
		t.Error("removing past the end was accepted")
	}
}

// TestAdoptGlobalInstructions is the migration. Anyone who had been keeping a
// list in the freeform box should find that list in the rulebook, switched on,
// because it was in force a moment ago.
func TestAdoptGlobalInstructions(t *testing.T) {
	c := Config{GlobalInstructions: "Keep replies short.\n- Never break the fourth wall.\n2. She lies about her past.\n\n"}
	c.adoptGlobalInstructions()

	if len(c.Rulebook) != 3 {
		t.Fatalf("got %d rules, want 3: %v", len(c.Rulebook), texts(c.Rulebook))
	}
	for i, want := range []string{
		"Keep replies short.",
		"Never break the fourth wall.",
		"She lies about her past.",
	} {
		if c.Rulebook[i].Text != want {
			t.Errorf("rule %d is %q, want %q", i, c.Rulebook[i].Text, want)
		}
		if !c.Rulebook[i].Enabled {
			t.Errorf("rule %d arrived switched off", i)
		}
	}
	// And the old field is cleared, so a standing instruction lives in one place
	// rather than being editable in the list while the old copy stays in force.
	if c.GlobalInstructions != "" {
		t.Errorf("the freeform block survived the migration: %q", c.GlobalInstructions)
	}
}

func TestAdoptGlobalInstructionsRunsOnce(t *testing.T) {
	c := Config{
		GlobalInstructions: "An instruction.",
		Rulebook:           []Rule{{Text: "A rule someone wrote.", Enabled: true}},
	}
	c.adoptGlobalInstructions()
	if len(c.Rulebook) != 1 {
		t.Errorf("the migration ran over an existing rulebook: %v", texts(c.Rulebook))
	}
}

func texts(rules []Rule) string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Text)
	}
	return strings.Join(out, " ")
}
