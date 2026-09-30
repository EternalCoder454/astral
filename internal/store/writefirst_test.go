package store

import (
	"testing"
	"time"

	"astral/internal/ollama"
)

func TestWriteFirstRoundTrip(t *testing.T) {
	s := openTest(t)
	ch, _ := s.NewChat(0, "Scene", "m", KindRoleplay)
	if got, _ := s.Chat(ch.ID); got.WriteFirst != 0 || got.NudgedAt != 0 {
		t.Fatalf("a new chat writes first after %d minutes, nudged at %d", got.WriteFirst, got.NudgedAt)
	}
	if err := s.SetChatWriteFirst(ch.ID, 60); err != nil {
		t.Fatal(err)
	}
	mid, _ := s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: "Hello."})
	if err := s.SetChatNudged(ch.ID, mid); err != nil {
		t.Fatal(err)
	}
	got, err := s.Chat(ch.ID)
	if err != nil || got.WriteFirst != 60 || got.NudgedAt != mid {
		t.Fatalf("Chat read back %d minutes and nudged at %d (want 60 and %d), %v", got.WriteFirst, got.NudgedAt, mid, err)
	}
	list, _ := s.Chats()
	if len(list) != 1 || list[0].WriteFirst != 60 || list[0].NudgedAt != mid {
		t.Fatalf("Chats read back %+v", list)
	}

	// Nonsense is refused, and the wait is left as it was.
	for _, bad := range []int{-1, MaxWriteFirst + 1} {
		if err := s.SetChatWriteFirst(ch.ID, bad); err == nil {
			t.Errorf("a wait of %d minutes was accepted", bad)
		}
	}
	if got, _ := s.Chat(ch.ID); got.WriteFirst != 60 {
		t.Errorf("a refused wait changed it to %d", got.WriteFirst)
	}
}

func TestABranchWritesFirstToo(t *testing.T) {
	s := openTest(t)
	ch, _ := s.NewChat(0, "Scene", "m", KindRoleplay)
	a, _ := s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: "Hi."})
	b, _ := s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: "Hello."})
	s.SetChatWriteFirst(ch.ID, 10)
	s.SetChatNudged(ch.ID, b)

	branch, err := s.BranchChat(ch.ID, b, "Branch")
	if err != nil {
		t.Fatal(err)
	}
	if branch.WriteFirst != 10 {
		t.Errorf("the branch writes first after %d minutes, want 10", branch.WriteFirst)
	}
	// The message it points at is not one of the branch's.
	if branch.NudgedAt != 0 {
		t.Errorf("the branch carries nudged_at %d from the chat it came from (%d)", branch.NudgedAt, a)
	}
}

func TestChatsWritingFirstAndTheLastMessage(t *testing.T) {
	s := openTest(t)
	on, _ := s.NewChat(0, "On", "m", KindRoleplay)
	off, _ := s.NewChat(0, "Off", "m", KindRoleplay)
	away, _ := s.NewChat(0, "Away", "m", KindRoleplay)
	s.SetChatWriteFirst(on.ID, 10)
	s.SetChatWriteFirst(away.ID, 10)
	s.SetChatArchived(away.ID, true)

	got, err := s.ChatsWritingFirst()
	if err != nil || len(got) != 1 || got[0].ID != on.ID {
		t.Fatalf("ChatsWritingFirst = %+v, %v; want only the chat set to and not put away (off is %d)", got, err, off.ID)
	}

	if _, ok, err := s.LastMessage(on.ID); ok || err != nil {
		t.Fatalf("an empty chat has a last message: %v %v", ok, err)
	}
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	s.AddMessage(Message{ChatID: on.ID, Role: ollama.RoleUser, Content: "one"})
	s.AddMessage(Message{ChatID: on.ID, Role: ollama.RoleAssistant, Content: "two", CreatedAt: at})
	last, ok, err := s.LastMessage(on.ID)
	if err != nil || !ok || last.Content != "two" || last.Role != ollama.RoleAssistant || !last.CreatedAt.Equal(at) {
		t.Fatalf("LastMessage = %+v, %v, %v", last, ok, err)
	}
}
