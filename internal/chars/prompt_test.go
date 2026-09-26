package chars

import (
	"strings"
	"testing"

	"astral/internal/ollama"
)

func testChar() Character {
	return Character{
		Name:        "Vesper",
		Description: "A cartographer. {{user}} has been here before.",
		Personality: "wry, guarded",
		Scenario:    "The map room, past midnight.",
		FirstMes:    "\"You're late, {{user}}.\"",
		MesExample:  "<START>\n{{user}}: Hello.\n{{char}}: \"Is it.\"\n<START>\n{{user}}: Again?\n{{char}}: \"Always.\"",
	}
}

func TestSubstitute(t *testing.T) {
	got := Substitute("{{char}} greets {{user}}; <BOT> nods at <USER>.", "Vesper", "Wren")
	want := "Vesper greets Wren; Vesper nods at Wren."
	if got != want {
		t.Errorf("Substitute = %q, want %q", got, want)
	}
}

func TestSubstituteFallsBackWhenUnnamed(t *testing.T) {
	got := Substitute("Hello {{user}}.", "Vesper", "")
	if !strings.Contains(got, DefaultPersonaName) {
		t.Errorf("unnamed persona not substituted: %q", got)
	}
}

func TestBuildSystemIncludesEverySection(t *testing.T) {
	sys := BuildSystem(testChar(), Persona{Name: "Wren", Description: "A courier."})
	for _, want := range []string{"Vesper", "wry, guarded", "map room", "A courier.", "Wren"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q:\n%s", want, sys)
		}
	}
	// Placeholders must be expanded, not passed through to the model.
	if strings.Contains(sys, "{{user}}") || strings.Contains(sys, "{{char}}") {
		t.Errorf("unexpanded placeholder in system prompt:\n%s", sys)
	}
	// The framing template uses the name three times; a leftover verb would
	// print as %!s(MISSING) into the prompt.
	if strings.Contains(sys, "%!") || strings.Contains(sys, "%s") {
		t.Errorf("framing template not fully filled:\n%s", sys)
	}
}

// Instructions are layered on top of the framing, never in place of it. The
// framing is what stops a local model narrating from outside the scene, so a
// character that replaced it wholesale played worse — which is the whole
// reason this is additive.
func TestInstructionsAddToFramingRatherThanReplacingIt(t *testing.T) {
	c := testChar()
	c.Instructions = "Speak only in questions. Never mention {{user}}'s job."
	sys := BuildSystem(c, Persona{Name: "Wren"})

	if !strings.Contains(sys, "Stay in character at all times") {
		t.Errorf("framing was dropped:\n%s", sys)
	}
	if !strings.Contains(sys, "Speak only in questions.") {
		t.Errorf("instructions missing:\n%s", sys)
	}
	if strings.Contains(sys, "{{user}}") {
		t.Errorf("placeholder not expanded in instructions:\n%s", sys)
	}
	// Position is most of what makes an instruction get followed, so they must
	// come after the character's own material, not before it.
	if strings.Index(sys, "Speak only in questions.") < strings.Index(sys, "map room") {
		t.Errorf("instructions appear before the character sections:\n%s", sys)
	}
	if !strings.Contains(sys, "take priority") {
		t.Errorf("instructions are not marked as outranking the framing:\n%s", sys)
	}
}

func TestExampleTurnsBecomeRealMessages(t *testing.T) {
	msgs := BuildMessages(testChar(), Scene{Persona: Persona{Name: "Wren"}, History: nil})
	if msgs[0].Role != ollama.RoleSystem {
		t.Fatalf("first message is %q, want system", msgs[0].Role)
	}
	// Everything between the system framing and the closing reminder is the
	// card's example dialogue, turned into real alternating turns.
	var roles []string
	for _, m := range msgs[1 : len(msgs)-1] {
		roles = append(roles, m.Role)
	}
	want := []string{"user", "assistant", "user", "assistant"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Errorf("example turns = %v, want %v", roles, want)
	}
	if msgs[1].Content != "Hello." {
		t.Errorf("example content = %q", msgs[1].Content)
	}
}

// A trailing user turn teaches a shape the model then tries to complete, which
// makes it answer the example instead of the actual conversation.
func TestExampleTurnsDropOddTrailingUser(t *testing.T) {
	c := testChar()
	c.MesExample = "<START>\n{{user}}: Hello.\n{{char}}: \"Is it.\"\n{{user}}: Dangling."
	msgs := BuildMessages(c, Scene{Persona: Persona{Name: "Wren"}, History: nil})
	last := msgs[len(msgs)-1]
	if last.Role == ollama.RoleUser {
		t.Errorf("trailing user example was kept: %q", last.Content)
	}
}

