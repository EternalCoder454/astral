package serve

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"astral/internal/store"
)

// Archiving from a phone has to reach the same flag the desktop's menu sets,
// show up in the list the phone draws from, and turn away a device that has
// not been paired.
func TestArchiveAChatFromAPhone(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)
	ch, err := s.store.NewChat(0, "A chat", "m", store.KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/chats/" + strconv.FormatInt(ch.ID, 10) + "/archive"

	archivedInState := func() bool {
		t.Helper()
		rec := do(t, s, "GET", "/api/state", token, "")
		var st struct {
			Chats []struct {
				ID       int64 `json:"id"`
				Archived bool  `json:"archived"`
			} `json:"chats"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || len(st.Chats) != 1 {
			t.Fatalf("state = %s, %v", rec.Body.String(), err)
		}
		return st.Chats[0].Archived
	}

	if archivedInState() {
		t.Fatal("a new chat is listed as archived")
	}
	if got := do(t, s, "POST", path, token, `{"archived": true}`).Code; got != http.StatusOK {
		t.Fatalf("archiving answered %d", got)
	}
	if !archivedInState() {
		t.Error("the state does not list the chat as archived")
	}
	if got, _ := s.store.Chat(ch.ID); !got.Archived {
		t.Error("the store does not hold the chat as archived")
	}

	if got := do(t, s, "POST", path, token, `{"archived": false}`).Code; got != http.StatusOK {
		t.Fatalf("unarchiving answered %d", got)
	}
	if archivedInState() {
		t.Error("the chat is still listed as archived after being brought back")
	}

	if got := do(t, s, "POST", path, token, "not json").Code; got != http.StatusBadRequest {
		t.Errorf("an unreadable body answered %d, want 400", got)
	}
	if got := do(t, s, "POST", "/api/chats/999999/archive", token, `{"archived": true}`).Code; got != http.StatusNotFound {
		t.Errorf("an unknown chat answered %d, want 404", got)
	}
	if got := do(t, s, "POST", path, "", `{"archived": true}`).Code; got == http.StatusOK {
		t.Error("an unpaired request was allowed to archive a chat")
	}
	if got, _ := s.store.Chat(ch.ID); got.Archived {
		t.Error("the refused requests changed the chat")
	}
}
