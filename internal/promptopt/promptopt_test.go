package promptopt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"astral/internal/ollama"
	"astral/internal/prompts"
	"astral/internal/websearch"

	// The packages whose prompts the optimizer lists, so the list is the real one.
	_ "astral/internal/chars"
	_ "astral/internal/knowledge"
	_ "astral/internal/websearch"
	_ "astral/internal/world"
)

// The optimizer sees the prompt it is working on in full, knows what a rewrite
// has to keep, and has every other prompt listed by id.
func TestSystemCarriesThePromptAndTheList(t *testing.T) {
	sys := System("scene.framing")
	for _, want := range []string{
		"THE PROMPT YOU ARE WORKING ON", "Scene Framing (scene.framing)",
		"You are roleplaying as {{char}}", "What a rewrite must keep:", "{{char}} becomes",
		"- designer.character: Character Designer", "- chat.search:", "- optimizer.system:",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("the system prompt is missing %q", want)
		}
	}
	// A rewrite is what it works on from then on, and it knows the original
	// is still there.
	prompts.SetOverrides(map[string]string{"scene.framing": "My own framing."})
	defer prompts.SetOverrides(nil)
	sys = System("scene.framing")
	if !strings.Contains(sys, "<<<PROMPT\nMy own framing.\nPROMPT>>>") || !strings.Contains(sys, "scene.framing#original") {
		t.Errorf("the rewrite is not what it works on:\n%s", sys[len(sys)-400:])
	}
	if !strings.Contains(Read("scene.framing#original"), "You are roleplaying as {{char}}") {
		t.Error("the original cannot be read")
	}
	if !strings.Contains(Read("scene.framing"), "My own framing.") {
		t.Error("read_prompt does not return what is sent")
	}
	if !strings.Contains(Read("no.such"), "scene.framing") {
		t.Error("an unknown id is not answered with the list")
	}
	if !strings.Contains(System(""), "One they are bringing you") {
		t.Error("a brought prompt is not explained")
	}
}

func TestProposal(t *testing.T) {
	reply := "The main problem is length.\n\n```prompt\nYou are a cartographer.\nAnswer briefly.\n```\n\n- Shorter."
	got, ok := Proposal(reply)
	if !ok || got != "You are a cartographer.\nAnswer briefly." {
		t.Errorf("got %q %v", got, ok)
	}
	// The last one wins, and a block marked prompt beats a later unmarked one.
	two := "```prompt\nfirst\n```\nthen\n```prompt\nsecond\n```\n```go\nx := 1\n```"
	if got, _ := Proposal(two); got != "second" {
		t.Errorf("got %q", got)
	}
	// A prompt that talks about fences mid-line is not cut short.
	self := "```prompt\nPut it in a block that opens with ```prompt on its own line.\nMore.\n```"
	if got, _ := Proposal(self); !strings.HasSuffix(got, "More.") {
		t.Errorf("cut short: %q", got)
	}
	if _, ok := Proposal("No block at all."); ok {
		t.Error("found a proposal in plain text")
	}
}

