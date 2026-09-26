package serve

import (
	"net/http"
	"strconv"
	"testing"

	"astral/internal/chars"
	"astral/internal/world"
)

// The cast page on a phone can delete now, so the endpoints behind that gesture
// have to do what the desktop does and no more.
func TestDeleteCharacterAndWorldFromAPhone(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)

	charID, err := s.store.SaveCharacter(chars.Character{Name: "Vesper"})
	if err != nil {
		t.Fatal(err)
	}
	worldID, err := s.store.SaveWorld(world.World{Name: "Sever Reach"})
	if err != nil {
		t.Fatal(err)
	}

	if got := do(t, s, "DELETE", "/api/characters/"+strconv.FormatInt(charID, 10), token, "").Code; got != http.StatusOK {
		t.Errorf("deleting a character answered %d", got)
	}
	if _, err := s.store.Character(charID); err == nil {
		t.Error("the character survived being deleted")
	}

	if got := do(t, s, "DELETE", "/api/worlds/"+strconv.FormatInt(worldID, 10), token, "").Code; got != http.StatusOK {
		t.Errorf("deleting a world answered %d", got)
	}
	if _, err := s.store.World(worldID); err == nil {
		t.Error("the world survived being deleted")
	}
}

// Deleting a character keeps the scenes played with them, the same as on the
// desktop: a transcript is yours, and losing one because you tidied up the cast
// is not a trade anybody would choose.
func TestDeletingACharacterKeepsItsChats(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)
	charID, err := s.store.SaveCharacter(chars.Character{Name: "Vesper"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.store.NewChat(charID, "A scene", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := do(t, s, "DELETE", "/api/characters/"+strconv.FormatInt(charID, 10), token, "").Code; got != http.StatusOK {
		t.Fatalf("deleting answered %d", got)
	}
	if _, err := s.store.Chat(ch.ID); err != nil {
		t.Errorf("the scene was deleted with the character: %v", err)
	}
}

func TestDeleteRefusesRubbish(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)
	for _, path := range []string{"/api/characters/not-a-number", "/api/worlds/nope"} {
		if got := do(t, s, "DELETE", path, token, "").Code; got != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400", path, got)
		}
	}
}
