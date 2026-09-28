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

// A model that asks to read a prompt is given it, and then answers.
func TestRunReadsPrompts(t *testing.T) {
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

	var read []string
	msg, _, err := Run(context.Background(), ollama.NewClient(srv.URL), "m",
		[]ollama.Message{{Role: ollama.RoleSystem, Content: System("scene.framing")}, {Role: ollama.RoleUser, Content: "Go."}},
		ollama.Options{}, nil, nil, func(name string) { read = append(read, name) }, nil)
	if err != nil || msg.Content != "Here it is." {
		t.Fatalf("got %q %v", msg.Content, err)
	}
	if len(read) != 1 || read[0] != "Format Reminder" {
		t.Errorf("reads reported: %v", read)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "FORMAT.") {
		t.Errorf("the model was not given the prompt: %v", seen)
	}
}

// The last request of a kind can be read whole, and is listed once there is one.
func TestSentRequestsCanBeRead(t *testing.T) {
	prompts.RecordSent("scene", "Scene Request", "[system]\nYou are roleplaying as Vesper.")
	if !strings.Contains(System("scene.framing"), "- sent.scene: the last Scene Request") {
		t.Error("the sent request is not listed")
	}
	if got := Read("sent.scene"); !strings.Contains(got, "You are roleplaying as Vesper.") {
		t.Errorf("read %q", got)
	}
	if got := Read("sent.nothing"); !strings.Contains(got, "Nothing of that kind") {
		t.Errorf("read %q", got)
	}
}
