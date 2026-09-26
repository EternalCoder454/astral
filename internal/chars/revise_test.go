package chars

import (
	"strings"
	"testing"
)

func existingCharacter() Character {
	return Character{
		ID:           7,
		Name:         "Vesper",
		Description:  "A cartographer. Guarded, precise.",
		Personality:  "dry",
		Scenario:     "The map room, past midnight.",
		FirstMes:     "\"You're late.\"",
		Instructions: "She never explains herself.",
		AltGreetings: []string{"\"Again?\""},
		Tags:         []string{"maps"},
		AvatarPath:   "/pics/vesper.png",
		PortraitPath: "/pics/vesper-full.png",
		WorldID:      3,
		Accent:       2,
		Creator:      "Somebody",
	}
}

// TestReviseKeepsIdentity is the property the whole feature rests on. A revision
// has to leave every scene this character is in still pointing at them, which
// means the row, the pictures and the world all survive.
func TestReviseKeepsIdentity(t *testing.T) {
	was := existingCharacter()
	got := Revise(was, Character{
		Name:        "Vesper Quill",
		Description: "She charts coastlines that have not settled, and does not look up.",
		Personality: "dry, exact, unhurried",
	})

	if got.ID != was.ID {
		t.Errorf("the row changed: %d, want %d", got.ID, was.ID)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"avatar", got.AvatarPath, was.AvatarPath},
		{"portrait", got.PortraitPath, was.PortraitPath},
		{"creator", got.Creator, was.Creator},
	} {
		if tc.got != tc.want {
			t.Errorf("%s changed: %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if got.WorldID != was.WorldID || got.Accent != was.Accent {
		t.Errorf("world or tint changed: %d, %d", got.WorldID, got.Accent)
	}
	// And the text it was asked to rewrite did change.
	if got.Name != "Vesper Quill" || !strings.Contains(got.Description, "coastlines") {
		t.Errorf("the revision did not take: %q / %q", got.Name, got.Description)
	}
}

// TestReviseKeepsWhatWasNotDiscussed is the other half. The schema the model
// answers has no field for instructions or alternate openings, so a revision that
// took the written card wholesale would silently delete them.
func TestReviseKeepsWhatWasNotDiscussed(t *testing.T) {
	was := existingCharacter()
	got := Revise(was, Character{Description: "Rewritten."})

	if got.Instructions != was.Instructions {
		t.Errorf("instructions were lost: %q", got.Instructions)
	}
	if len(got.AltGreetings) != len(was.AltGreetings) {
		t.Errorf("alternate openings were lost: %#v", got.AltGreetings)
	}
	// A field the conversation did not touch keeps its old text rather than
	// being blanked.
	if got.Scenario != was.Scenario {
		t.Errorf("scenario was blanked: %q", got.Scenario)
	}
	if got.FirstMes != was.FirstMes {
		t.Errorf("the opening message was blanked: %q", got.FirstMes)
	}
	if got.Description != "Rewritten." {
		t.Errorf("the one field that was rewritten did not take: %q", got.Description)
	}
}

func TestReviseIgnoresBlankAnswers(t *testing.T) {
	was := existingCharacter()
	got := Revise(was, Character{Name: "   ", Description: "\n\t "})
	if got.Name != was.Name || got.Description != was.Description {
		t.Errorf("a blank answer overwrote a real field: %q / %q", got.Name, got.Description)
	}
}

// TestReviseSystemPutsTheCardOnTheTable is what makes it a revision rather than a
// second first draft: the model has to be able to read what is already there.
func TestReviseSystemPutsTheCardOnTheTable(t *testing.T) {
	got := ReviseSystem(existingCharacter(), Persona{Name: "Wren"})
	for _, want := range []string{
		"Vesper", "A cartographer. Guarded, precise.", "The map room, past midnight.",
		"She never explains herself.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt never shows %q", want)
		}
	}
	// An empty field is named rather than omitted, because the thing a revision
	// should notice is that this character has no example dialogue.
	if !strings.Contains(got, "Example dialogue: (empty)") {
		t.Errorf("an empty field was left out rather than named:\n%s", got)
	}
	// And it is told to open with a criticism, because a model handed a card and
	// asked to help spends three replies praising it otherwise.
	if !strings.Contains(strings.ToLower(got), "weakest") {
		t.Error("the prompt does not ask the model to say what is weakest")
	}
	if !strings.Contains(got, "Save Character") {
		t.Error("the prompt names the wrong button, so the interview never ends")
	}
}

func TestReviseOpeningNamesTheCharacter(t *testing.T) {
	got := ReviseOpening(existingCharacter())
	if !strings.Contains(got, "Vesper") {
		t.Errorf("the opening does not name them: %q", got)
	}
	if !strings.Contains(got, "Save Character") {
		t.Error("the opening does not say how to finish")
	}
	// A nameless character still gets a sentence rather than a gap.
	if got := ReviseOpening(Character{}); strings.Contains(got, "  ") {
		t.Errorf("a nameless character left a hole in the opening: %q", got)
	}
}