// The closing reminder goes after the transcript. A model weights the end of
// its context far above the middle, so by turn thirty the system prompt is
// competing with everything that has happened since.
func TestClosingReminderGoesAfterTheTranscript(t *testing.T) {
	c := testChar()
	c.Instructions = "Keep replies to one paragraph."
	history := []ollama.Message{
		{Role: ollama.RoleUser, Content: "Hi"},
		{Role: ollama.RoleAssistant, Content: "\"Hello.\""},
	}
	msgs := BuildMessages(c, Scene{Persona: Persona{Name: "Wren"}, History: history})

	last := msgs[len(msgs)-1]
	if last.Role != ollama.RoleSystem {
		t.Fatalf("last message role = %q, want system", last.Role)
	}
	for _, want := range []string{"Vesper", "Keep replies to one paragraph.", "narrate Wren's"} {
		if !strings.Contains(last.Content, want) {
			t.Errorf("closing reminder missing %q:\n%s", want, last.Content)
		}
	}
	// It has to come after the conversation, or it is just more system prompt.
	if msgs[len(msgs)-2].Content != "\"Hello.\"" {
		t.Errorf("reminder is not immediately after the transcript: %+v", msgs[len(msgs)-2])
	}
}

// Even with no instructions, the reminder still restates who they are: drifting
// out of character is the failure mode that needs no help to appear.
func TestClosingReminderWithoutInstructions(t *testing.T) {
	msgs := BuildMessages(testChar(), Scene{
		Persona: Persona{Name: "Wren"},
		History: []ollama.Message{{Role: ollama.RoleUser, Content: "Hi"}},
	})
	last := msgs[len(msgs)-1]
	if last.Role != ollama.RoleSystem || !strings.Contains(last.Content, "Vesper") {
		t.Errorf("no closing reminder without instructions: %+v", last)
	}
	if strings.Contains(last.Content, "Follow these instructions") {
		t.Errorf("reminder mentions instructions that do not exist:\n%s", last.Content)
	}
}

// A character with no instructions must not gain an empty section.
func TestNoInstructionsSectionWhenEmpty(t *testing.T) {
	sys := BuildSystem(testChar(), Persona{Name: "Wren"})
	if strings.Contains(sys, "## Instructions") {
		t.Errorf("empty instructions produced a heading:\n%s", sys)
	}
}

func TestGreetingExpandsPlaceholders(t *testing.T) {
	got := Greeting(testChar(), Persona{Name: "Wren"})
	if got != `"You're late, Wren."` {
		t.Errorf("Greeting = %q", got)
	}
}

func TestHistoryIsCarriedInOrder(t *testing.T) {
	history := []ollama.Message{
		{Role: ollama.RoleUser, Content: "one"},
		{Role: ollama.RoleAssistant, Content: "two"},
		{Role: ollama.RoleUser, Content: "three"},
	}
	msgs := BuildMessages(testChar(), Scene{Persona: Persona{Name: "Wren"}, History: history})
	var got []string
	for _, m := range msgs {
		if m.Content == "one" || m.Content == "two" || m.Content == "three" {
			got = append(got, m.Content)
		}
	}
	if strings.Join(got, ",") != "one,two,three" {
		t.Errorf("history order = %v", got)
	}
}

func TestInitial(t *testing.T) {
	cases := map[string]string{"Vesper": "V", " aria": "A", "": "?"}
	for in, want := range cases {
		if got := (Character{Name: in}).Initial(); got != want {
			t.Errorf("Initial(%q) = %q, want %q", in, got, want)
		}
	}
}

// Global instructions come from Settings and apply everywhere; a character's
// own come after, so the specific one wins where the two disagree.
func TestGlobalAndCharacterInstructionsLayer(t *testing.T) {
	c := testChar()
	c.Instructions = "Vesper never swears."
	p := Persona{Name: "Wren", GlobalInstructions: "Keep replies under three paragraphs."}

	sys := BuildSystem(c, p)
	gi := strings.Index(sys, "Keep replies under three paragraphs.")
	ci := strings.Index(sys, "Vesper never swears.")
	if gi < 0 || ci < 0 {
		t.Fatalf("an instruction set is missing:\n%s", sys)
	}
	if gi > ci {
		t.Error("the character's instructions came before the global ones; the specific must come last")
	}

	// Both must also reach the closing reminder, which is the placement that
	// actually gets obeyed deep into a scene.
	msgs := BuildMessages(c, Scene{Persona: p, History: []ollama.Message{{Role: ollama.RoleUser, Content: "Hi"}}})
	last := msgs[len(msgs)-1].Content
	for _, want := range []string{"Keep replies under three paragraphs.", "Vesper never swears."} {
		if !strings.Contains(last, want) {
			t.Errorf("closing reminder missing %q:\n%s", want, last)
		}
	}
}

