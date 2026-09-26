package serve

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// TestLiveTurnThroughTheServer sends a real message to a real model through the
// real handler, because everything below the HTTP layer can be right while the
// thing a phone actually does is broken.
func TestLiveTurnThroughTheServer(t *testing.T) {
	if testing.Short() {
		t.Skip("live test skipped in -short mode")
	}
	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	installed, err := client.Probe(ctx)
	cancel()
	if err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}
	if len(installed) == 0 {
		t.Skip("Ollama is running but has no models installed")
	}
	model := os.Getenv("ASTRAL_TEST_MODEL")
	if model == "" {
		model = installed[0].Name
	}

	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// A world and a character, so the prompt goes through the parts a real
	// scene does: the setting, the lorebook, the framing.
	wid, err := st.SaveWorld(world.World{
		Name: "Kestrel Bay", Description: "A harbour town under permanent rain.",
		Rules: "Nobody sails east of the Sever.",
	})
	if err != nil {
		t.Fatal(err)
	}
	caID, err := st.SaveCharacter(chars.Character{
		Name: "Vesper Quill", Description: "A cartographer, impatient and precise.",
		WorldID: wid,
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := store.DefaultConfig()
	cfg.Model = model
	cfg.NumCtx = 4096
	cfg.NumPredict = 128
	s := New(st, func() store.Config { return cfg }, func() *ollama.Client { return client })

	code, err := s.OpenPairing()
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "POST", "/api/pair", "", `{"code":"`+code+`","name":"Test phone"}`)
	if w.Code != 200 {
		t.Fatalf("pairing failed: %s", w.Body.String())
	}
	var paired struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &paired)

	w = do(t, s, "POST", "/api/chats", paired.Token, `{"character_id":`+itoa(caID)+`}`)
	if w.Code != 200 {
		t.Fatalf("creating a chat failed: %s", w.Body.String())
	}
	var made struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &made)

	// httptest's recorder is not a streaming client, so this reads the whole
	// event stream once the turn is over. What it proves is the shape: tokens
	// arrived as events, and the turn ended with a saved reply.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/chats/"+itoa(made.ID)+"/send",
		strings.NewReader(`{"text":"\"You're late again.\" *I set the ruined chart on her desk.*"}`))
	req.Header.Set("Authorization", "Bearer "+paired.Token)
	s.routes().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("send = %d: %s", rec.Code, body)
	}
	if strings.Contains(body, "event: error") {
		t.Fatalf("the turn reported an error:\n%s", body)
	}
	if !strings.Contains(body, "event: token") {
		t.Errorf("no tokens were streamed:\n%s", truncate(body, 400))
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("the turn never finished:\n%s", truncate(body, 400))
	}

	// And it is in the library, which is the point: the phone and the window
	// are looking at the same scene.
	msgs, err := st.Messages(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("stored %d messages, want the turn and its reply", len(msgs))
	}
	if msgs[0].Role != ollama.RoleUser || msgs[1].Role != ollama.RoleAssistant {
		t.Errorf("stored roles are %q then %q", msgs[0].Role, msgs[1].Role)
	}
	if strings.TrimSpace(msgs[1].Content) == "" {
		t.Error("the reply was stored empty")
	}
	if strings.Contains(msgs[1].Content, "<think>") {
		t.Errorf("a think tag was stored as the reply:\n%s", truncate(msgs[1].Content, 200))
	}
	t.Logf("reply (%d chars): %s", len(msgs[1].Content), truncate(msgs[1].Content, 300))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
