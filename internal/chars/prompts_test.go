package chars

import (
	"strings"
	"testing"

	"astral/internal/prompts"
)

// Your version of a prompt is what is sent, everywhere it is sent, and putting
// the original back sends Astral's again.
func TestYourVersionIsWhatIsSent(t *testing.T) {
	c := Character{Name: "Vesper", Description: "A cartographer."}
	p := Persona{Name: "Wren"}
	defer prompts.SetOverrides(nil)

	prompts.SetOverrides(map[string]string{
		"scene.framing":      "You play {{char}} opposite {{user}}, and nobody else.",
		"designer.character": "Ask about the character, one question at a time.",
	})
	sys := BuildSystem(c, p)
	if !strings.Contains(sys, "You play Vesper opposite Wren, and nobody else.") || strings.Contains(sys, "Stay in character at all times") {
		t.Errorf("the scene did not use your framing:\n%s", sys[:200])
	}
	if DesignerPrompt() != "Ask about the character, one question at a time." {
		t.Error("the designer did not use your prompt")
	}

	prompts.SetOverrides(nil)
	if !strings.Contains(BuildSystem(c, p), "Stay in character at all times") {
		t.Error("the original did not come back")
	}
}

// Every registered prompt is complete enough to list and to optimize.
func TestEveryPromptIsDescribed(t *testing.T) {
	all := prompts.All()
	if len(all) < 15 {
		t.Fatalf("only %d prompts registered", len(all))
	}
	for _, p := range all {
		if p.ID == "" || p.Name == "" || p.Group == "" || strings.TrimSpace(p.About) == "" || strings.TrimSpace(p.Default) == "" {
			t.Errorf("%q is missing something: %+v", p.ID, p)
		}
		if strings.ContainsAny(p.About+p.Keep, "\u2014\u2013") {
			t.Errorf("%s's description uses a dash", p.ID)
		}
	}
}