func TestGlobalInstructionsAloneStillApply(t *testing.T) {
	sys := BuildSystem(testChar(), Persona{Name: "Wren", GlobalInstructions: "British spelling."})
	if !strings.Contains(sys, "British spelling.") {
		t.Errorf("global instructions ignored when the character has none:\n%s", sys)
	}
}

// Example dialogue is re-sent on every turn and is among the most token-heavy
// parts of a card. Once the real transcript can teach the voice, it stops.
func TestExamplesStopOnceTheSceneIsUnderway(t *testing.T) {
	c := testChar()
	p := Persona{Name: "Wren"}

	short := BuildMessages(c, Scene{Persona: p, History: make([]ollama.Message, 2)})
	if !containsContent(short, "Hello.") {
		t.Error("examples were dropped while the scene was still new")
	}

	long := make([]ollama.Message, exampleCutoff)
	for i := range long {
		long[i] = ollama.Message{Role: ollama.RoleUser, Content: "turn"}
	}
	if containsContent(BuildMessages(c, Scene{Persona: p, History: long}), "Hello.") {
		t.Errorf("examples were still sent after %d turns", exampleCutoff)
	}
}

func containsContent(msgs []ollama.Message, want string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, want) {
			return true
		}
	}
	return false
}

// A scene that outgrows the context window must lose its oldest turns here,
// where we choose what goes. Left to the server it drops the *front* of the
// prompt instead — the framing and the character — so the model keeps the
// small talk and forgets who it is playing.
func TestTrimHistoryKeepsTheMostRecentTurns(t *testing.T) {
	var history []ollama.Message
	for i := 0; i < 50; i++ {
		history = append(history, ollama.Message{
			Role:    ollama.RoleUser,
			Content: strings.Repeat("x", 1000) + string(rune('A'+i%26)),
		})
	}
	out := trimHistory(history, 5000)
	if len(out) == 0 || len(out) >= len(history) {
		t.Fatalf("trimmed to %d of %d turns", len(out), len(history))
	}
	total := 0
	for _, m := range out {
		total += len(m.Content)
	}
	if total > 5000 {
		t.Errorf("kept %d chars, over the %d budget", total, 5000)
	}
	// It must keep the END of the conversation, not the beginning.
	if out[len(out)-1].Content != history[len(history)-1].Content {
		t.Error("the newest turn was dropped")
	}
}

func TestTrimHistoryLeavesShortScenesAlone(t *testing.T) {
	history := []ollama.Message{
		{Role: ollama.RoleUser, Content: "one"},
		{Role: ollama.RoleAssistant, Content: "two"},
	}
	if out := trimHistory(history, 24000); len(out) != 2 {
		t.Errorf("a short scene was trimmed to %d turns", len(out))
	}
}

// One turn longer than the entire budget must still be sent, or the scene
// deadlocks with nothing to reply to.
func TestTrimHistoryKeepsAnOversizedFinalTurn(t *testing.T) {
	history := []ollama.Message{
		{Role: ollama.RoleUser, Content: strings.Repeat("y", 100)},
		{Role: ollama.RoleUser, Content: strings.Repeat("z", 9000)},
	}
	out := trimHistory(history, 500)
	if len(out) != 1 || !strings.HasPrefix(out[0].Content, "z") {
		t.Errorf("oversized final turn not preserved: %d turns", len(out))
	}
}

// The framing has to actually state the formatting rule, because the renderer
// depends on the model following it.
func TestFramingSpecifiesTheWritingStyle(t *testing.T) {
	sys := BuildSystem(testChar(), Persona{Name: "Wren"})
	for _, want := range []string{"*single asterisks*", "double quotes", "Never write an unmarked sentence"} {
		if !strings.Contains(sys, want) {
			t.Errorf("framing does not state %q:\n%s", want, sys)
		}
	}
}

// The active style supplies the "how it should sound" block, and must not be
// able to disturb the structural framing the renderer depends on.
func TestWritingStyleReplacesOnlyTheVoiceBlock(t *testing.T) {
	c := testChar()
	p := Persona{Name: "Wren", Style: WritingStyle{
		Name:         "Sparse",
		Instructions: "One paragraph. No adverbs. Never describe weather.",
	}}
	sys := BuildSystem(c, p)

	if !strings.Contains(sys, "One paragraph. No adverbs.") {
		t.Errorf("style instructions missing:\n%s", sys)
	}
	// The default's guidance must be gone — a style replaces the block.
	if strings.Contains(sys, "Real speech is shorter than written prose") {
		t.Errorf("the default style leaked in alongside a custom one:\n%s", sys)
	}
	// ...but the structure and format rules must survive it untouched.
	for _, want := range []string{"*single asterisks*", "double quotes", "Never write, decide, or narrate", "never break character"} {
		if !strings.Contains(sys, want) {
			t.Errorf("a style displaced structural framing %q:\n%s", want, sys)
		}
	}
}

