package serve

import (
	"net/http"
	"strconv"
	"testing"

	"astral/internal/chars"
	"astral/internal/store"
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

// A phone renames a chat and edits a turn through the same store calls the
// window uses, and is refused what makes no sense.
func TestRenameChatAndEditMessageFromAPhone(t *testing.T) {
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
	mid, err := s.store.AddMessage(store.Message{ChatID: ch.ID, Role: "user", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/chats/" + strconv.FormatInt(ch.ID, 10)

	if got := do(t, s, "POST", base+"/title", token, `{"title":"  Harbour Night "}`).Code; got != http.StatusOK {
		t.Fatalf("renaming answered %d", got)
	}
	if c, _ := s.store.Chat(ch.ID); c.Title != "Harbour Night" {
		t.Errorf("title = %q", c.Title)
	}
	if got := do(t, s, "POST", base+"/title", token, `{"title":"   "}`).Code; got != http.StatusBadRequest {
		t.Errorf("a blank title answered %d, want 400", got)
	}
	if got := do(t, s, "POST", "/api/chats/999/title", token, `{"title":"x"}`).Code; got != http.StatusNotFound {
		t.Errorf("renaming a missing chat answered %d, want 404", got)
	}

	edit := base + "/messages/" + strconv.FormatInt(mid, 10) + "/edit"
	if got := do(t, s, "POST", edit, token, `{"content":"hello again"}`).Code; got != http.StatusOK {
		t.Fatalf("editing answered %d", got)
	}
	if msgs, _ := s.store.Messages(ch.ID); len(msgs) != 1 || msgs[0].Content != "hello again" {
		t.Errorf("messages = %+v", msgs)
	}
	if got := do(t, s, "POST", edit, token, `{"content":" "}`).Code; got != http.StatusBadRequest {
		t.Errorf("a blank edit answered %d, want 400", got)
	}
	if got := do(t, s, "POST", base+"/messages/9999/edit", token, `{"content":"x"}`).Code; got != http.StatusNotFound {
		t.Errorf("editing a message not in the chat answered %d, want 404", got)
	}
	if got := do(t, s, "POST", edit, "", `{"content":"x"}`).Code; got != http.StatusUnauthorized {
		t.Errorf("editing without a token answered %d, want 401", got)
	}
}

// Out of range settings are refused, and the old value stays.
func TestSettingsRefuseOutOfRange(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)
	before := s.config()
	for _, body := range []string{
		`{"num_ctx":100}`, `{"temperature":5}`, `{"temperature":-1}`,
		`{"num_predict":-3}`, `{"persona":"  "}`,
	} {
		if got := do(t, s, "POST", "/api/settings", token, body).Code; got != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400", body, got)
		}
	}
	if s.config().NumCtx != before.NumCtx || s.config().Temperature != before.Temperature {
		t.Error("a refused save changed the config")
	}
	if got := do(t, s, "POST", "/api/settings", token, `{"num_ctx":4096,"temperature":2,"num_predict":0}`).Code; got != http.StatusOK {
		t.Errorf("in-range values answered %d", got)
	}
}
