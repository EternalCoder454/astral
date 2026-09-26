package chars

import (
	"strings"
	"testing"
)

func TestRelationsReachTheGroupPrompt(t *testing.T) {
	rels := []Relation{
		{A: "Vesper", B: "Kestrel", Note: "She trained her, and neither of them mentions it."},
		{A: "Kestrel", B: "Ash", Note: "He owes her money and has stopped answering."},
	}
	got := BuildGroupSystem(threeHanded(), Persona{Name: "Wren"}, rels)

	if !strings.Contains(got, "How they know each other") {
		t.Errorf("no section for the relations:\n%s", got)
	}
	for _, want := range []string{
		"Vesper and Kestrel: She trained her",
		"Kestrel and Ash: He owes her money",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt is missing %q", want)
		}
	}
	// The people come before what is between them, because it is about people
	// the model has just read about.
	if strings.Index(got, "## Vesper") > strings.Index(got, "How they know each other") {
		t.Error("the relations are described before the people they are about")
	}
}

func TestNoRelationsSectionWhenThereAreNone(t *testing.T) {
	got := BuildGroupSystem(threeHanded(), Persona{Name: "Wren"}, nil)
	if strings.Contains(got, "How they know each other") {
		t.Error("an empty relations section was sent, which is a heading and no content")
	}
	// And a blank note does not earn one either.
	blank := BuildGroupSystem(threeHanded(), Persona{Name: "Wren"},
		[]Relation{{A: "Vesper", B: "Kestrel", Note: "   "}})
	if strings.Contains(blank, "How they know each other") {
		t.Error("a blank note produced a section")
	}
}

func TestRelationWithAMissingNameIsDropped(t *testing.T) {
	// A character deleted out from under a relation leaves a name behind. The
	// line would read "and Kestrel: ..." and say nothing.
	got := BuildGroupSystem(threeHanded(), Persona{Name: "Wren"},
		[]Relation{{A: "", B: "Kestrel", Note: "Something."}})
	if strings.Contains(got, "How they know each other") {
		t.Error("a half-named relation reached the prompt")
	}
}
