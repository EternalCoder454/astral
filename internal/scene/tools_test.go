package scene

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"astral/internal/store"
	"astral/internal/websearch"
)

// Every conversation that is not a scene is given its tools, the optimizer one
// more, and a scene none; search goes with the switch and saving does not.
func TestWhoGetsTools(t *testing.T) {
	cfg := store.DefaultConfig()
	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	names := func(r *websearch.Runner) []string {
		var out []string
		for _, e := range r.Extras {
			out = append(out, e.Tool.Function.Name)
		}
		return out
	}
	for _, kind := range []string{store.KindAssistant, store.KindDesigner, store.KindStyleDesigner,
		store.KindWorldDesigner, store.KindPromptOptimizer} {
		r := Runner(nil, cfg, st, kind, "m", Options(cfg), nil)
		if r == nil || r.Provider == nil {
			t.Fatalf("%s cannot search", kind)
		}
		got := strings.Join(names(r), ",")
		want := "save_to_knowledge"
		if kind == store.KindPromptOptimizer {
			want += ",read_prompt"
		}
		if got != want {
			t.Errorf("%s is given %s, want %s", kind, got, want)
		}
	}
	if Runner(nil, cfg, st, store.KindRoleplay, "m", Options(cfg), nil) != nil {
		t.Error("a scene was given tools")
	}
	// With search off there is nothing found to keep, so saving goes too; the
	// optimizer can still read prompts.
	cfg.WebSearch = false
	if r := Runner(nil, cfg, st, store.KindAssistant, "m", Options(cfg), nil); r == nil || r.Provider != nil || len(r.Extras) != 0 {
		t.Error("with search off, a chat should neither search nor save")
	}
	if r := Runner(nil, cfg, st, store.KindPromptOptimizer, "m", Options(cfg), nil); r == nil || len(r.Extras) != 1 {
		t.Error("with search off, the optimizer should still read prompts")
	}
}

// What the model saves lands in the knowledge base, marked as saved from a
// conversation, and a note with nothing in it is refused.
func TestSavingToKnowledge(t *testing.T) {
	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	save := KnowledgeSaver(st)
	args, _ := json.Marshal(map[string]any{"title": "Ollama keep_alive", "content": "0 unloads at once.",
		"source": "https://docs.ollama.com/faq", "tags": []string{"ollama"}})
	if got := save.Answer(context.Background(), args); !strings.Contains(got, "Saved") {
		t.Fatalf("answered %q", got)
	}
	if note := save.Note(args); note != `Saved "Ollama keep_alive" to Knowledge` {
		t.Errorf("note %q", note)
	}
	entries, _ := st.KnowledgeEntries()
	if len(entries) != 1 || entries[0].Origin != store.OriginChat || entries[0].Source != "https://docs.ollama.com/faq" {
		t.Errorf("saved %+v", entries)
	}
	empty, _ := json.Marshal(map[string]string{"title": "Nothing"})
	if got := save.Answer(context.Background(), empty); !strings.Contains(got, "Nothing was saved") {
		t.Errorf("an empty note was saved: %q", got)
	}
}
