package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTimeOpenBigLibrary times opening a large library, migrations included:
// set ASTRAL_BIGDB to one made by TestMakeBigLibrary.
func TestTimeOpenBigLibrary(t *testing.T) {
	src := os.Getenv("ASTRAL_BIGDB")
	if src == "" {
		t.Skip("set ASTRAL_BIGDB to time opening a large library")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "astral.db")
	os.WriteFile(path, data, 0o644)
	for i := 0; i < 3; i++ {
		start := time.Now()
		s, _, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		opened := time.Since(start)
		start = time.Now()
		chats, _ := s.Chats()
		t.Logf("open %v, list %d chats %v", opened.Round(time.Millisecond), len(chats), time.Since(start).Round(time.Millisecond))
		s.Close()
	}
}