// A model that asks to read a prompt is given it through the conversation's
// tool loop, and then answers.
func TestReadingAPromptThroughTheToolLoop(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ollama.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls++
		n := calls
		for _, m := range body.Messages {
			if m.Role == ollama.RoleTool {
				seen = append(seen, m.Content)
			}
		}
		mu.Unlock()
		if n == 1 {
			w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"read_prompt","arguments":{"id":"scene.format"}}}]},"done":true}` + "\n"))
			return
		}
		w.Write([]byte(`{"message":{"role":"assistant","content":"Here it is."},"done":true}` + "\n"))
	}))
	defer srv.Close()

	var notes []string
	r := &websearch.Runner{
		Client: ollama.NewClient(srv.URL), Model: "m",
		Extras:  []websearch.Extra{ReadExtra()},
		OnRound: func(round websearch.Round) { notes = append(notes, round.Note) },
	}
	msg, _, rounds, err := r.Run(context.Background(),
		[]ollama.Message{{Role: ollama.RoleSystem, Content: System("scene.framing")}, {Role: ollama.RoleUser, Content: "Go."}}, nil)
	if err != nil || msg.Content != "Here it is." {
		t.Fatalf("got %q %v", msg.Content, err)
	}
	if len(notes) != 1 || notes[0] != "Read the Format Reminder prompt" || len(rounds) != 1 {
		t.Errorf("notes %v, rounds %d", notes, len(rounds))
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "FORMAT.") {
		t.Errorf("the model was not given the prompt: %v", seen)
	}
	if !strings.Contains(websearch.Notes(rounds), "Read the Format Reminder prompt") {
		t.Errorf("the fold does not say what was read: %q", websearch.Notes(rounds))
	}
}

// What was wrong with the Scene rewrites the optimizer produced on a small
// model is caught before saving: markers copied in, a slot that is not a slot,
// a stage direction, dashes, and a dropped name.
func TestProblemsCatchWhatWentWrong(t *testing.T) {
	orig := "Write {{char}}'s words. Never narrate {{user}}. %[1]s: *...* \"...\"\n\n%[2]s: \"...\""
	bad := "<<<PROMPT\nWrite {{char}}'s words\u2014always. %[1]s: *...* %[speaker]: \"...\"\n(blank line)\n%[2]s: \"...\"\nPROMPT>>>"
	got := strings.Join(Problems(orig, bad), "\n")
	for _, want := range []string{"{{user}}", "%[speaker]", "dashes", "marker"} {
		if !strings.Contains(got, want) {
			t.Errorf("did not flag %s:\n%s", want, got)
		}
	}
	if p := Problems(orig, orig); len(p) != 0 {
		t.Errorf("the original itself has problems: %v", p)
	}
	// And the markers never reach a saved prompt at all.
	if got, _ := Proposal("```prompt\n<<<PROMPT\nFORMAT.\nPROMPT>>>\n```"); got != "FORMAT." {
		t.Errorf("markers kept: %q", got)
	}
}

// One prompt is rewritten in one request, with no one there to answer, and
// the result says whether it changed and what is wrong with it.
func TestRewriteOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ollama.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if !strings.Contains(body.Messages[len(body.Messages)-1].Content, "nobody here to answer") {
			t.Error("the request did not say it was on its own")
		}
		reply := "It repeats itself.\n\n```prompt\nNever mention that you are an AI\u2014ever.\n```\n\n- Shorter."
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": reply}, "done": true})
	}))
	defer srv.Close()
	got := RewriteOne(context.Background(), ollama.NewClient(srv.URL), "m", ollama.Options{}, "scene.close")
	if got.Err != nil || got.After != "Never mention that you are an AI\u2014ever." || got.Unchanged {
		t.Fatalf("got %+v", got)
	}
	if len(got.Problems) != 1 || !strings.Contains(got.Problems[0], "dashes") {
		t.Errorf("problems %v", got.Problems)
	}
	if got.Summary() != "It repeats itself." {
		t.Errorf("summary %q", got.Summary())
	}
}

// A rewrite that tidies away a measured phrase, or adds a heading, is flagged.
func TestProtectedPhrases(t *testing.T) {
	p, _ := prompts.Get("scene.framing")
	tidy := strings.Replace(p.Default, "Never write, decide, or narrate {{user}}'s words, thoughts, or actions",
		"Never narrate {{user}}'s thoughts", 1)
	got := strings.Join(ProblemsFor(p, "# RULES\n"+tidy), "\n")
	if !strings.Contains(got, "words, thoughts, or actions") || !strings.Contains(got, "headings") {
		t.Errorf("not flagged:\n%s", got)
	}
	if extra := ProblemsFor(p, p.Default); len(extra) != 0 {
		t.Errorf("the original is flagged: %v", extra)
	}
	if !strings.Contains(System("scene.framing"), "must appear in the rewrite exactly as written") {
		t.Error("the optimizer is not told the protected phrases")
	}
}
