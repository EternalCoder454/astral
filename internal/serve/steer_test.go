package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// listeningModel is an Ollama that answers every chat with reply, and keeps
// the last request it was sent so a test can read the prompt.
func listeningModel(t *testing.T, reply string) (url string, last func() []ollama.Message) {
	t.Helper()
	var mu sync.Mutex
	var got []ollama.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			var body struct {
				Messages []ollama.Message `json:"messages"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			got = body.Messages
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"role": "assistant", "content": reply}, "done": false,
			})
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
	return srv.URL, func() []ollama.Message {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

func lastSystem(msgs []ollama.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].Content
}

// Write for Me drafts your turn in your voice and stores nothing.
func TestDraftFromThePhone(t *testing.T) {
	url, last := listeningModel(t, `User: "Show me the map."`)
	s := serverOn(t, url)
	token := paired(t, s)
	ch := seedScene(t, s)
	before := messageIDs(t, s, ch.ID)

	got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/draft", token, `{"idea":"ask about the map"}`)
	if got.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), `"text":"\"Show me the map.\""`) {
		t.Errorf("the draft did not come back tidied:\n%s", got.Body.String())
	}
	if a := lastSystem(last()); !strings.Contains(a, "you are not writing Vesper") || !strings.Contains(a, "ask about the map") {
		t.Errorf("the draft's closing block:\n%s", a)
	}
	if now := messageIDs(t, s, ch.ID); len(now) != len(before) {
		t.Errorf("a draft stored a message: %v then %v", before, now)
	}

	// Not in a chat with nobody to write to.
	plain, _ := s.store.NewChat(0, "Q", "m", store.KindAssistant)
	if got := do(t, s, "POST", "/api/chats/"+itoa(plain.ID)+"/draft", token, `{}`); got.Code != http.StatusBadRequest {
		t.Errorf("a draft in a general chat answered %d", got.Code)
	}
}

// A rewrite's note reaches the end of the prompt.
func TestRewriteWithANoteFromThePhone(t *testing.T) {
	url, last := listeningModel(t, `*She laughs.* "No."`)
	s := serverOn(t, url)
	token := paired(t, s)
	ch := seedScene(t, s)
	got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/regenerate", token, `{"note":"she refuses"}`)
	if got.Code != http.StatusOK {
		t.Fatalf("answered %d", got.Code)
	}
	if a := lastSystem(last()); !strings.Contains(a, "THIS REPLY.") || !strings.HasSuffix(a, "she refuses]") {
		t.Errorf("the note is not at the end:\n%s", a)
	}
	// A plain rewrite still works with no body at all.
	if got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/regenerate", token, ""); got.Code != http.StatusOK {
		t.Fatalf("a rewrite with no body answered %d", got.Code)
	}
	if a := lastSystem(last()); strings.Contains(a, "THIS REPLY.") {
		t.Error("a plain rewrite kept the last note")
	}
}

// A group can carry on without you, and a two-hander cannot.
func TestLetThemTalkFromThePhone(t *testing.T) {
	url, last := listeningModel(t, "Ilse: *She shrugs.* \"Fine.\"")
	s := serverOn(t, url)
	token := paired(t, s)
	ch := seedScene(t, s)
	if got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/send", token, `{"text":"","onward":true}`); got.Code != http.StatusBadRequest {
		t.Errorf("carrying on in a two-hander answered %d", got.Code)
	}
	var ids []int64
	for _, n := range []string{"Vesper", "Ilse", "Maro"} {
		id, _ := s.store.SaveCharacter(chars.Character{Name: n})
		ids = append(ids, id)
	}
	g, _ := s.store.NewChatIn(ids[0], 0, "Group", "m", store.KindRoleplay)
	s.store.SetCast(g.ID, ids)
	s.store.AddMessage(store.Message{ChatID: g.ID, Role: ollama.RoleUser, Content: "Vesper, sit."})
	s.store.AddMessage(store.Message{ChatID: g.ID, Role: ollama.RoleAssistant, Content: "*She sits.*", CharacterID: ids[0]})

	got := do(t, s, "POST", "/api/chats/"+itoa(g.ID)+"/send", token, `{"text":"","onward":true}`)
	if got.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", got.Code, got.Body.String())
	}
	if a := lastSystem(last()); !strings.Contains(a, "NOBODY IS WAITING ON") {
		t.Errorf("carrying on:\n%s", a)
	}
	msgs, _ := s.store.Messages(g.ID)
	if msgs[len(msgs)-1].Role != ollama.RoleAssistant || msgs[len(msgs)-2].Role != ollama.RoleAssistant {
		t.Error("carrying on stored a turn of yours")
	}

	got = do(t, s, "POST", "/api/chats/"+itoa(g.ID)+"/send", token, `{"text":"And you?","speaker":"Maro"}`)
	if got.Code != http.StatusOK {
		t.Fatalf("answered %d", got.Code)
	}
	if a := lastSystem(last()); !strings.Contains(a, "THIS TURN: Maro answers") {
		t.Errorf("the chosen speaker:\n%s", a)
	}
}

// Pins, the scene's memory and branches, from the phone.
func TestPinsMemoryAndBranchesFromThePhone(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)
	ch := seedScene(t, s)
	other := seedScene(t, s)
	ids := messageIDs(t, s, ch.ID)

	// Pinning a message through another chat does nothing.
	if got := do(t, s, "POST", "/api/chats/"+itoa(other.ID)+"/messages/"+itoa(ids[1])+"/pin", token, `{"pinned":true}`); got.Code != http.StatusNotFound {
		t.Errorf("pinning across chats answered %d", got.Code)
	}
	if got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/messages/"+itoa(ids[1])+"/pin", token, `{"pinned":true}`); got.Code != http.StatusOK {
		t.Fatalf("pinning answered %d", got.Code)
	}
	chat := do(t, s, "GET", "/api/chats/"+itoa(ch.ID), token, "")
	if !strings.Contains(chat.Body.String(), `"pinned":true`) || !strings.Contains(chat.Body.String(), `"remembers":true`) ||
		!strings.Contains(chat.Body.String(), `"can_draft":true`) {
		t.Errorf("the chat does not say what it offers:\n%s", chat.Body.String())
	}

	mem := do(t, s, "GET", "/api/chats/"+itoa(ch.ID)+"/memory", token, "")
	var m struct {
		Covers bool
		Pins   []pinOut
	}
	json.Unmarshal(mem.Body.Bytes(), &m)
	if m.Covers || len(m.Pins) != 1 || m.Pins[0].Who != "Vesper" {
		t.Errorf("memory: %s", mem.Body.String())
	}
	// No record yet, so nothing to correct.
	if got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/memory", token, `{"recap":"x"}`); got.Code != http.StatusBadRequest {
		t.Errorf("saving a record that does not exist answered %d", got.Code)
	}
	s.store.SetChatSummary(ch.ID, "They met.", ids[0])
	if got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/memory", token, `{"recap":"They met at the docks."}`); got.Code != http.StatusOK {
		t.Fatalf("saving the record answered %d", got.Code)
	}
	if c, _ := s.store.Chat(ch.ID); c.Summary != "They met at the docks." || c.SummaryUpto != ids[0] {
		t.Errorf("record %q up to %d", c.Summary, c.SummaryUpto)
	}

	got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/branch", token, `{"message_id":`+itoa(ids[0])+`}`)
	if got.Code != http.StatusOK {
		t.Fatalf("branching answered %d: %s", got.Code, got.Body.String())
	}
	var b struct {
		ID    int64
		Title string
	}
	json.Unmarshal(got.Body.Bytes(), &b)
	if b.Title != "A scene (Branch)" || len(messageIDs(t, s, b.ID)) != 1 {
		t.Errorf("branch %+v with %d messages", b, len(messageIDs(t, s, b.ID)))
	}
}

// A message you sent comes back better, stored, and the phone is told whether
// it changed.
func TestRewriteMineFromThePhone(t *testing.T) {
	url, last := listeningModel(t, `*I lean on the bar.* "Hello, Vesper."`)
	s := serverOn(t, url)
	token := paired(t, s)
	ch := seedScene(t, s)
	ids := messageIDs(t, s, ch.ID)
	got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/messages/"+itoa(ids[0])+"/rewrite", token, "{}")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"changed":true`) {
		t.Fatalf("answered %d: %s", got.Code, got.Body.String())
	}
	if a := lastSystem(last()); !strings.Contains(a, "make it better") || !strings.Contains(a, "Hello.") {
		t.Errorf("the rewrite's closing block:\n%s", a)
	}
	msgs, _ := s.store.Messages(ch.ID)
	if msgs[0].Content != `*I lean on the bar.* "Hello, Vesper."` {
		t.Errorf("the rewrite was not stored: %q", msgs[0].Content)
	}
	// The reply is not one of yours.
	if got := do(t, s, "POST", "/api/chats/"+itoa(ch.ID)+"/messages/"+itoa(ids[1])+"/rewrite", token, "{}"); got.Code != http.StatusNotFound {
		t.Errorf("rewriting the character's reply answered %d", got.Code)
	}
}

