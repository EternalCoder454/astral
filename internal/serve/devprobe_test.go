package serve

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// TestDevServe is a throwaway harness for looking at the phone interface.
func TestDevServe(t *testing.T) {
	if os.Getenv("ASTRAL_DEV_SERVE") == "" {
		t.Skip("set ASTRAL_DEV_SERVE to run the interface")
	}
	dir := t.TempDir()
	st, _, err := store.Open(filepath.Join(dir, "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	wid, _ := st.SaveWorld(world.World{Name: "Kestrel Bay",
		Description: "A harbour town under permanent rain.",
		Rules:       "Nobody sails east of the Sever."})
	caID, _ := st.SaveCharacter(chars.Character{Name: "Vesper Quill",
		Description: "A cartographer, impatient and precise.", WorldID: wid})
	ch, _ := st.NewChatIn(caID, 0, "The tide came in early", "m", store.KindRoleplay)
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleUser,
		Content: `"You're late again." *I set the ruined chart on her desk.*`})
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant,
		Content: `She did not look up. "The tide was wrong, and so was the wind." *The pen kept moving, marking a line that would not hold by morning.*`})

	cfg := store.DefaultConfig()
	cfg.PersonaName = "Christian"
	cfg.Model = "huihui_ai/qwen3.6-abliterated:27b"
	s := New(st, func() store.Config { return cfg },
		func() *ollama.Client { return ollama.NewClient("") },
		func(next store.Config) error { cfg = next; return nil }, "0.3.0")
	if err := s.Start(8799); err != nil {
		t.Fatal(err)
	}
	code, _ := s.OpenPairing()
	t.Logf("serving on http://127.0.0.1:8799  pairing code %s", code)
	time.Sleep(4 * time.Minute)
}
