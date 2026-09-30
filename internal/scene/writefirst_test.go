package scene

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

func TestWhenAChatIsDueToWriteFirst(t *testing.T) {
	now := time.Now()
	cast := []chars.Character{{ID: 1, Name: "Vesper"}}
	ch := store.Chat{ID: 5, Kind: store.KindRoleplay, WriteFirst: 10, CharacterID: 1}
	last := store.Message{ID: 40, ChatID: 5, Role: ollama.RoleAssistant, Content: "Well?", CreatedAt: now.Add(-11 * time.Minute)}

	if !WriteFirstDue(ch, cast, last, now) {
		t.Fatal("a scene whose character spoke last, eleven minutes ago, is not due after ten")
	}
	for name, c := range map[string]struct {
		ch   store.Chat
		cast []chars.Character
		last store.Message
	}{
		"off":            {func() store.Chat { c := ch; c.WriteFirst = 0; return c }(), cast, last},
		"put away":       {func() store.Chat { c := ch; c.Archived = true; return c }(), cast, last},
		"not a scene":    {func() store.Chat { c := ch; c.Kind = store.KindAssistant; return c }(), cast, last},
		"nobody in it":   {ch, nil, last},
		"nothing said":   {ch, cast, store.Message{}},
		"you spoke last": {ch, cast, func() store.Message { m := last; m.Role = ollama.RoleUser; return m }()},
		"already nudged": {func() store.Chat { c := ch; c.NudgedAt = 40; return c }(), cast, last},
		"too soon":       {ch, cast, func() store.Message { m := last; m.CreatedAt = now.Add(-9 * time.Minute); return m }()},
		"no time":        {ch, cast, func() store.Message { m := last; m.CreatedAt = time.Time{}; return m }()},
		"empty":          {ch, cast, func() store.Message { m := last; m.Content = "  "; return m }()},
	} {
		if WriteFirstDue(c.ch, c.cast, c.last, now) {
			t.Errorf("%s: due, and should not be", name)
		}
	}
	// Once you have written, and been answered, it is due again.
	ch.NudgedAt = 40
	next := store.Message{ID: 43, Role: ollama.RoleAssistant, Content: "Fine.", CreatedAt: now.Add(-time.Hour)}
	if !WriteFirstDue(ch, cast, next, now) {
		t.Error("a scene is not due again after a new reply has gone quiet")
	}
}

func TestDueChatsAreFoundInTheStoreOldestFirst(t *testing.T) {
	st := memoryStore(t)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper"})
	var chats []store.Chat
	for _, age := range []time.Duration{2 * time.Hour, 5 * time.Hour, time.Minute} {
		ch, _ := st.NewChat(id, "Scene", "m", store.KindRoleplay)
		st.SetChatWriteFirst(ch.ID, 10)
		st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: "Well?", CreatedAt: time.Now().Add(-age)})
		chats = append(chats, ch)
	}
	due := WriteFirstDueChats(st, time.Now())
	if len(due) != 2 || due[0].ID != chats[1].ID || due[1].ID != chats[0].ID {
		t.Fatalf("due = %v, want the five hour silence then the two hour one", ids(due))
	}
}

func ids(chats []store.Chat) []int64 {
	var out []int64
	for _, c := range chats {
		out = append(out, c.ID)
	}
	return out
}

