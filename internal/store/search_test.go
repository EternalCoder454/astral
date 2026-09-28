package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchChats(t *testing.T) {
	s, _, err := Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.NewChatIn(0, 0, "The tide came in early", "m", KindAssistant)
	b, _ := s.NewChatIn(0, 0, "Something else", "m", KindAssistant)
	c, _ := s.NewChatIn(0, 0, "A third", "m", KindAssistant)
	s.AddMessage(Message{ChatID: b.ID, Role: "assistant", Content: "She was expelled from the Guild for a coastline she never saw."})
	s.AddMessage(Message{ChatID: c.ID, Role: "assistant", Content: "The Guild met at dawn."})

	hits, err := s.SearchChats("tide", 10)
	if err != nil || len(hits) != 1 || hits[0].ChatID != a.ID || hits[0].Snippet != "" {
		t.Errorf("title search: %+v %v", hits, err)
	}
	// Every word has to be there, and the end of a word is still being typed.
	hits, _ = s.SearchChats("guild coast", 10)
	if len(hits) != 1 || hits[0].ChatID != b.ID {
		t.Fatalf("message search: %+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "\x01Guild\x02") {
		t.Errorf("the snippet does not mark the match: %q", hits[0].Snippet)
	}
	hits, _ = s.SearchChats("guild", 10)
	if len(hits) != 2 {
		t.Errorf("one hit per chat: %+v", hits)
	}
	// Punctuation and single letters are not a query, and a LIKE wildcard in
	// what was typed is matched literally.
	if hits, _ := s.SearchChats(`" * a`, 10); len(hits) != 0 {
		t.Errorf("nonsense matched: %+v", hits)
	}
	if hits, _ := s.SearchChats("100%", 10); len(hits) != 0 {
		t.Errorf("a percent sign matched everything: %+v", hits)
	}
}
