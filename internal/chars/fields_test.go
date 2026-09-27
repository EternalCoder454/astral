package chars

import (
	"encoding/json"
	"strings"
	"testing"
)

// Appearance and voice were added to the card, the editor and the prompt, and
// not to the designer. So the designer interviewed about a voice, said it was the
// most valuable thing on a card, and then produced a card with the field empty.
//
// Every place that has to know about a field is checked here, because the way
// this went wrong was one of them being missed.
func TestNewFieldsReachEveryPlaceThatNeedsThem(t *testing.T) {
	t.Run("the designer asks for them", func(t *testing.T) {
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if err := json.Unmarshal(characterSchema, &schema); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"appearance", "speech"} {
			if _, ok := schema.Properties[f]; !ok {
				t.Errorf("the schema has no %q, so a designed character cannot have one", f)
			}
			found := false
			for _, r := range schema.Required {
				if r == f {
					found = true
				}
			}
			if !found {
				t.Errorf("%q is optional, so the model will leave it out", f)
			}
		}
	})

	t.Run("the instruction explains them", func(t *testing.T) {
		low := strings.ToLower(extractInstruction)
		for _, f := range []string{"appearance:", "speech:"} {
			if !strings.Contains(low, f) {
				t.Errorf("the extraction instruction never explains %q", f)
			}
		}
	})

	t.Run("a revision is shown them", func(t *testing.T) {
		got := ReviseSystem(Character{Name: "Vesper", Speech: "Short sentences."}, Persona{Name: "Wren"})
		if !strings.Contains(got, "Short sentences.") {
			t.Error("the revision prompt does not show the voice it is revising")
		}
		// Empty ones are named rather than omitted, because a card with no voice
		// is the thing a revision should notice first.
		if !strings.Contains(got, "Appearance: (empty)") {
			t.Errorf("an empty field was left out rather than named:\n%s", got)
		}
	})

	t.Run("a revision keeps them", func(t *testing.T) {
		was := Character{ID: 1, Name: "Vesper", Appearance: "Ink to the elbows.", Speech: "Clipped."}
		kept := Revise(was, Character{Description: "Rewritten."})
		if kept.Appearance != was.Appearance || kept.Speech != was.Speech {
			t.Errorf("a revision that did not mention them threw them away: %#v", kept)
		}
		changed := Revise(was, Character{Appearance: "Ink to the wrists.", Speech: "Slower now."})
		if changed.Appearance != "Ink to the wrists." || changed.Speech != "Slower now." {
			t.Errorf("a revision that rewrote them did not take: %#v", changed)
		}
	})

	t.Run("a card carries them both ways", func(t *testing.T) {
		// Flat, which is how the designer's own answer arrives.
		flat, err := ParseCard([]byte(`{"name":"V","appearance":"Tall.","speech":"Clipped."}`))
		if err != nil {
			t.Fatal(err)
		}
		if flat.Appearance != "Tall." || flat.Speech != "Clipped." {
			t.Errorf("a flat card lost them: %#v", flat)
		}
		// Nested, which is how Astral exports them.
		out, err := ExportCard(Character{Name: "V", Appearance: "Tall.", Speech: "Clipped."})
		if err != nil {
			t.Fatal(err)
		}
		back, err := ParseCard(out)
		if err != nil {
			t.Fatal(err)
		}
		if back.Appearance != "Tall." || back.Speech != "Clipped." {
			t.Errorf("a round trip lost them: %#v", back)
		}
	})

	t.Run("they reach the scene", func(t *testing.T) {
		sys := BuildSystem(Character{
			Name: "Vesper", Appearance: "Ink to the elbows.", Speech: "Clipped, never explains.",
		}, Persona{Name: "Wren"})
		for _, want := range []string{"Ink to the elbows.", "Clipped, never explains."} {
			if !strings.Contains(sys, want) {
				t.Errorf("the prompt never mentions %q", want)
			}
		}
	})
}