// Writes First is set from the phone, and only to something sensible.
func TestWritesFirstFromThePhone(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)
	ch := seedScene(t, s)
	path := "/api/chats/" + itoa(ch.ID) + "/write-first"

	if got := do(t, s, "POST", path, token, `{"minutes":60}`); got.Code != http.StatusOK {
		t.Fatalf("setting it answered %d: %s", got.Code, got.Body.String())
	}
	if got, _ := s.store.Chat(ch.ID); got.WriteFirst != 60 {
		t.Fatalf("the chat writes first after %d minutes, want 60", got.WriteFirst)
	}
	w := do(t, s, "GET", "/api/chats/"+itoa(ch.ID), token, "")
	var out struct {
		WriteFirst int `json:"write_first"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.WriteFirst != 60 {
		t.Fatalf("the chat's JSON says %d (%v): %s", out.WriteFirst, err, w.Body.String())
	}

	for _, bad := range []string{`{"minutes":-5}`, `{"minutes":99999999}`, `{"minutes":"soon"}`, `nonsense`} {
		if got := do(t, s, "POST", path, token, bad); got.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400", bad, got.Code)
		}
	}
	if got, _ := s.store.Chat(ch.ID); got.WriteFirst != 60 {
		t.Errorf("a refused value changed it to %d", got.WriteFirst)
	}
	if got := do(t, s, "POST", "/api/chats/99999/write-first", token, `{"minutes":10}`); got.Code != http.StatusNotFound {
		t.Errorf("a chat that is not there answered %d, want 404", got.Code)
	}
	if got := do(t, s, "POST", path, token, `{"minutes":0}`); got.Code != http.StatusOK {
		t.Errorf("turning it off answered %d", got.Code)
	}
}
