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
	st.NewChatIn(0, 0, "A plain question", "m", store.KindAssistant)

	cfg := store.DefaultConfig()
	cfg.PersonaName = "Wren"
	// The small model, so a message sent from this harness cannot load a large
	// one beside whatever is already on the card.
	cfg.Model = "huihui_ai/qwen3.5-abliterated:4b"
	ollamaURL := ""
	if os.Getenv("ASTRAL_DEV_SERVE_FAKE") != "" {
		// No model at all: a fake that writes a long reply a word at a time,
		// slowly enough to press Stop, lock the screen or walk away mid-reply.
		ollamaURL = slowModelEvery(t, 120, 80*time.Millisecond)
		cfg.Model = "fake"
		cfg.WebSearch = false
	}
	s := New(st, func() store.Config { return cfg },
		func() *ollama.Client { return ollama.NewClient(ollamaURL) },
		func(next store.Config) error { cfg = next; return nil }, "0.3.0")
	if err := s.Start(8799); err != nil {
		t.Fatal(err)
	}
	code, _ := s.OpenPairing()
	t.Logf("serving on http://127.0.0.1:8799  pairing code %s", code)
	if f := os.Getenv("ASTRAL_DEV_SERVE_CODE"); f != "" {
		os.WriteFile(f, []byte(code), 0o600)
	}
	time.Sleep(4 * time.Minute)
}
