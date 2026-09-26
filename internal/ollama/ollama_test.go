package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ndjson writes a sequence of chat chunks the way Ollama streams them: one
// JSON object per line, ending with a chunk carrying done and the counters.
func ndjson(w http.ResponseWriter, chunks ...string) {
	for _, c := range chunks {
		fmt.Fprintln(w, c)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func chunk(content string) string {
	b, _ := json.Marshal(map[string]any{
		"message": map[string]string{"role": "assistant", "content": content},
		"done":    false,
	})
	return string(b)
}

func finalChunk(evalCount int, evalNs int64) string {
	b, _ := json.Marshal(map[string]any{
		"message":        map[string]string{"role": "assistant", "content": ""},
		"done":           true,
		"done_reason":    "stop",
		"eval_count":     evalCount,
		"eval_duration":  evalNs,
		"total_duration": evalNs * 2,
	})
	return string(b)
}

func TestChatStreamsAndAssembles(t *testing.T) {
	var gotReq chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&gotReq)
		ndjson(w, chunk("Hello"), chunk(", "), chunk("world"), finalChunk(3, 1e9))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	var deltas []string
	msg, stats, err := c.Chat(context.Background(), "m",
		[]Message{{Role: RoleUser, Content: "hi"}}, Options{}, nil,
		func(d Delta) { deltas = append(deltas, d.Content) })
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if msg.Content != "Hello, world" {
		t.Errorf("Content = %q", msg.Content)
	}
	if strings.Join(deltas, "|") != "Hello|, |world" {
		t.Errorf("deltas = %v, want one per chunk", deltas)
	}
	if stats.Tokens != 3 || stats.TokPerSec != 3 {
		t.Errorf("stats = %+v, want 3 tokens at 3 tok/s", stats)
	}
	if !gotReq.Stream {
		t.Error("stream was not requested")
	}
	if len(gotReq.Messages) != 1 || gotReq.Messages[0].Content != "hi" {
		t.Errorf("messages not sent through: %+v", gotReq.Messages)
	}
}

// Reasoning arrives on its own field and must not be mixed into the reply.
func TestChatSeparatesThinking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		think, _ := json.Marshal(map[string]any{
			"message": map[string]string{"role": "assistant", "thinking": "weighing it up"},
			"done":    false,
		})
		ndjson(w, string(think), chunk("Answer."), finalChunk(2, 1e9))
	}))
	defer srv.Close()

	msg, _, err := NewClient(srv.URL).Chat(context.Background(), "m",
		[]Message{{Role: RoleUser, Content: "hi"}}, Options{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "Answer." {
		t.Errorf("Content = %q, want the reasoning kept out of it", msg.Content)
	}
	if msg.Thinking != "weighing it up" {
		t.Errorf("Thinking = %q", msg.Thinking)
	}
}

// Stopping keeps what already arrived: it is usually most of a paragraph, and
// throwing it away would discard the model's work for the sake of tidiness.
func TestChatCancellationKeepsPartialText(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ndjson(w, chunk("partial "))
		<-release // hold the connection open until the test cancels
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan Message, 1)
	go func() {
		msg, _, _ := NewClient(srv.URL).Chat(ctx, "m",
			[]Message{{Role: RoleUser, Content: "hi"}}, Options{}, nil,
			func(d Delta) { cancel() }) // stop as soon as the first token lands
		got <- msg
	}()

	select {
	case msg := <-got:
		if !strings.Contains(msg.Content, "partial") {
			t.Errorf("cancelled reply lost its text: %q", msg.Content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Chat did not return after cancellation")
	}
}

func TestChatReportsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model 'nope' not found"}`)
	}))
	defer srv.Close()

	_, _, err := NewClient(srv.URL).Chat(context.Background(), "nope",
		[]Message{{Role: RoleUser, Content: "hi"}}, Options{}, nil, nil)
	if err == nil {
		t.Fatal("a 404 was not reported")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error lost the server's explanation: %v", err)
	}
}

