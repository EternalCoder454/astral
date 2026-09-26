package store

import (
	"testing"
)

func TestRelationIsTheSamePairEitherWayRound(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel")

	if err := s.SetRelation(ids[0], ids[1], "She trained her."); err != nil {
		t.Fatal(err)
	}
	// Written the other way round, it is the same relation rather than a second
	// one. Otherwise a pair could hold two contradictory lines and the prompt
	// would carry both.
	if err := s.SetRelation(ids[1], ids[0], "She trained her, and neither mentions it."); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		rels, err := s.Relations(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(rels) != 1 {
			t.Fatalf("character %d has %d relations, want 1", id, len(rels))
		}
		if rels[0].Note != "She trained her, and neither mentions it." {
			t.Errorf("the note was not replaced: %q", rels[0].Note)
		}
	}
}

func TestRelationReadsFromBothEnds(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel")
	if err := s.SetRelation(ids[0], ids[1], "She owes him money."); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		rels, _ := s.Relations(id)
		if len(rels) != 1 {
			t.Fatalf("end %d sees %d relations", i, len(rels))
		}
		// Each end sees the other person, never itself.
		if rels[0].Other.ID == id {
			t.Errorf("end %d sees itself at the far end", i)
		}
	}
}

func TestEmptyRelationIsRemoved(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel")
	if err := s.SetRelation(ids[0], ids[1], "Something."); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRelation(ids[0], ids[1], "   "); err != nil {
		t.Fatal(err)
	}
	if rels, _ := s.Relations(ids[0]); len(rels) != 0 {
		t.Errorf("a relation with nothing to say survived: %#v", rels)
	}
}

func TestRelationRefusesNonsense(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper")
	// A character cannot have a relation with themselves, and a zero id is not
	// anybody.
	if err := s.SetRelation(ids[0], ids[0], "Self."); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRelation(ids[0], 0, "Nobody."); err != nil {
		t.Fatal(err)
	}
	if rels, _ := s.Relations(ids[0]); len(rels) != 0 {
		t.Errorf("stored %d nonsense relations", len(rels))
	}
}

// TestRelationsAmongOnlyPairsBothPresent is the property that makes this cheap
// enough to send every turn, and the one that keeps a scene from being told about
// somebody who is not in it.
func TestRelationsAmongOnlyPairsBothPresent(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel", "Ash")

	if err := s.SetRelation(ids[0], ids[1], "She trained her."); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRelation(ids[0], ids[2], "They have never met."); err != nil {
		t.Fatal(err)
	}

	// Only the first two are in the scene, so only their line is sent.
	got, err := s.RelationsAmong(ids[:2])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d relations, want 1: %#v", len(got), got)
	}
	if got[0].Note != "She trained her." {
		t.Errorf("the wrong pair came back: %#v", got[0])
	}
	if got[0].A == "" || got[0].B == "" {
		t.Errorf("a relation arrived without both names: %#v", got[0])
	}

	// All three present: both lines.
	if all, _ := s.RelationsAmong(ids); len(all) != 2 {
		t.Errorf("with everybody present, got %d relations, want 2", len(all))
	}
	// A scene with one character has no pairs at all.
	if one, _ := s.RelationsAmong(ids[:1]); len(one) != 0 {
		t.Errorf("a two-hander was given %d relations", len(one))
	}
}

func TestRelationIsBounded(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel")
	long := ""
	for len(long) < MaxRelationChars*2 {
		long += "a shared history that will not end. "
	}
	if err := s.SetRelation(ids[0], ids[1], long); err != nil {
		t.Fatal(err)
	}
	rels, _ := s.Relations(ids[0])
	if len(rels) != 1 {
		t.Fatal("the relation was not stored")
	}
	if len(rels[0].Note) > MaxRelationChars {
		t.Errorf("stored %d characters, want at most %d", len(rels[0].Note), MaxRelationChars)
	}
}
