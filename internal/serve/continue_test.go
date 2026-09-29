package serve

import (
	"encoding/json"
	"strconv"
	"testing"

	"astral/internal/store"
)

// Continue in a New Chat from a phone makes the continuation on the PC and
// says where it is. A chat short enough to carry whole needs no model; a
// design chat is not one that can be continued.
func TestContinueAChatFromAPhone(t *testing.T) {
	s, st := testServer(t)
	token := paired(t, s)
	ch, err := st.NewChat(0, "Night Ferry", "m", store.KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"hello", "hi", "where to?"} {
		role := "user"
		if text == "hi" {
			role = "assistant"
		}
		if _, err := st.AddMessage(store.Message{ChatID: ch.ID, Role: role, Content: text}); err != nil {
			t.Fatal(err)
		}
	}
	rec := do(t, s, "POST", "/api/chats/"+strconv.FormatInt(ch.ID, 10)+"/continue", token, "")
	if rec.Code != 200 {
		t.Fatalf("continue: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == 0 || out.Title != "Night Ferry, Part 2" {
		t.Fatalf("answer %s, %v", rec.Body.String(), err)
	}
	if msgs, _ := st.Messages(out.ID); len(msgs) != 3 {
		t.Errorf("the continuation has %d messages, want the 3 carried", len(msgs))
	}

	design, _ := st.NewChat(0, "Design", "m", store.KindDesigner)
	if rec := do(t, s, "POST", "/api/chats/"+strconv.FormatInt(design.ID, 10)+"/continue", token, ""); rec.Code != 400 {
		t.Errorf("a design chat: %d, want 400", rec.Code)
	}
}
