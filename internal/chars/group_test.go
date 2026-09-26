package chars

import (
	"strings"
	"testing"

	"astral/internal/ollama"
)

func threeHanded() []Character {
	return []Character{
		{Name: "Vesper", Description: "A cartographer who does not look up.", Personality: "dry, exact"},
		{Name: "Kestrel", Description: "A courier who is always early.", Personality: "restless"},
		{Name: "Ash", Description: "The harbourmaster's clerk.", Instructions: "Ash never says the word no."},
	}
}

func TestGroupSystemNamesTheWholeCast(t *testing.T) {
	got := BuildGroupSystem(threeHanded(), Persona{Name: "Wren"})
	for _, want := range []string{"Vesper", "Kestrel", "Ash", "Wren"} {
		if !strings.Contains(got, want) {
			t.Errorf("the system prompt never mentions %q", want)
		}
	}
	if !strings.Contains(got, "A cartographer who does not look up.") {
		t.Error("a character's description did not reach the prompt")
	}
	// The label format has to be shown, not described. A model told about a
	// format and a model shown one behave differently, and the renderer depends
	// on getting it.
	if !strings.Contains(got, "Vesper: ") {
		t.Error("the prompt never demonstrates the label format")
	}
}

func TestGroupSystemKeepsInstructionsWithTheirOwner(t *testing.T) {
	// Pooling them is the bug worth guarding: "Ash never says the word no"
	// applied to the whole cast is a scene where nobody can refuse anything.
	got := BuildGroupSystem(threeHanded(), Persona{Name: "Wren"})
	i := strings.Index(got, "## Ash")
	if i < 0 {
		t.Fatal("no block for Ash")
	}
	j := strings.Index(got, "Ash never says the word no")
	if j < i {
		t.Errorf("Ash's instructions are outside Ash's block (block at %d, instruction at %d)", i, j)
	}
	if k := strings.Index(got, "## Instructions"); k >= 0 && k < j {
		t.Error("a character's own instructions ended up in the shared instruction section")
	}
}

func TestGroupSystemTakesOneScenario(t *testing.T) {
	cast := threeHanded()
	cast[0].Scenario = "The tide came in early."
	cast[1].Scenario = "A funeral, three counties away."
	got := BuildGroupSystem(cast, Persona{})
	if !strings.Contains(got, "The tide came in early.") {
		t.Error("the first scenario did not reach the prompt")
	}
	if strings.Contains(got, "A funeral, three counties away.") {
		t.Error("a second scenario reached the prompt: a scene cannot start in two places")
	}
}

func TestGroupSystemArguesAgainstARollCall(t *testing.T) {
	got := BuildGroupSystem(threeHanded(), Persona{})
	low := strings.ToLower(got)
	if !strings.Contains(low, "not everyone speaks every turn") {
		t.Error("nothing tells the model that silence is allowed")
	}
	if !strings.Contains(low, "one line each") {
		t.Error("nothing warns against giving every character one line each")
	}
}

func TestGroupSystemExpandsPlaceholdersAgainstTheCast(t *testing.T) {
	cast := threeHanded()
	cast[0].Description = "{{char}} keeps {{user}} waiting."
	got := BuildGroupSystem(cast, Persona{Name: "Wren"})
	if strings.Contains(got, "{{char}}") || strings.Contains(got, "{{user}}") {
		t.Errorf("a placeholder reached the model:\n%s", got)
	}
	// Inside a character's own block {{char}} is that character, not the list.
	if !strings.Contains(got, "Vesper keeps Wren waiting.") {
		t.Error("{{char}} in a character's description should expand to that character")
	}
}

func TestGroupFallsBackToTheTwoHanderWithOneCharacter(t *testing.T) {
	one := []Character{{Name: "Vesper", Description: "A cartographer."}}
	got := BuildGroupSystem(one, Persona{Name: "Wren"})
	want := BuildSystem(one[0], Persona{Name: "Wren"})
	if got != want {
		t.Error("a cast of one should build exactly the ordinary scene prompt")
	}
	msgs := BuildGroupMessages(one, Scene{Persona: Persona{Name: "Wren"}})
	plain := BuildMessages(one[0], Scene{Persona: Persona{Name: "Wren"}})
	if len(msgs) != len(plain) || msgs[0].Content != plain[0].Content {
		t.Error("a cast of one should build exactly the ordinary request")
	}
}

