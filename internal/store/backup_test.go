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

	if filepath.Ext(path) != ".gz" {
		t.Errorf("the copy was not compressed: %s", path)
	}
	unpacked := filepath.Join(dir, "unpacked.db")
	if err := gunzipFile(path, unpacked); err != nil {
		t.Fatal(err)
	}
	copy, _, err := Open(unpacked)
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
	if _, err := os.Stat(filepath.Join(backups, "astral-2026-09-01.db.gz")); err == nil {
		t.Error("the oldest copy was kept past the week")
	}
}

// A backup put back is the library as it was that day, and the library it
// replaced is kept beside it rather than lost.
func TestRestoreBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "astral.db")
	st, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.NewChat(0, "Before the backup", "m", KindRoleplay)
	backups := filepath.Join(dir, "backups")
	if _, err := st.BackupDaily(backups, time.Date(2026, 9, 1, 12, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	st.NewChat(0, "After the backup", "m", KindRoleplay)
	st.Close()

	list := Backups(backups)
	if len(list) != 1 || list[0].Day.Day() != 1 {
		t.Fatalf("backups = %+v", list)
	}
	kept, err := RestoreBackup(path, list[0].Path, time.Date(2026, 9, 2, 8, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	st, _, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	chats, _ := st.Chats()
	st.Close()
	if len(chats) != 1 || chats[0].Title != "Before the backup" {
		t.Errorf("after restoring, the chats are %+v", chats)
	}
	old, _, err := Open(kept)
	if err != nil {
		t.Fatalf("the replaced library was not kept: %v", err)
	}
	defer old.Close()
	if prev, _ := old.Chats(); len(prev) != 2 {
		t.Errorf("the kept library has %d chats, want 2", len(prev))
	}
	bad := filepath.Join(dir, "bad.db")
	os.WriteFile(bad, []byte("not a database at all"), 0o600)
	if _, err := RestoreBackup(path, bad, time.Now()); err == nil {
		t.Error("a file that is not a database was restored")
	}
}

// A copy made before backups were compressed still restores.
func TestRestoreAPlainBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "astral.db")
	st, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.NewChat(0, "In the old copy", "m", KindRoleplay)
	backups := filepath.Join(dir, "backups")
	gz, err := st.BackupDaily(backups, time.Date(2026, 9, 1, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	st.NewChat(0, "Made later", "m", KindRoleplay)
	st.Close()
	plain := filepath.Join(backups, "astral-2026-08-31.db")
	if err := gunzipFile(gz, plain); err != nil {
		t.Fatal(err)
	}
	os.Remove(gz)
	if list := Backups(backups); len(list) != 1 || list[0].Path != plain {
		t.Fatalf("listed %+v", list)
	}
	if _, err := RestoreBackup(path, plain, time.Now()); err != nil {
		t.Fatal(err)
	}
	st, _, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chats, _ := st.Chats()
	if len(chats) != 1 || chats[0].Title != "In the old copy" {
		t.Fatalf("restored %+v", chats)
	}
}
