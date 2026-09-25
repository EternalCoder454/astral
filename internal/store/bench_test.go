package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"astral/internal/ollama"
)

func benchStore(b *testing.B) *Store {
	b.Helper()
	dir := b.TempDir()
	b.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	s, _, err := Open(filepath.Join(dir, "astral.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	return s
}

// BenchmarkAddMessage is the one that matters for responsiveness: it runs on
// the GTK main thread every time a message is sent or a reply lands, and the
// database is opened with synchronous=FULL, so every call is an fsync.
func BenchmarkAddMessage(b *testing.B) {
	s := benchStore(b)
	ch, err := s.NewChat(0, "bench", "m", KindRoleplay)
	if err != nil {
		b.Fatal(err)
	}
	msg := Message{ChatID: ch.ID, Role: ollama.RoleAssistant,
		Content: "A paragraph of roleplay prose, about the length of a real reply. " +
			"It runs to a couple of hundred characters so the write is representative."}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.AddMessage(msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChats measures the sidebar refresh, which runs twice per turn.
func BenchmarkChats(b *testing.B) {
	for _, n := range []int{10, 100} {
		b.Run(fmt.Sprintf("chats=%d", n), func(b *testing.B) {
			s := benchStore(b)
			for i := 0; i < n; i++ {
				ch, _ := s.NewChat(0, fmt.Sprintf("chat %d", i), "m", KindRoleplay)
				for j := 0; j < 10; j++ {
					s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: "hi"})
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Chats(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMessages measures opening a chat.
func BenchmarkMessages(b *testing.B) {
	s := benchStore(b)
	ch, _ := s.NewChat(0, "bench", "m", KindRoleplay)
	for i := 0; i < 200; i++ {
		s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: "a reply of some length, as they are"})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Messages(ch.ID); err != nil {
			b.Fatal(err)
		}
	}
}
