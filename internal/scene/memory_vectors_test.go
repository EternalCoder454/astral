package scene

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/knowledge"
	"astral/internal/ollama"
	"astral/internal/store"
)

// fakeEmbed is an Ollama that only embeds. The vector of a text is chosen by
// what it is about, so a test decides which moments are near which queries
// without any real model.
type fakeEmbed struct {
	mu      sync.Mutex
	calls   [][]string
	options []map[string]any
	delay   atomic.Int64 // nanoseconds a request takes to answer
}

func (f *fakeEmbed) client(t *testing.T) *ollama.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			json.NewEncoder(w).Encode(map[string]any{"models": []any{}})
		case "/api/embed":
			var b struct {
				Input   []string       `json:"input"`
				Options map[string]any `json:"options"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			f.mu.Lock()
			f.calls = append(f.calls, b.Input)
			f.options = append(f.options, b.Options)
			f.mu.Unlock()
			if d := time.Duration(f.delay.Load()); d > 0 {
				select {
				case <-time.After(d):
				case <-r.Context().Done():
					return
				}
			}
			out := make([][]float32, len(b.Input))
			for i, s := range b.Input {
				out[i] = meaningOf(s)
			}
			json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return ollama.NewClient(srv.URL)
}

func (f *fakeEmbed) embeds() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// meaningOf puts boats near boats and lamps near lamps.
func meaningOf(s string) []float32 {
	switch {
	case strings.Contains(s, "Gannet") || strings.Contains(s, "boat"):
		return []float32{1, 0.1, 0}
	case strings.Contains(s, "lamp"):
		return []float32{0, 1, 0}
	}
	return []float32{0, 0, 1}
}

func noEmbeddingCache(t *testing.T) {
	t.Helper()
	embedModel = knowledge.Model{}
	embedding.Store(nil)
	t.Cleanup(func() {
		embedModel = knowledge.Model{}
		embedding.Store(nil)
	})
}

const gannetMoment = `*She pressed the brass key into your palm.* "The Gannet sails at dawn. Promise me you will be on it, whatever happens tonight."`
const lampMoment = `"The lamp oil is nearly gone," she said, trimming the wick with the care of long habit and no hope.`

// meaningScene is a scene folded past two moments, one of them a promise about
// a ship.
func meaningScene(t *testing.T) (*store.Store, store.Chat) {
	t.Helper()
	st := memoryStore(t)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper"})
	ch, _ := st.NewChat(id, "Scene", "m", store.KindRoleplay)
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: gannetMoment})
	upto, err := st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: lampMoment})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatSummary(ch.ID, "A recap.", upto); err != nil {
		t.Fatal(err)
	}
	ch, _ = st.Chat(ch.ID)
	return st, ch
}

func recalled(kept []store.Moment, text string) bool {
	for _, m := range kept {
		if strings.Contains(m.Content, text) {
			return true
		}
	}
	return false
}

func TestIndexingFillsVectorsInBatchesOldestFirst(t *testing.T) {
	noEmbeddingCache(t)
	st := memoryStore(t)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper"})
	ch, _ := st.NewChat(id, "Scene", "m", store.KindRoleplay)
	for i := 0; i < 40; i++ {
		text := fmt.Sprintf("message %02d, a turn long enough to be worth recalling one day", i)
		if _, err := st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: text}); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeEmbed{}
	client := f.client(t)
	cfg := store.DefaultConfig()
	cfg.EmbeddingModel = "fake-embed"

	for i, want := range []int{32, 8, 0} {
		n, err := IndexMessages(context.Background(), st, client, cfg, ch.ID)
		if err != nil || n != want {
			t.Fatalf("pass %d embedded %d, %v, want %d", i, n, err, want)
		}
	}
	if len(f.calls) != 2 || len(f.calls[0]) != 32 || len(f.calls[1]) != 8 {
		t.Fatalf("requests were not batched: %d", len(f.calls))
	}
	if !strings.HasPrefix(f.calls[0][0], "message 00") || !strings.HasPrefix(f.calls[1][0], "message 32") {
		t.Errorf("not oldest first: %q then %q", f.calls[0][0], f.calls[1][0])
	}
	if gpu, ok := f.options[0]["num_gpu"].(float64); !ok || gpu != 0 {
		t.Errorf("the embedding was not asked to stay off the GPU: %v", f.options[0])
	}
	if left, _ := st.MessagesWithoutVector(ch.ID, "fake-embed", 100); len(left) != 0 {
		t.Errorf("%d messages still lack a vector", len(left))
	}
}

func TestIndexingDoesNothingWithoutAModel(t *testing.T) {
	noEmbeddingCache(t)
	st, ch := meaningScene(t)
	f := &fakeEmbed{}
	client := f.client(t)

	n, err := IndexMessages(context.Background(), st, client, store.DefaultConfig(), ch.ID)
	if n != 0 || err != nil {
		t.Fatalf("embedded %d, %v with no embedding model", n, err)
	}
	// And recall does not so much as ask.
	UseEmbedding(client, store.DefaultConfig())
	hist := []ollama.Message{{Role: ollama.RoleUser, Content: "Which boat departs at sunrise?"}}
	if _, kept := recall(st, ch, hist, 4000); len(kept) != 0 {
		t.Errorf("recalled %d moments from nothing", len(kept))
	}
	if f.embeds() != 0 {
		t.Errorf("the embedding server was asked %d times", f.embeds())
	}
	if has, _ := st.HasMessageVectors(ch.ID); has {
		t.Error("vectors appeared with no model")
	}
}

func TestRecallFindsAMomentByMeaningWhenNoWordsAreShared(t *testing.T) {
	noEmbeddingCache(t)
	st, ch := meaningScene(t)
	f := &fakeEmbed{}
	client := f.client(t)
	cfg := store.DefaultConfig()
	cfg.EmbeddingModel = "fake-embed"
	hist := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: `*The harbour bell rang twice.* "Soon."`},
		{Role: ollama.RoleUser, Content: "Which boat departs at sunrise?"},
	}
	// The point of it: the words alone find nothing.
	if found, _ := st.Moments(ch.ID, ch.SummaryUpto, hist[0].Content+"\n"+hist[1].Content, 6); len(found) != 0 {
		t.Fatalf("the query shares words with the scene: %+v", found)
	}

	UseEmbedding(client, cfg)
	if _, kept := recall(st, ch, hist, 4000); len(kept) != 0 {
		t.Fatalf("recalled %d moments before any had a vector", len(kept))
	}
	if n, err := IndexMessages(context.Background(), st, client, cfg, ch.ID); n != 2 || err != nil {
		t.Fatalf("indexed %d, %v", n, err)
	}
	_, kept := recall(st, ch, hist, 4000)
	if !recalled(kept, "The Gannet sails at dawn") {
		t.Fatalf("the promise was not recalled by meaning: %+v", kept)
	}
	// Nearest first, so a scene with room for one keeps that one.
	_, tight := recall(st, ch, hist, len(gannetMoment)+30)
	if len(tight) != 1 || !recalled(tight, "The Gannet sails at dawn") {
		t.Errorf("the nearest moment did not win the room: %+v", tight)
	}
}

func TestASlowEmbeddingServerFallsBackToWords(t *testing.T) {
	noEmbeddingCache(t)
	st, ch := meaningScene(t)
	f := &fakeEmbed{}
	client := f.client(t)
	cfg := store.DefaultConfig()
	cfg.EmbeddingModel = "fake-embed"
	if n, _ := IndexMessages(context.Background(), st, client, cfg, ch.ID); n != 2 {
		t.Fatalf("indexed %d", n)
	}
	UseEmbedding(client, cfg)

	f.delay.Store(int64(5 * time.Second))
	before := f.embeds()
	hist := []ollama.Message{{Role: ollama.RoleUser, Content: "Where is the brass key?"}}
	start := time.Now()
	_, kept := recall(st, ch, hist, 4000)
	took := time.Since(start)

	if f.embeds() == before {
		t.Fatal("the slow server was never asked")
	}
	if took > 2*time.Second {
		t.Errorf("recall waited %v for a slow embedding server", took)
	}
	if !recalled(kept, "brass key") {
		t.Errorf("the words alone were not used: %+v", kept)
	}
}

func TestAMomentBothSearchesLikedComesFirst(t *testing.T) {
	m := func(id int64) store.Moment { return store.Moment{ID: id} }
	got := fuseMoments([]store.Moment{m(1), m(2), m(3)}, []store.Moment{m(9), m(3)})
	if len(got) != 4 || got[0].ID != 3 {
		t.Fatalf("fused = %+v", got)
	}
	if only := fuseMoments([]store.Moment{m(5), m(4)}); only[0].ID != 5 || only[1].ID != 4 {
		t.Errorf("one list changed order: %+v", only)
	}
}
