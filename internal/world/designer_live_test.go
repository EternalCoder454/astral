package world

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// Whether a designer produces a usable world is not a question the prompt can
// answer. The failure that matters is silent: entries whose keys never fire, so
// the lorebook looks full and behaves empty.
func TestLiveWorldDesigner(t *testing.T) {
	if testing.Short() {
		t.Skip("live test skipped in -short mode")
	}
	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	probe, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	models, err := client.Probe(probe)
	if err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}
	if len(models) == 0 {
		t.Skip("Ollama is running but has no models installed")
	}
	model := os.Getenv("ASTRAL_TEST_MODEL")
	if model == "" {
		model = models[0].Name
	}

	// A short interview, of the shape one actually has.
	history := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: DesignerOpening},
		{Role: ollama.RoleUser, Content: "A port city where the tide comes in wrong. Nobody trusts the charts any more."},
		{Role: ollama.RoleAssistant, Content: "Good. Who profits from the confusion, and who is blamed for it?"},
		{Role: ollama.RoleUser, Content: "The harbourmaster profits. The cartographers get blamed. There is one cartographer, Vesper, who is still trying to get it right."},
		{Role: ollama.RoleAssistant, Content: "And what happens to someone who sails on an old chart?"},
		{Role: ollama.RoleUser, Content: "They lose the ship. It has happened twice this year and the second one had the mayor's son on it."},
	}

	ctx, cancel2 := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel2()
	draft, err := BuildFromConversation(ctx, client, model,
		history, ollama.Options{NumCtx: 8192})
	if err != nil {
		t.Fatalf("building the world failed: %v", err)
	}

	t.Logf("world: %q", draft.World.Name)
	t.Logf("description: %s", draft.World.Description)
	t.Logf("rules:\n%s", draft.World.Rules)
	for _, e := range draft.Entries {
		t.Logf("  entry %-24q keys %v", e.Name, e.Keys)
	}

	if draft.World.Name == "" {
		t.Error("the world has no name")
	}
	if len(draft.World.Description) < 20 {
		t.Errorf("the description is %d characters, which is not a setting", len(draft.World.Description))
	}
	if strings.TrimSpace(draft.World.Rules) == "" {
		t.Error("the world has no rules, which is the part sent on every turn")
	}
	if len(draft.Entries) < 2 {
		t.Fatalf("got %d lorebook entries, want at least 2", len(draft.Entries))
	}

	// The test that matters: the keys have to fire on the conversation the world
	// was built from. A key that does not appear in the material it came from
	// will not appear in play either.
	var said strings.Builder
	for _, m := range history {
		said.WriteString(strings.ToLower(m.Content))
		said.WriteByte(' ')
	}
	dead := 0
	for _, e := range draft.Entries {
		fires := false
		for _, k := range e.Keys {
			if strings.Contains(said.String(), strings.ToLower(k)) {
				fires = true
				break
			}
		}
		if !fires {
			dead++
			t.Logf("  no key of %q appears in the conversation: %v", e.Name, e.Keys)
		}
	}
	if dead*2 > len(draft.Entries) {
		t.Errorf("%d of %d entries have no key that appears in the material they came from; "+
			"a lorebook like that looks full and behaves empty", dead, len(draft.Entries))
	}
}
