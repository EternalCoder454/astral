package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A day's copy is a working database with the transcripts in it, one is made
// per day, and a week of them is kept.
func TestBackupDaily(t *testing.T) {
	dir := t.TempDir()
	s, _, err := Open(filepath.Join(dir, "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ch, _ := s.NewChatIn(0, 0, "Kept", "m", KindAssistant)
	s.AddMessage(Message{ChatID: ch.ID, Role: "user", Content: "Remember this."})

	backups := filepath.Join(dir, "backups")
	day := time.Date(2026, 9, 1, 12, 0, 0, 0, time.Local)
	path, err := s.BackupDaily(backups, day)
	if err != nil || path == "" {
		t.Fatalf("first backup: %q %v", path, err)
	}
	if again, err := s.BackupDaily(backups, day); err != nil || again != "" {
		t.Errorf("a second copy on the same day: %q %v", again, err)
	}

	copy, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := copy.Messages(ch.ID)
	copy.Close()
	if len(msgs) != 1 || msgs[0].Content != "Remember this." {
		t.Errorf("the copy holds %+v", msgs)
	}

	// Something that is not Astral's is never pruned.
	os.WriteFile(filepath.Join(backups, "notes.txt"), []byte("mine"), 0o644)
	for i := 1; i <= 9; i++ {
		if _, err := s.BackupDaily(backups, day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(backups)
	var kept []string
	for _, e := range entries {
		kept = append(kept, e.Name())
	}
	if len(kept) != keepBackups+1 {
		t.Errorf("kept %v", kept)
	}
	if _, err := os.Stat(filepath.Join(backups, "notes.txt")); err != nil {
		t.Error("a file that was not a backup was removed")
	}
	if _, err := os.Stat(filepath.Join(backups, "astral-2026-09-01.db")); err == nil {
		t.Error("the oldest copy was kept past the week")
	}
}
