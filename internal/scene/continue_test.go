package scene

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// A continuation carries the last few messages word for word and a record of
// the rest, written by the model; a chat short enough to carry whole needs no
// model at all.
func TestContinueChatCarriesTheStory(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		resp, _ := json.Marshal(map[string]any{
			"message": map[string]string{"role": "assistant", "content": "Wren came back for the key."},
			"done":    true, "done_reason": "stop",
		})
		fmt.Fprintln(w, string(resp))
	}))
	defer srv.Close()
	client := ollama.NewClient(srv.URL)

	st := memoryStore(t)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper", Description: "A cartographer."})
	ch, _ := st.NewChat(id, "Harbour", "m", store.KindRoleplay)
	for i := 1; i <= 10; i++ {
		role := ollama.RoleUser
		if i%2 == 0 {
			role = ollama.RoleAssistant
		}
		if _, err := st.AddMessage(store.Message{ChatID: ch.ID, Role: role, Content: fmt.Sprintf("line %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	ch, _ = st.Chat(ch.ID)
	next, err := ContinueChat(context.Background(), client, "m", st, store.Config{}, ch)
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 || !strings.Contains(next.Summary, "came back for the key") {
		t.Errorf("%d model calls, record %q", calls, next.Summary)
	}
	shown, _ := st.MessagesAfter(next.ID, next.SummaryUpto)
	if len(shown) != ContinueTail || shown[0].Content != "line 5" {
		t.Errorf("carried %d messages starting %q, want %d from line 5", len(shown), shown[0].Content, ContinueTail)
	}

	calls = 0
	short, _ := st.NewChat(id, "Short", "m", store.KindRoleplay)
	st.AddMessage(store.Message{ChatID: short.ID, Role: ollama.RoleUser, Content: "hello"})
	short, _ = st.Chat(short.ID)
	if _, err := ContinueChat(context.Background(), client, "", st, store.Config{}, short); err != nil || calls != 0 {
		t.Errorf("a short chat: %v, %d model calls", err, calls)
	}

	design, _ := st.NewChat(0, "Design", "m", store.KindDesigner)
	if _, err := ContinueChat(context.Background(), client, "m", st, store.Config{}, design); err == nil {
		t.Error("a design chat was continued")
	}
}
