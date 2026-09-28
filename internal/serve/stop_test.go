package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
	"astral/internal/store"
)

// slowModel is an Ollama whose replies arrive a word at a time, slowly enough
// that a test can act in the middle of one. It stops when its caller goes away,
// as the real one does.
func slowModel(t *testing.T, words int) string {
	t.Helper()
	return slowModelEvery(t, words, 15*time.Millisecond)
}

func slowModelEvery(t *testing.T, words int, every time.Duration) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			fl := w.(http.Flusher)
			for i := 0; i < words; i++ {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(every):
				}
				json.NewEncoder(w).Encode(map[string]any{
					"message": map[string]string{"role": "assistant", "content": fmt.Sprintf("word%d ", i)},
					"done":    false,
				})
				fl.Flush()
			}
			json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"role": "assistant", "content": ""},
				"done":    true, "done_reason": "stop",
			})
		case "/api/tags", "/api/ps":
			w.Write([]byte(`{"models":[]}`))
		case "/api/show":
			w.Write([]byte(`{"capabilities":["completion"]}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func serverOn(t *testing.T, ollamaURL string) *Server {
	t.Helper()
	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := store.DefaultConfig()
	cfg.Model = "m"
	cfg.WebSearch = false
	return New(st, func() store.Config { return cfg },
		func() *ollama.Client { return ollama.NewClient(ollamaURL) },
		func(next store.Config) error { cfg = next; return nil }, "test")
}

// lastReply is the newest assistant turn in a chat, or empty.
func lastReply(t *testing.T, s *Server, chatID int64) string {
	t.Helper()
	msgs, err := s.store.Messages(chatID)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == ollama.RoleAssistant {
			return msgs[i].Content
		}
	}
	return ""
}

// Stop ends the reply and keeps what was written, the way the window does.
func TestStopKeepsWhatWasWritten(t *testing.T) {
	s := serverOn(t, slowModel(t, 400))
	token := paired(t, s)
	ch := seedScene(t, s)

	done := make(chan string, 1)
	go func() {
		done <- do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/send", token, `{"text":"Go on."}`).Body.String()
	}()
	// Let a few words arrive, then stop.
	deadline := time.Now().Add(5 * time.Second)
	for !s.busy.writing(ch.ID) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	stop := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/stop", token, "{}")
	if stop.Code != http.StatusOK || !strings.Contains(stop.Body.String(), "true") {
		t.Fatalf("stop answered %d %s", stop.Code, stop.Body.String())
	}
	var body string
	select {
	case body = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the reply did not stop")
	}
	if !strings.Contains(body, "event: accepted") {
		t.Error("the phone was not told the id of its own turn")
	}
	if !strings.Contains(body, `"stopped":true`) {
		t.Errorf("the phone was not told the reply was stopped:\n%s", body)
	}
	got := lastReply(t, s, ch.ID)
	if !strings.HasPrefix(got, "word0 word1") || strings.Contains(got, "word399") {
		t.Errorf("stored %q, want the part written before the stop", got)
	}
	if s.busy.writing(ch.ID) {
		t.Error("the chat is still marked busy")
	}
	// Nothing to stop now.
	if again := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/stop", token, "{}"); !strings.Contains(again.Body.String(), "false") {
		t.Errorf("a second stop said %s", again.Body.String())
	}
}

// A phone that drops its connection mid-reply (the screen locks, the Wi-Fi
// blinks) used to take the reply with it. The PC now finishes and stores it,
// and says it is still writing until then, so the phone can come back for it.
func TestAReplyOutlivesTheConnection(t *testing.T) {
	s := serverOn(t, slowModel(t, 40))
	token := paired(t, s)
	ch := seedScene(t, s)
	srv := httptest.NewServer(s.routes())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/api/chats/"+itoa(ch.ID)+"/send",
		strings.NewReader(`{"text":"Tell me."}`))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Read until the first word arrives, then hang up.
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "event: token") {
			break
		}
	}
	cancel()
	resp.Body.Close()

	var chat struct {
		Writing bool `json:"writing"`
	}
	get := func() {
		r := do(t, s, "GET", "/api/chats/"+itoa(ch.ID), token, "")
		json.Unmarshal(r.Body.Bytes(), &chat)
	}
	get()
	if !chat.Writing {
		t.Error("the chat did not say it was still writing")
	}
	deadline := time.Now().Add(5 * time.Second)
	for chat.Writing && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		get()
	}
	if chat.Writing {
		t.Fatal("the reply never finished")
	}
	if got := lastReply(t, s, ch.ID); !strings.Contains(got, "word39") {
		t.Errorf("stored %q, want the whole reply", got)
	}
}

// Writing the last reply again keeps the old one as another version, and a
// failed attempt puts it back rather than leaving the scene without it.
func TestRegenerateKeepsTheOldReply(t *testing.T) {
	s := serverOn(t, slowModel(t, 5))
	token := paired(t, s)
	ch := seedScene(t, s)
	do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/regenerate", token, "{}")
	msgs, _ := s.store.Messages(ch.ID)
	last := msgs[len(msgs)-1]
	if len(msgs) != 2 || len(last.Versions) != 2 || last.Versions[0].Content != "A reply." ||
		last.Version != 1 || !strings.HasPrefix(last.Content, "word0") {
		t.Fatalf("after writing it again: %+v", last)
	}

	// Against a model that is not there, the attempt fails, and both versions
	// come back.
	down, _ := testServer(t)
	downToken := paired(t, down)
	ch2 := seedScene(t, down)
	do(t, down, "POST", "/api/chats/"+itoa(ch2.ID)+"/regenerate", downToken, "{}")
	if got := lastReply(t, down, ch2.ID); got != "A reply." {
		t.Errorf("after a failed attempt the last reply is %q", got)
	}
}
