package store

import (
	"testing"

	"astral/internal/chars"
)

// Deleting a character used to leave its relations and its place in every cast
// behind. Nothing broke, because both are read through a join, but the rows
// stayed for ever and a scene held a row saying somebody was in it who was not.
func TestDeletingACharacterTakesItsRowsWithIt(t *testing.T) {
	s := openTest(t)

	a, err := s.SaveCharacter(chars.Character{Name: "Vesper"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SaveCharacter(chars.Character{Name: "Kestrel"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.NewChat(a, "A scene", "m", KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCast(ch.ID, []int64{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRelation(a, b, "They trained together."); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteCharacter(a); err != nil {
		t.Fatal(err)
	}

	var relations, castRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM relations WHERE a_id = ? OR b_id = ?`, a, a).
		Scan(&relations); err != nil {
		t.Fatal(err)
	}
	if relations != 0 {
		t.Errorf("%d relation rows left behind", relations)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM chat_cast WHERE character_id = ?`, a).
		Scan(&castRows); err != nil {
		t.Fatal(err)
	}
	if castRows != 0 {
		t.Errorf("%d cast rows left behind", castRows)
	}

	// The one still in the scene stays in it, and the scene itself survives.
	cast, err := s.Cast(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cast) != 1 || cast[0].ID != b {
		t.Errorf("wanted only Kestrel left in the cast, got %d members", len(cast))
	}
	if _, err := s.Chat(ch.ID); err != nil {
		t.Errorf("the scene went with the character: %v", err)
	}
}
