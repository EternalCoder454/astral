package store

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
)

// TestMakeBigLibrary writes a library the size a heavy user reaches, for timing
// the app against: set ASTRAL_BIGDB to the database path to make one. Skipped
// otherwise.
func TestMakeBigLibrary(t *testing.T) {
	path := os.Getenv("ASTRAL_BIGDB")
	if path == "" {
		t.Skip("set ASTRAL_BIGDB to make a large library")
	}
	s, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rng := rand.New(rand.NewSource(1))
	words := strings.Fields("the tide rain chart harbour lamp guild ferry door map ink rope salt bell glass knife coat river stone voice hand window letter candle smoke")
	sentence := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(words[rng.Intn(len(words))])
		}
		return b.String()
	}
	var ids []int64
	for i := 0; i < 60; i++ {
		id, err := s.SaveCharacter(chars.Character{Name: fmt.Sprintf("Character %d", i),
			Description: sentence(120), Personality: sentence(20), FirstMes: `*` + sentence(30) + `* "` + sentence(12) + `"`})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	start := time.Now()
	total := 0
	for c := 0; c < 150; c++ {
		ch, err := s.NewChat(ids[c%len(ids)], fmt.Sprintf("Scene %d: %s", c, sentence(4)), "m", KindRoleplay)
		if err != nil {
			t.Fatal(err)
		}
		n := 20 + rng.Intn(300)
		if c == 0 {
			n = 1500 // one scene played for months
		}
		for i := 0; i < n; i++ {
			role, body := "user", `*`+sentence(8)+`* "`+sentence(6)+`"`
			if i%2 == 1 {
				role, body = "assistant", `*`+sentence(40)+`* "`+sentence(25)+`"`+"\n\n*"+sentence(30)+`*`
			}
			if _, err := s.AddMessage(Message{ChatID: ch.ID, Role: role, Content: body}); err != nil {
				t.Fatal(err)
			}
			total++
		}
	}
	info, _ := os.Stat(path)
	t.Logf("%d chats, %d messages in %v, database %d MB", 150, total, time.Since(start).Round(time.Second), info.Size()>>20)
}