func TestGroupMessagesPutTheAnchorLast(t *testing.T) {
	msgs := BuildGroupMessages(threeHanded(), Scene{
		Persona: Persona{Name: "Wren"},
		History: []ollama.Message{{Role: ollama.RoleUser, Content: "Hello."}},
	})
	if len(msgs) < 3 {
		t.Fatalf("got %d messages, want a system prompt, a turn and an anchor", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Role != ollama.RoleSystem {
		t.Fatalf("the last message is a %s, want the anchor", last.Role)
	}
	if !strings.Contains(last.Content, "You are playing Vesper, Kestrel, Ash") {
		t.Errorf("the anchor does not name the cast: %q", last.Content)
	}
}

func TestGroupMessagesSendNoExampleDialogue(t *testing.T) {
	// Each card's examples are written for a scene with one character in it and
	// carry no labels, so several of them teach the model that labels are
	// optional and that the others are not there.
	cast := threeHanded()
	cast[0].MesExample = "{{user}}: Hello\n{{char}}: Go away"
	msgs := BuildGroupMessages(cast, Scene{Persona: Persona{Name: "Wren"}})
	for _, m := range msgs {
		if strings.Contains(m.Content, "Go away") {
			t.Errorf("example dialogue reached a group prompt as a %s turn", m.Role)
		}
	}
}

func TestGroupAnchorOnlyMentionsTheRollCallWhenItIsHappening(t *testing.T) {
	quiet := GroupAnchor(threeHanded(), Scene{Persona: Persona{Name: "Wren"}}, "Wren")
	if strings.Contains(quiet, "reads as a list") {
		t.Error("the anchor complains about a roll call that is not happening")
	}
	loud := GroupAnchor(threeHanded(), Scene{Persona: Persona{Name: "Wren"}, RollCall: true}, "Wren")
	if !strings.Contains(loud, "reads as a list") {
		t.Error("the anchor says nothing when the scene has become a roll call")
	}
}

func TestRollCallSpotsTheListAndNothingElse(t *testing.T) {
	names := []string{"Vesper", "Kestrel", "Ash"}
	list := []string{
		"Vesper: a\n\nKestrel: b\n\nAsh: c",
		"Vesper: d\n\nKestrel: e\n\nAsh: f",
	}
	if !RollCall(list, names) {
		t.Error("two replies with everyone speaking in order is a roll call")
	}
	// Order is not what makes it a list. Everyone once, whatever the order, is.
	shuffled := []string{
		"Kestrel: a\n\nAsh: b\n\nVesper: c",
		"Ash: d\n\nVesper: e\n\nKestrel: f",
	}
	if !RollCall(shuffled, names) {
		t.Error("everyone speaking once is a roll call however the order is shuffled")
	}
	// A real scene: someone silent, and someone speaking twice.
	real := []string{
		"Kestrel: a\n\nVesper: b",
		"Ash: c\n\nKestrel: d\n\nAsh: e",
	}
	if RollCall(real, names) {
		t.Error("a scene where somebody is silent or speaks twice is not a roll call")
	}
	// One list is not a habit yet.
	if RollCall(list[:1], names) {
		t.Error("a single reply should not be enough to call it a habit")
	}
	// With two characters both speaking every turn is just a conversation.
	if RollCall([]string{"Vesper: a\n\nKestrel: b", "Vesper: c\n\nKestrel: d"},
		[]string{"Vesper", "Kestrel"}) {
		t.Error("a two-hander cannot be a roll call")
	}
}

func TestGroupCastNamesSkipsTheNameless(t *testing.T) {
	got := CastNames([]Character{{Name: "Vesper"}, {Name: "  "}, {Name: "Ash"}})
	if len(got) != 2 || got[0] != "Vesper" || got[1] != "Ash" {
		t.Errorf("got %#v, want the two named members", got)
	}
}

// TestGroupPromptIsStableBetweenTurns is the prefix cache property, the same one
// the two-hander has: everything before the transcript must be byte-identical
// from one turn to the next, or the server re-reads the whole prompt every turn.
func TestGroupPromptIsStableBetweenTurns(t *testing.T) {
	cast := threeHanded()
	p := Persona{Name: "Wren"}
	first := BuildGroupMessages(cast, Scene{Persona: p, History: []ollama.Message{
		{Role: ollama.RoleUser, Content: "One."},
	}})
	second := BuildGroupMessages(cast, Scene{Persona: p, History: []ollama.Message{
		{Role: ollama.RoleUser, Content: "One."},
		{Role: ollama.RoleAssistant, Content: "Vesper: Two."},
		{Role: ollama.RoleUser, Content: "Three."},
	}})
	if first[0].Content != second[0].Content {
		t.Error("the system prompt changed between turns, which throws away the cached prefix")
	}
	// And the history is appended, not rewritten.
	for i := 1; i < len(first)-1; i++ {
		if first[i].Content != second[i].Content {
			t.Errorf("message %d changed between turns: %q then %q", i, first[i].Content, second[i].Content)
		}
	}
}

// TestCompactPromptNamesTheWholeCast is the bug groups introduced. The recap is
// the only surviving record of the turns it replaces, so a group summarised as a
// two-hander loses everything the other characters did — invisibly, and only in
// scenes long enough to have been worth keeping.
func TestCompactPromptNamesTheWholeCast(t *testing.T) {
	aged := []ollama.Message{
		{Role: ollama.RoleUser, Content: "Who took the chart?"},
		{Role: ollama.RoleAssistant, Content: "Vesper: \"Not me.\"\n\nKestrel: \"I did.\""},
	}
	got := compactPrompt("", aged, threeHanded(), Persona{Name: "Wren"})
	for _, want := range []string{"Vesper", "Kestrel", "Ash", "Wren"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summariser is never told about %q", want)
		}
	}
	if strings.Contains(got, "The two characters are") {
		t.Error("a cast of three was described as a two-hander")
	}
	// The beats already carry their names, so nothing may be prefixed onto them.
	if strings.Contains(got, "Vesper: Vesper:") {
		t.Error("a beat was given a second label")
	}
	if strings.Contains(got, "Vesper: Kestrel:") {
		t.Errorf("one character's name was put in front of another's line:\n%s", got)
	}
}

// And the wording for one character is unchanged, because a scene that has
// already been compacted must keep being compacted the same way or its notes
// change voice halfway through.
func TestCompactPromptIsUnchangedForOneCharacter(t *testing.T) {
	aged := []ollama.Message{
		{Role: ollama.RoleUser, Content: "Hello."},
		{Role: ollama.RoleAssistant, Content: "*She did not look up.*"},
	}
	one := Character{Name: "Vesper"}
	got := compactPrompt("", aged, []Character{one}, Persona{Name: "Wren"})
	if !strings.HasPrefix(got, "The two characters are Vesper and Wren.") {
		t.Errorf("the two-hander wording changed:\n%s", got)
	}
	if !strings.Contains(got, "Vesper: *She did not look up.*") {
		t.Errorf("a two-hander's reply lost its name in the record:\n%s", got)
	}
}
