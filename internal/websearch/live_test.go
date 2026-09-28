package websearch

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/livetest"
	"astral/internal/ollama"
)

// Whether a model searches well is not a question the prompt can answer. The
// failures that matter are all behavioural: searching for arithmetic, phrasing a
// query as a question, answering as though it had found something when it found
// nothing.
//
// ASTRAL_SEARXNG points at an instance; without one these skip.
func liveSearch(t *testing.T) (*ollama.Client, string, Provider) {
	t.Helper()
	if testing.Short() {
		t.Skip("live test skipped in -short mode")
	}
	addr := os.Getenv("ASTRAL_SEARXNG")
	if addr == "" {
		addr = "http://localhost:8080"
	}
	p := NewSearXNG(addr)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := p.Search(ctx, "astral test", 2); err != nil {
		t.Skipf("no SearXNG reachable at %s: %v", addr, err)
	}

	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	pctx, pcancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer pcancel()
	models, err := client.Probe(pctx)
	if err != nil || len(models) == 0 {
		t.Skip("no Ollama server with models")
	}
	model := livetest.Model(t, client, models)
	return client, model, p
}

func runner(client *ollama.Client, model string, p Provider) *Runner {
	no := false
	return &Runner{
		Client: client, Provider: p, Model: model,
		Options: ollama.Options{NumCtx: 8192, Temperature: 0.3},
		Think:   &no, Results: 5,
	}
}

func ask(t *testing.T, r *Runner, question string) (string, []Round) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	msgs := []ollama.Message{
		{Role: ollama.RoleSystem, Content: "You are a helpful assistant.\n\n" + Guidance},
		{Role: ollama.RoleUser, Content: question},
	}
	msg, _, rounds, err := r.Run(ctx, msgs, nil)
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	return msg.Content, rounds
}

// TestLiveSearchesWhenItShould is the whole feature: a question whose answer
// changes, and a model that goes and looks.
func TestLiveSearchesWhenItShould(t *testing.T) {
	client, model, p := liveSearch(t)
	r := runner(client, model, p)
	answer, rounds := ask(t, r, "What is the newest released version of Go? Be specific.")
	for _, rd := range rounds {
		t.Logf("searched %q, %d results, err %v", rd.Query, len(rd.Results), rd.Err)
	}
	t.Logf("answer: %s", answer)

	if len(rounds) == 0 {
		t.Fatal("the model answered a version question from memory without searching")
	}
	// Keywords, not a question. A query phrased as a sentence gets worse results
	// from every engine, and the guidance spends a line on it.
	q := rounds[0].Query
	if strings.HasSuffix(strings.TrimSpace(q), "?") {
		t.Errorf("the query was phrased as a question: %q", q)
	}
	if len(strings.Fields(q)) > 10 {
		t.Errorf("the query is a sentence rather than keywords: %q", q)
	}
}

// TestLiveDoesNotSearchWhenItShouldNot is the other half, and the one that makes
// this a feature rather than a tax on every message.
func TestLiveDoesNotSearchWhenItShouldNot(t *testing.T) {
	client, model, p := liveSearch(t)
	r := runner(client, model, p)
	answer, rounds := ask(t, r, "What is 12 times 8? Answer with just the number.")
	t.Logf("answer: %s, searches: %d", answer, len(rounds))

	if len(rounds) > 0 {
		t.Errorf("the model searched the web for arithmetic: %q", rounds[0].Query)
	}
	if !strings.Contains(answer, "96") {
		t.Errorf("answer does not contain 96: %q", answer)
	}
}

// TestLiveSaysSoWhenSearchFails is the clause worth the tokens. A model handed a
// failure will otherwise answer as though it had found something, and an answer
// that looks sourced and is not is worse than no search at all.
func TestLiveSaysSoWhenSearchFails(t *testing.T) {
	client, model, _ := liveSearch(t)
	// An address that cannot answer, so every search fails.
	r := runner(client, model, NewSearXNG("http://127.0.0.1:9"))
	answer, rounds := ask(t, r, "What is the newest released version of Go?")
	t.Logf("answer: %s", answer)
	if len(rounds) == 0 {
		t.Skip("the model did not try to search, so there is no failure to report")
	}
	for _, rd := range rounds {
		if rd.Err == nil {
			t.Fatalf("the search unexpectedly succeeded: %q", rd.Query)
		}
	}
	low := strings.ToLower(answer)
	admits := false
	for _, phrase := range []string{
		"could not", "couldn't", "unable", "failed", "no access", "not able",
		"cannot check", "can't check", "without access", "search", "verify",
	} {
		if strings.Contains(low, phrase) {
			admits = true
			break
		}
	}
	if !admits {
		t.Errorf("the model did not say it had been unable to check:\n%s", answer)
	}
}