func TestTheClosingBlockSaysToWriteFirstOnlyOnANudge(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.PersonaName = "Wren"
	ch := store.Chat{Kind: store.KindRoleplay}
	one := []chars.Character{{Name: "Vesper", Description: "A cartographer."}}
	hist := []ollama.Message{{Role: ollama.RoleUser, Content: "Hello."}, {Role: ollama.RoleAssistant, Content: "Hi."}}

	plain := BuildTurn(nil, cfg, ch, one, hist, Turn{})
	nudged := BuildTurn(nil, cfg, ch, one, hist, Turn{Nudge: true})
	a, b := plain[len(plain)-1].Content, nudged[len(nudged)-1].Content
	if strings.Contains(a, "SPEAKING FIRST.") || !strings.Contains(a, "PACE.") {
		t.Errorf("an ordinary turn's closing block:\n%s", a)
	}
	if !strings.Contains(b, "SPEAKING FIRST.") || strings.Contains(b, "PACE.") {
		t.Errorf("a nudge's closing block:\n%s", b)
	}
	if !strings.Contains(b, "Vesper speaks up first") || !strings.Contains(b, "Wren") {
		t.Errorf("the note does not name them:\n%s", b)
	}
	if !strings.Contains(b, "Write nothing for Wren") {
		t.Errorf("the note does not say to write nothing for the person:\n%s", b)
	}
	// Only the closing block changed: the prefix is the cached one.
	for i := range plain[:len(plain)-1] {
		if plain[i].Content != nudged[i].Content {
			t.Fatalf("message %d changed, which throws away the cached prefix", i)
		}
	}

	// A group carries on among itself, whichever way it is asked.
	cast := []chars.Character{{Name: "Vesper"}, {Name: "Ilse"}}
	onward := BuildTurn(nil, cfg, ch, cast, hist, Turn{Onward: true})
	nudge := BuildTurn(nil, cfg, ch, cast, hist, Turn{Nudge: true})
	if got, want := nudge[len(nudge)-1].Content, onward[len(onward)-1].Content; got != want || !strings.Contains(got, "NOBODY IS WAITING ON") {
		t.Errorf("a group's nudge is not Let Them Talk:\n%s", got)
	}
}

// A model that answers every chat with reply, keeping the last prompt.
func answering(t *testing.T, reply string, last *[]ollama.Message, during func()) *ollama.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			var body struct {
				Messages []ollama.Message `json:"messages"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			*last = body.Messages
			if during != nil {
				during()
			}
			json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": reply}, "done": false})
			json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": ""}, "done": true, "done_reason": "stop"})
		case "/api/tags", "/api/ps":
			w.Write([]byte(`{"models":[]}`))
		case "/api/show":
			w.Write([]byte(`{"capabilities":["completion"]}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return ollama.NewClient(srv.URL)
}

func TestWritingFirstStoresTheMessageOnce(t *testing.T) {
	st := memoryStore(t)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper", Description: "A cartographer."})
	ch, _ := st.NewChat(id, "Scene", "m", store.KindRoleplay)
	st.SetChatWriteFirst(ch.ID, 10)
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: "Hello."})
	first, _ := st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: "Hi.",
		CreatedAt: time.Now().Add(-time.Hour)})
	cfg := store.DefaultConfig()
	cfg.Model = "m"

	var sent []ollama.Message
	client := answering(t, `*She looks up from the map.* "Still there?"`, &sent, nil)
	ch, _ = st.Chat(ch.ID)
	wrote, err := WriteFirst(t.Context(), client, st, cfg, ch)
	if err != nil {
		t.Fatal(err)
	}
	if wrote.Who != "Vesper" || len(wrote.Messages) != 1 || !strings.Contains(wrote.Text, "Still there?") {
		t.Fatalf("wrote %+v", wrote)
	}
	if end := sent[len(sent)-1].Content; !strings.Contains(end, "SPEAKING FIRST.") {
		t.Errorf("the model was not told to write first:\n%s", end)
	}
	got, _ := st.Chat(ch.ID)
	last, _, _ := st.LastMessage(ch.ID)
	if last.ID == first || last.Role != ollama.RoleAssistant || got.NudgedAt != last.ID {
		t.Fatalf("stored %+v, nudged at %d", last, got.NudgedAt)
	}
	// One message a silence: not due again until you write.
	if l, _, _ := st.LastMessage(ch.ID); WriteFirstDue(got, castOf(st, got), l, time.Now().Add(24*time.Hour)) {
		t.Error("due again with nothing new from you")
	}
}

func TestAMessageForABrokenSilenceIsNotStored(t *testing.T) {
	st := memoryStore(t)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper"})
	ch, _ := st.NewChat(id, "Scene", "m", store.KindRoleplay)
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: "Hi.", CreatedAt: time.Now().Add(-time.Hour)})
	cfg := store.DefaultConfig()
	cfg.Model = "m"

	var sent []ollama.Message
	// You answer while it is being written.
	client := answering(t, "Hello?", &sent, func() {
		st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: "I am here."})
	})
	_, err := WriteFirst(t.Context(), client, st, cfg, ch)
	if err != ErrMovedOn {
		t.Fatalf("err = %v, want ErrMovedOn", err)
	}
	if last, _, _ := st.LastMessage(ch.ID); last.Role != ollama.RoleUser {
		t.Errorf("a message for a broken silence was stored: %+v", last)
	}
}
