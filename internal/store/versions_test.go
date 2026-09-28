package store

import (
	"path/filepath"
	"testing"
)

// Writing a reply again keeps the one before it. The versions come back with
// the message, the content column always holds the one showing, and an edit
// changes that one without touching the others.
func TestReplyVersions(t *testing.T) {
	s, _, err := Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ch, err := s.NewChatIn(0, 0, "A chat", "m", KindAssistant)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddMessage(Message{ChatID: ch.ID, Role: "assistant", Content: "second",
		Versions: []Version{{Content: "first", Thinking: "hm"}, {Content: "second"}}, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Messages(ch.ID)
	if len(msgs) != 1 || len(msgs[0].Versions) != 2 || msgs[0].Version != 1 || msgs[0].Content != "second" {
		t.Fatalf("read back %+v", msgs)
	}

	v, err := s.SetMessageVersion(id, 0)
	if err != nil || v.Content != "first" {
		t.Fatalf("flip: %+v %v", v, err)
	}
	msgs, _ = s.Messages(ch.ID)
	if msgs[0].Content != "first" || msgs[0].Thinking != "hm" || msgs[0].Version != 0 {
		t.Errorf("after the flip the message shows %+v", msgs[0])
	}

	if err := s.SetMessageContent(id, "first, edited"); err != nil {
		t.Fatal(err)
	}
	msgs, _ = s.Messages(ch.ID)
	if msgs[0].Versions[0].Content != "first, edited" || msgs[0].Versions[1].Content != "second" {
		t.Errorf("the edit went to the wrong version: %+v", msgs[0].Versions)
	}
	if _, err := s.SetMessageVersion(id, 2); err == nil {
		t.Error("a version that does not exist was shown")
	}

	// A turn written once has no versions at all.
	plain, _ := s.AddMessage(Message{ChatID: ch.ID, Role: "assistant", Content: "only"})
	msgs, _ = s.Messages(ch.ID)
	if msgs[1].ID != plain || msgs[1].Versions != nil {
		t.Errorf("a single reply carries versions: %+v", msgs[1])
	}
}
