package serve

import (
	"net/http"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// seedScene puts a short scene in the store and returns the chat.
func seedScene(t *testing.T, s *Server) store.Chat {
	t.Helper()
	id, err := s.store.SaveCharacter(chars.Character{Name: "Vesper", Description: "A cartographer."})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.store.NewChatIn(id, 0, "A scene", "m", store.KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.Message{
		{ChatID: ch.ID, Role: ollama.RoleUser, Content: "Hello."},
		{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: "A reply."},
	} {
		if _, err := s.store.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	return ch
}

func messageIDs(t *testing.T, s *Server, chatID int64) []int64 {
	t.Helper()
	msgs, err := s.store.Messages(chatID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	return ids
}

// A turn can be deleted from the phone, and only from the scene it is in.
func TestDeleteMessageNeedsTheRightChat(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)
	ch := seedScene(t, s)
	other := seedScene(t, s)
	ids := messageIDs(t, s, ch.ID)

	// Naming the wrong chat deletes nothing, and says nothing went wrong:
	// there is no message by that id in that chat, which is the same answer
	// as deleting one that was already gone.
	path := "/api/chats/" + itoa(other.ID) + "/messages/" + itoa(ids[0])
	if got := do(t, s, "DELETE", path, token, ""); got.Code != http.StatusOK {
		t.Fatalf("answered %d", got.Code)
	}
	if now := messageIDs(t, s, ch.ID); len(now) != len(ids) {
		t.Errorf("a message was deleted out of another chat: %v then %v", ids, now)
	}

	// Naming the right one deletes it.
	path = "/api/chats/" + itoa(ch.ID) + "/messages/" + itoa(ids[0])
	if got := do(t, s, "DELETE", path, token, ""); got.Code != http.StatusOK {
		t.Fatalf("answered %d", got.Code)
	}
	now := messageIDs(t, s, ch.ID)
	if len(now) != len(ids)-1 || now[0] == ids[0] {
		t.Errorf("wanted %v minus its first, got %v", ids, now)
	}
}

// Regenerating with nothing to regenerate is refused rather than producing an
// empty turn, and the transcript is left alone.
func TestRegenerateNeedsAReply(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)
	id, err := s.store.SaveCharacter(chars.Character{Name: "Vesper"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.store.NewChatIn(id, 0, "A scene", "m", store.KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.AddMessage(store.Message{
		ChatID: ch.ID, Role: ollama.RoleUser, Content: "Hello.",
	}); err != nil {
		t.Fatal(err)
	}
	got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/regenerate", token, "{}")
	if got.Code != http.StatusBadRequest {
		t.Fatalf("answered %d, want 400", got.Code)
	}
	if ids := messageIDs(t, s, ch.ID); len(ids) != 1 {
		t.Errorf("the turn was rewound anyway: %v", ids)
	}
}

// Two generations must not run on one chat at once: the window and a phone can
// both be in the same scene, and two replies written into it interleave.
func TestOneGenerationPerChat(t *testing.T) {
	s, _ := testServer(t)
	ch := seedScene(t, s)

	release, free := s.busy.claim(ch.ID)
	if !free {
		t.Fatal("a fresh chat was already claimed")
	}
	if _, free := s.busy.claim(ch.ID); free {
		t.Error("the same chat was claimed twice")
	}
	// A different scene is unaffected: the guard is per chat, not a queue for
	// the whole server.
	if _, free := s.busy.claim(ch.ID + 1); !free {
		t.Error("claiming one chat blocked another")
	}
	release()
	if _, free := s.busy.claim(ch.ID); !free {
		t.Error("the chat stayed claimed after it was released")
	}
}

// A refused claim still hands back a usable release, so a caller that defers it
// without looking cannot panic or free somebody else's claim.
func TestRefusedClaimIsStillSafeToRelease(t *testing.T) {
	var b busyChats
	release, free := b.claim(7)
	if !free {
		t.Fatal("the first claim was refused")
	}
	refused, free := b.claim(7)
	if free {
		t.Fatal("the second claim was allowed")
	}
	refused() // must not free the live claim
	if _, free := b.claim(7); free {
		t.Error("releasing a refused claim freed the chat")
	}
	release()
}