// A malformed line in the middle of a stream is survivable: skip it and keep
// reading, because the alternative is losing an otherwise fine reply.
func TestChatSkipsUnparseableLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ndjson(w, chunk("a"), "{not json", "", chunk("b"), finalChunk(2, 1e9))
	}))
	defer srv.Close()

	msg, _, err := NewClient(srv.URL).Chat(context.Background(), "m",
		[]Message{{Role: RoleUser, Content: "hi"}}, Options{}, nil, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if msg.Content != "ab" {
		t.Errorf("Content = %q, want the good chunks kept", msg.Content)
	}
}

func TestChatRejectsEmptyInput(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	if _, _, err := c.Chat(context.Background(), "", []Message{{Role: RoleUser}}, Options{}, nil, nil); err == nil {
		t.Error("an empty model was accepted")
	}
	if _, _, err := c.Chat(context.Background(), "m", nil, Options{}, nil, nil); err == nil {
		t.Error("an empty conversation was accepted")
	}
}

// An unset option must be absent from the JSON, not present as zero: Ollama
// reads temperature 0 as greedy decoding, which is a real and very different
// setting from "use the model's default".
func TestOptionsOmitUnsetValues(t *testing.T) {
	if m := (Options{}).toMap(); m != nil {
		t.Errorf("empty Options produced %v, want nothing sent", m)
	}
	m := Options{Temperature: 0.8, NumCtx: 4096}.toMap()
	if len(m) != 2 || m["temperature"] != 0.8 || m["num_ctx"] != 4096 {
		t.Errorf("toMap = %v", m)
	}
	if _, ok := m["top_p"]; ok {
		t.Error("an unset option was sent anyway")
	}
}

func TestModelsAndHasModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"models":[
			{"name":"qwen3:8b","size":5200000000,"details":{"parameter_size":"8.2B","quantization_level":"Q4_K_M"}},
			{"name":"llama3.2:latest","details":{"parameter_size":"3B"}}
		]}`)
	}))
	defer srv.Close()

	models, err := NewClient(srv.URL).Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models", len(models))
	}
	if got := models[0].Label(); got != "qwen3:8b · 8.2B · Q4_K_M" {
		t.Errorf("Label = %q", got)
	}
	// Ollama appends ":latest" to a bare tag but people rarely type it, so
	// matching has to tolerate it in either direction.
	for _, name := range []string{"llama3.2", "llama3.2:latest", "qwen3:8b"} {
		if !HasModel(models, name) {
			t.Errorf("HasModel(%q) = false", name)
		}
	}
	if HasModel(models, "mistral") || HasModel(models, "") {
		t.Error("HasModel matched something that is not installed")
	}
}

// A reachable server with nothing pulled is not the same failure as no server
// at all, and the UI shows different advice for each.
func TestModelsDistinguishesEmptyFromUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"models":[]}`)
	}))
	defer srv.Close()

	models, err := NewClient(srv.URL).Models(context.Background())
	if err != nil || len(models) != 0 {
		t.Errorf("empty server: models=%v err=%v, want no models and no error", models, err)
	}
	if _, err := NewClient("http://127.0.0.1:1").Models(context.Background()); err == nil {
		t.Error("an unreachable server reported success")
	}
}

func TestStatsSummary(t *testing.T) {
	if got := (Stats{}).Summary(); got != "" {
		t.Errorf("empty stats rendered %q", got)
	}
	got := Stats{Tokens: 363, TokPerSec: 141.73}.Summary()
	if got != "141.7 tok/s · 363 tokens" {
		t.Errorf("Summary = %q", got)
	}
}

