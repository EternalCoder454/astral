package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestSystemFirstJoinsTheOpeningAndHandsTheRestToThePerson(t *testing.T) {
	in := []Message{
		{Role: RoleSystem, Content: "You are Odile."},
		{Role: RoleSystem, Content: "Examples."},
		{Role: RoleAssistant, Content: "Evening."},
		{Role: RoleUser, Content: "Hello."},
		{Role: RoleSystem, Content: "[Keep it short.]"},
	}
	want := []Message{
		{Role: RoleSystem, Content: "You are Odile.\n\nExamples."},
		{Role: RoleAssistant, Content: "Evening."},
		{Role: RoleUser, Content: "Hello.\n\n[Keep it short.]"},
	}
	if got := SystemFirst(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if in[3].Content != "Hello." {
		t.Error("the conversation passed in was changed")
	}
	// After the character, with nothing of the person's to join it to, it is
	// theirs on its own.
	nudge := []Message{
		{Role: RoleSystem, Content: "You are Odile."},
		{Role: RoleAssistant, Content: "Evening."},
		{Role: RoleSystem, Content: "[Speak first.]"},
	}
	if got := SystemFirst(nudge); len(got) != 3 || got[2].Role != RoleUser || got[2].Content != "[Speak first.]" {
		t.Errorf("got %+v", got)
	}
}

// A template that refuses a late system message is asked again with it moved,
// and the model is remembered, so the next request goes right the first time.
func TestChatMovesSystemMessagesForATemplateThatRefusesThem(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		bodies = append(bodies, req.Messages)
		mu.Unlock()
		for i, m := range req.Messages {
			if i > 0 && m.Role == RoleSystem {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":"Jinja Exception: System message must be at the beginning."}`)
				return
			}
		}
		ndjson(w, `{"message":{"role":"assistant","content":"Hi."},"done":true}`)
	}))
	defer srv.Close()
	t.Cleanup(func() { systemFirstModels.Delete(srv.URL + " qwen") })

	c := NewClient(srv.URL)
	msgs := []Message{{Role: RoleSystem, Content: "Card."}, {Role: RoleUser, Content: "Hello."}, {Role: RoleSystem, Content: "[Close.]"}}
	for round := 1; round <= 2; round++ {
		msg, _, err := c.Chat(context.Background(), "qwen", msgs, Options{}, nil, nil)
		if err != nil || msg.Content != "Hi." {
			t.Fatalf("round %d: %q, %v", round, msg.Content, err)
		}
	}
	if len(bodies) != 3 {
		t.Fatalf("%d requests, want the refused one, its retry and the next asked right at once", len(bodies))
	}
	if got := bodies[2]; len(got) != 2 || got[1].Content != "Hello.\n\n[Close.]" {
		t.Errorf("the second round sent %+v", got)
	}
	// Another model on the server is not assumed to be the same.
	c.Chat(context.Background(), "gemma", msgs, Options{}, nil, nil)
	if n := len(bodies[3]); n != 3 {
		t.Errorf("a model that never refused had its messages moved: %d", n)
	}
}
