package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		Description: "A cartographer, impatient and precise.", WorldID: wid,
		FirstMes: `*She does not look up from the chart.* "You're late, {{user}}."`,
		// A portrait, to see the scene set in front of it.
		PortraitPath: os.Getenv("ASTRAL_DEV_SERVE_PORTRAIT")})
	ch, _ := st.NewChatIn(caID, 0, "The tide came in early", "m", store.KindRoleplay)
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleUser,
		Content: `"You're late again." *I set the ruined chart on her desk.*`})
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant,
		Content: `She did not look up. "The tide was wrong, and so was the wind." *The pen kept moving, marking a line that would not hold by morning.*`})
	st.NewChatIn(0, 0, "A plain question", "m", store.KindAssistant)
	if os.Getenv("ASTRAL_DEV_SERVE_RICH") != "" {
		// Enough to look at a phone's lists and a long scene the way they are
		// after a few weeks of use: a long title, a long reply, a busy cast.
		for i := 0; i < 9; i++ {
			st.NewChatIn(caID, 0, fmt.Sprintf("Scene %d at the harbour", i), "m", store.KindRoleplay)
		}
		long, _ := st.NewChatIn(caID, 0,
			"A very long scene title that keeps going well past the width of any phone screen", "m", store.KindRoleplay)
		for i := 0; i < 8; i++ {
			st.AddMessage(store.Message{ChatID: long.ID, Role: ollama.RoleUser,
				Content: fmt.Sprintf(`*I lean on the rail, watching the tide.* "Turn %d. Tell me what you saw."`, i)})
			st.AddMessage(store.Message{ChatID: long.ID, Role: ollama.RoleAssistant,
				Content: strings.Repeat(`*She folds the chart twice, then a third time, as if the fold could hide the line.* "The water came in wrong, and it came in fast." `, 3)})
		}
		for _, n := range []string{"Maren Voss", "Oswin Tarrow", "Brand Ashcombe", "Ilse of the Lanterns"} {
			st.SaveCharacter(chars.Character{Name: n, Description: "Someone from the harbour with a long description that runs on for a while so the list has to cut it somewhere sensible."})
		}
		st.NewChatIn(0, 0, "Creating a Persona", "", store.KindPersonaDesigner)
		if os.Getenv("ASTRAL_DEV_SERVE_EPIC") != "" {
			// A scene played for weeks, to time opening it on a phone.
			epic, _ := st.NewChatIn(caID, 0, "The epic", "", store.KindRoleplay)
			for i := 0; i < 150; i++ {
				st.AddMessage(store.Message{ChatID: epic.ID, Role: ollama.RoleUser,
					Content: fmt.Sprintf(`*I lean closer, turn %d.* "And then what happened?"`, i)})
				st.AddMessage(store.Message{ChatID: epic.ID, Role: ollama.RoleAssistant,
					Content: strings.Repeat(`*She traces the coastline with one finger, slowly, as if the ink might still be wet.* "The tide took the lower town first, and nobody rang the bell." `, 5)})
			}
		}
		st.SavePersona(chars.Profile{Name: "Wren", Age: "27", Race: "half-elf"})
		st.SavePersona(chars.Profile{Name: "Brand", Age: "41"})
	}

	cfg := store.DefaultConfig()
	cfg.PersonaName = "Wren"
	// The small model, so a message sent from this harness cannot load a large
	// one beside whatever is already on the card.
	cfg.Model = "huihui_ai/qwen3.5-abliterated:4b"
	if m := os.Getenv("ASTRAL_DEV_SERVE_MODEL"); m != "" {
		cfg.Model = m // a real model, chosen by name, for looking at real replies
	}
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