func TestZeroStyleFallsBackToTheDefault(t *testing.T) {
	sys := BuildSystem(testChar(), Persona{Name: "Wren"})
	if !strings.Contains(sys, "Real speech is shorter than written prose") {
		t.Errorf("an unset style did not fall back to the default:\n%s", sys)
	}
}

// An empty instructions field is the same as no style: a scene with no voice
// guidance at all drifts into summary within a few turns.
func TestEmptyStyleFallsBackToTheDefault(t *testing.T) {
	got := WritingStyle{Name: "Blank", Instructions: "   "}.Resolved()
	if !strings.Contains(got, "Real speech is shorter") {
		t.Errorf("Resolved() = %q", got)
	}
}

// Every free-text field has to expand placeholders. The writing style was
// missing from this list and reached the model as the literal "{{char}}".
func TestPlaceholdersExpandInEveryField(t *testing.T) {
	c := Character{
		Name:         "Vesper",
		Description:  "{{char}} has met {{user}} before.",
		Personality:  "wary of {{user}}",
		Scenario:     "{{user}} arrives at {{char}}'s door.",
		Instructions: "Never let {{char}} mention {{user}}'s job.",
	}
	p := Persona{
		Name:               "Wren",
		Description:        "{{user}} is a courier who reads {{char}}'s letters.",
		GlobalInstructions: "Address {{user}} by name.",
		Style:              WritingStyle{Name: "Terse", Instructions: "Keep {{char}} clipped."},
	}

	sys := BuildSystem(c, p)
	for _, leftover := range []string{"{{char}}", "{{user}}", "{char}", "{user}"} {
		if strings.Contains(sys, leftover) {
			t.Errorf("unexpanded %s survived into the prompt:\n%s", leftover, sys)
		}
	}
	for _, want := range []string{
		"Vesper has met Wren before.",
		"wary of Wren",
		"Wren arrives at Vesper's door.",
		"Never let Vesper mention Wren's job.",
		"Wren is a courier who reads Vesper's letters.",
		"Address Wren by name.",
		"Keep Vesper clipped.",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("missing %q:\n%s", want, sys)
		}
	}
}

// Single braces are what people type when they have not read a spec.
func TestSingleBracePlaceholders(t *testing.T) {
	got := Substitute("{char} greets {user}; {{char}} waves at {{user}}.", "Vesper", "Wren")
	want := "Vesper greets Wren; Vesper waves at Wren."
	if got != want {
		t.Errorf("Substitute = %q, want %q", got, want)
	}
}

// A double-brace placeholder must not be left with a stray brace by the
// single-brace rule matching inside it.
func TestDoubleBraceWinsOverSingle(t *testing.T) {
	if got := Substitute("{{char}}", "Vesper", "Wren"); got != "Vesper" {
		t.Errorf("Substitute(\"{{char}}\") = %q, want %q", got, "Vesper")
	}
}

// Cards carry several openings and Astral only ever showed the first, while
// importing, storing and exporting the rest.
func TestGreetingsOffersEveryOpening(t *testing.T) {
	c := Character{
		Name:         "Vesper",
		FirstMes:     "  *She does not look up.* \"You're late, {{user}}.\"  ",
		AltGreetings: []string{"*The door is already open.*", "   ", "\"Again?\""},
	}
	p := Persona{Name: "Wren"}

	all := Greetings(c)
	if len(all) != 3 {
		t.Fatalf("got %d openings, want 3 (the blank one dropped): %q", len(all), all)
	}
	if got := GreetingAt(c, p, 0); !strings.Contains(got, "You're late, Wren.") {
		t.Errorf("the first opening did not substitute: %q", got)
	}
	if got := GreetingAt(c, p, 1); got != "*The door is already open.*" {
		t.Errorf("second opening = %q", got)
	}
	// Stepping past the end wraps, so a caller needs no bounds of its own.
	if GreetingAt(c, p, 3) != GreetingAt(c, p, 0) {
		t.Error("stepping past the last opening did not wrap")
	}
	if GreetingAt(c, p, -1) != GreetingAt(c, p, 2) {
		t.Error("stepping back from the first did not wrap")
	}
	// And Greeting stays what it was.
	if Greeting(c, p) != GreetingAt(c, p, 0) {
		t.Error("Greeting is no longer the first opening")
	}
}

func TestGreetingsWithNothingToShow(t *testing.T) {
	if got := Greetings(Character{}); len(got) != 0 {
		t.Errorf("a character with no openings produced %q", got)
	}
	if got := GreetingAt(Character{}, Persona{}, 2); got != "" {
		t.Errorf("GreetingAt on nothing = %q", got)
	}
}
