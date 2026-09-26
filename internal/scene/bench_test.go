package scene

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// Everything Build does happens on the GTK main thread, between pressing send
// and the request going out. If it is slow the window stutters at exactly the
// moment someone is watching it.
func benchStore(tb testing.TB, entries int) (*store.Store, chars.Character, store.Chat) {
	tb.Helper()
	st, _, err := store.Open(filepath.Join(tb.TempDir(), "astral.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { st.Close() })

	wid, err := st.SaveWorld(world.World{
		Name: "Kestrel Bay", Description: "A harbour town under permanent rain.",
		Rules: "Nobody sails east of the Sever.",
	})
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < entries; i++ {
		if _, err := st.SaveLoreEntry(world.Entry{
			WorldID: wid,
			Name:    fmt.Sprintf("Subject %d", i),
			Keys:    []string{fmt.Sprintf("subject%d", i), fmt.Sprintf("thing%d", i)},
			Content: strings.Repeat("A fact that was established about this subject. ", 6),
			Enabled: true,
		}); err != nil {
			tb.Fatal(err)
		}
	}
	caID, err := st.SaveCharacter(chars.Character{
		Name: "Vesper Quill", WorldID: wid,
		Description: strings.Repeat("A cartographer, impatient and precise. ", 20),
	})
	if err != nil {
		tb.Fatal(err)
	}
	ca, err := st.Character(caID)
	if err != nil {
		tb.Fatal(err)
	}
	ch, err := st.NewChatIn(caID, 0, "scene", "m", store.KindRoleplay)
	if err != nil {
		tb.Fatal(err)
	}
	return st, ca, ch
}

func history(turns int) []ollama.Message {
	out := make([]ollama.Message, 0, turns)
	for i := 0; i < turns; i++ {
		role := ollama.RoleUser
		if i%2 == 1 {
			role = ollama.RoleAssistant
		}
		out = append(out, ollama.Message{Role: role,
			Content: fmt.Sprintf("Turn %d. ", i) + strings.Repeat("She moved a pin and said nothing. ", 8)})
	}
	return out
}

func BenchmarkBuild(b *testing.B) {
	for _, entries := range []int{0, 20, 100, 400} {
		b.Run(fmt.Sprintf("lore=%d", entries), func(b *testing.B) {
			st, ca, ch := benchStore(b, entries)
			cfg := store.DefaultConfig()
			cfg.NumCtx = 16384
			hist := history(24)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = Build(st, cfg, ch, ca, hist)
			}
		})
	}
}

// Which half is it: reading the lorebook back out of SQLite every turn, or
// matching it against what was recently said?
func BenchmarkLoreParts(b *testing.B) {
	st, ca, _ := benchStore(b, 400)
	hist := history(24)

	b.Run("read from store", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := st.LoreEntries(ca.WorldID); err != nil {
				b.Fatal(err)
			}
		}
	})

	entries, err := st.LoreEntries(ca.WorldID)
	if err != nil {
		b.Fatal(err)
	}
	turns := make([]string, 0, len(hist)+1)
	turns = append(turns, ca.Description+" "+ca.Scenario)
	for _, m := range hist {
		turns = append(turns, m.Content)
	}
	recent := world.RecentText(turns)
	b.Run("match", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = world.Match(entries, recent, 4000)
		}
	})
}
