package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"astral/internal/ollama"
)

// scriptedOllama answers /api/chat with one scripted stream per request, in
// order, as NDJSON the way Ollama streams it.
type scriptedOllama struct {
	mu      sync.Mutex
	replies [][]string // each reply is its NDJSON lines
	asked   int
	tools   [][]string // the tool names offered on each request
}

func (s *scriptedOllama) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []ollama.Tool `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		var names []string
		for _, tl := range req.Tools {
			names = append(names, tl.Function.Name)
		}
		s.tools = append(s.tools, names)
		i := s.asked
		s.asked++
		s.mu.Unlock()
		if i >= len(s.replies) {
			t.Errorf("request %d was not scripted", i+1)
			http.Error(w, "unscripted", 500)
			return
		}
		for _, line := range s.replies[i] {
			fmt.Fprintln(w, line)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func chunk(content string) string {
	b, _ := json.Marshal(map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "done": false})
	return string(b)
}

func toolChunk(name, args string) string {
	b, _ := json.Marshal(map[string]any{"message": map[string]any{
		"role": "assistant", "content": "",
		"tool_calls": []map[string]any{{"function": map[string]any{"name": name, "arguments": json.RawMessage(args)}}},
	}, "done": false})
	return string(b)
}

const doneLine = `{"message":{"role":"assistant","content":""},"done":true,"eval_count":3,"eval_duration":1000000}`

type fixedProvider struct{ results []Result }

func (f fixedProvider) Search(ctx context.Context, q string, n int) ([]Result, error) {
	return f.results, nil
}
func (f fixedProvider) Name() string { return "fixed" }

func TestAnAnswerStreamsEvenWhenSearchIsOffered(t *testing.T) {
	fake := &scriptedOllama{replies: [][]string{{chunk("Twelve "), chunk("times eight is 96."), doneLine}}}
	r := &Runner{Client: ollama.NewClient(fake.server(t).URL), Provider: fixedProvider{}, Model: "m"}
	var got []string
	msg, _, rounds, err := r.Run(context.Background(), []ollama.Message{{Role: "user", Content: "12*8"}},
		func(d ollama.Delta) { got = append(got, d.Content) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Errorf("the answer arrived in %d piece(s); it should have streamed", len(got))
	}
	if msg.Content != "Twelve times eight is 96." || len(rounds) != 0 {
		t.Errorf("got %q after %d rounds", msg.Content, len(rounds))
	}
}

func TestAPreambleBeforeASearchIsTakenBack(t *testing.T) {
	fake := &scriptedOllama{replies: [][]string{
		{chunk("Let me check that. "), toolChunk(ToolName, `{"query":"go release"}`), doneLine},
		{chunk("Go 1.26 came out in February."), doneLine},
	}}
	discarded := 0
	var streamed strings.Builder
	r := &Runner{
		Client:   ollama.NewClient(fake.server(t).URL),
		Provider: fixedProvider{results: []Result{{Title: "Go 1.26", URL: "https://go.dev/doc/go1.26", Snippet: "Released February."}}},
		Model:    "m",
		OnDiscard: func() {
			discarded++
			streamed.Reset()
		},
	}
	msg, _, rounds, err := r.Run(context.Background(), []ollama.Message{{Role: "user", Content: "when was go 1.26"}},
		func(d ollama.Delta) { streamed.WriteString(d.Content) })
	if err != nil {
		t.Fatal(err)
	}
	if discarded != 1 {
		t.Errorf("the preamble was discarded %d times, want once", discarded)
	}
	if streamed.String() != "Go 1.26 came out in February." {
		t.Errorf("what was left on screen: %q", streamed.String())
	}
	if msg.Content != "Go 1.26 came out in February." || len(rounds) != 1 {
		t.Errorf("got %q after %d rounds", msg.Content, len(rounds))
	}
}

func TestAToolCallWithNothingStreamedDiscardsNothing(t *testing.T) {
	fake := &scriptedOllama{replies: [][]string{
		{toolChunk(ToolName, `{"query":"x"}`), doneLine},
		{chunk("Answer."), doneLine},
	}}
	discarded := 0
	r := &Runner{Client: ollama.NewClient(fake.server(t).URL), Provider: fixedProvider{}, Model: "m",
		OnDiscard: func() { discarded++ }}
	if _, _, _, err := r.Run(context.Background(), []ollama.Message{{Role: "user", Content: "q"}}, nil); err != nil {
		t.Fatal(err)
	}
	if discarded != 0 {
		t.Errorf("discarded %d times with nothing on screen", discarded)
	}
}

func TestOpenPageIsOfferedOnlyWithAFetcher(t *testing.T) {
	fake := &scriptedOllama{replies: [][]string{{chunk("A."), doneLine}, {chunk("B."), doneLine}}}
	url := fake.server(t).URL
	without := &Runner{Client: ollama.NewClient(url), Provider: fixedProvider{}, Model: "m"}
	without.Run(context.Background(), []ollama.Message{{Role: "user", Content: "q"}}, nil)
	with := &Runner{Client: ollama.NewClient(url), Provider: fixedProvider{}, Model: "m", Fetcher: NewFetcher()}
	with.Run(context.Background(), []ollama.Message{{Role: "user", Content: "q"}}, nil)
	if strings.Join(fake.tools[0], ",") != ToolName {
		t.Errorf("without a fetcher the tools were %v", fake.tools[0])
	}
	if strings.Join(fake.tools[1], ",") != ToolName+","+OpenToolName {
		t.Errorf("with a fetcher the tools were %v", fake.tools[1])
	}
}

func TestOpeningAPageReportsItBack(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Release notes</title></head><body><main><p>` +
			strings.Repeat("Version 2 adds tide tables. ", 12) + `</p></main></body></html>`))
	}))
	defer page.Close()
	fake := &scriptedOllama{replies: [][]string{
		{toolChunk(OpenToolName, `{"url":"`+page.URL+`"}`), doneLine},
		{chunk("Version 2 adds tide tables."), doneLine},
	}}
	f := NewFetcher()
	f.allowPrivate = true
	var seen []Round
	r := &Runner{Client: ollama.NewClient(fake.server(t).URL), Provider: fixedProvider{}, Model: "m",
		Fetcher: f, OnRound: func(rd Round) { seen = append(seen, rd) }}
	_, _, rounds, err := r.Run(context.Background(), []ollama.Message{{Role: "user", Content: "q"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 || rounds[0].Page == nil || rounds[0].Page.Title != "Release notes" {
		t.Fatalf("rounds %+v", rounds)
	}
	if len(seen) != 1 {
		t.Errorf("OnRound was called %d times", len(seen))
	}
	if !strings.Contains(Notes(rounds), "Read \"Release notes\"") {
		t.Errorf("the notes do not say a page was read:\n%s", Notes(rounds))
	}
}

type countingProvider struct {
	name  string
	err   error
	calls int
}

func (c *countingProvider) Search(ctx context.Context, q string, n int) ([]Result, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return []Result{{Title: c.name, URL: "https://" + c.name}}, nil
}
func (c *countingProvider) Name() string { return c.name }

func TestAutoFallsBackAndStopsAskingAServerThatIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	a := NewAuto(srv.URL)
	ddg := &countingProvider{name: "ddg"}
	a.fallback = ddg

	for i := 0; i < 3; i++ {
		got, err := a.Search(context.Background(), "q", 3)
		if err != nil || len(got) != 1 || got[0].Title != "ddg" {
			t.Fatalf("search %d: %v %v", i, got, err)
		}
	}
	if ddg.calls != 3 {
		t.Errorf("the fallback answered %d of 3", ddg.calls)
	}
	if a.Name() != "ddg" {
		t.Errorf("Name says %q after the fallback answered", a.Name())
	}
	if a.searxUp() {
		t.Error("a failing SearXNG is still being tried on every search")
	}
}

func TestAutoWithNoAddressUsesTheFallback(t *testing.T) {
	a := NewAuto("")
	ddg := &countingProvider{name: "ddg"}
	a.fallback = ddg
	if _, err := a.Search(context.Background(), "q", 3); err != nil || ddg.calls != 1 {
		t.Errorf("err %v, calls %d", err, ddg.calls)
	}
}

func TestAutoDoesNotFallBackWhenStopped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	a := NewAuto(srv.URL)
	ddg := &countingProvider{name: "ddg"}
	a.fallback = ddg
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Search(ctx, "q", 3)
	if ddg.calls != 0 {
		t.Error("pressing Stop sent the search to the fallback")
	}
	if !a.searxUp() {
		t.Error("pressing Stop marked SearXNG as down")
	}
}