// keep_alive is the difference between a fast session and a slow one: without
// it Ollama evicts the model after five minutes, and the next message pays a
// full reload before producing a single token.
func TestChatSendsKeepAlive(t *testing.T) {
	var req map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&req)
		ndjson(w, chunk("hi"), finalChunk(1, 1e9))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if c.KeepAlive != DefaultKeepAlive {
		t.Errorf("new client KeepAlive = %q, want %q", c.KeepAlive, DefaultKeepAlive)
	}
	if _, _, err := c.Chat(context.Background(), "m",
		[]Message{{Role: RoleUser, Content: "hi"}}, Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if req["keep_alive"] != DefaultKeepAlive {
		t.Errorf("keep_alive = %v, want %q", req["keep_alive"], DefaultKeepAlive)
	}

	// An empty setting must send nothing at all rather than an empty string,
	// which Ollama would reject.
	req = nil
	c.KeepAlive = ""
	if _, _, err := c.Chat(context.Background(), "m",
		[]Message{{Role: RoleUser, Content: "hi"}}, Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, present := req["keep_alive"]; present {
		t.Errorf("keep_alive was sent when unset: %v", req["keep_alive"])
	}
}

func TestLoadedSpilled(t *testing.T) {
	tests := []struct {
		name          string
		size, vram    int64
		wantSpilled   bool
		wantOnGPUNear float64
	}{
		{"entirely on the GPU", 3_300_000_000, 3_300_000_000, false, 1.0},
		{"a rounding error off", 3_300_000_000, 3_290_000_000, false, 0.997},
		{"18 percent on the CPU", 4_900_000_000, 4_018_000_000, true, 0.82},
		{"entirely on the CPU", 4_900_000_000, 0, true, 0},
		// A server that reports nothing must not be read as a spill: the
		// warning it would raise is worse than the information it carries.
		{"no sizes reported", 0, 0, true, 0},
	}
	for _, tt := range tests {
		l := Loaded{Name: "m", Size: tt.size, SizeVRAM: tt.vram}
		if got := l.Spilled(); got != tt.wantSpilled {
			t.Errorf("%s: Spilled() = %v, want %v", tt.name, got, tt.wantSpilled)
		}
		if got := l.OnGPU(); got < tt.wantOnGPUNear-0.01 || got > tt.wantOnGPUNear+0.01 {
			t.Errorf("%s: OnGPU() = %.3f, want about %.3f", tt.name, got, tt.wantOnGPUNear)
		}
	}
}

func TestFindLoaded(t *testing.T) {
	loaded := []Loaded{
		{Name: "huihui_ai/qwen3.5-abliterated:4b", Size: 10, SizeVRAM: 10},
		{Name: "llama3.2:latest", Size: 10, SizeVRAM: 10},
	}
	for _, want := range []string{"huihui_ai/qwen3.5-abliterated:4b", "llama3.2", "llama3.2:latest"} {
		if _, ok := FindLoaded(loaded, want); !ok {
			t.Errorf("FindLoaded(%q) did not find it", want)
		}
	}
	if _, ok := FindLoaded(loaded, "qwen3.5-abliterated:4b"); ok {
		t.Error("FindLoaded matched on a partial name, which would confuse two models")
	}
}

// A server that does not answer has to be distinguishable from one that
// answers with a refusal, because only the first is worth telling someone how
// to fix. Matching the message text instead breaks when the wording changes,
// which is the bug this replaced.
func TestUnreachableIsMatchableByIdentity(t *testing.T) {
	// Nothing listening on this port.
	c := NewClient("http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := c.Probe(ctx)
	if err == nil {
		t.Fatal("probing a dead address succeeded")
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("errors.Is(err, ErrUnreachable) is false for %v", err)
	}
	// And the cause survives the wrapping, so a caller can still look deeper.
	if errors.Is(err, context.Canceled) {
		t.Error("a connection failure was reported as a cancellation")
	}
}

// A cancelled request must be recognisable as cancelled however deeply it is
// wrapped on the way back.
func TestCancellationSurvivesWrapping(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Probe(ctx)
	if err == nil {
		t.Fatal("a cancelled probe succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) is false for %v", err)
	}
}
