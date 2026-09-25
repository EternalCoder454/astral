package world

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// TestLiveLearnRecordsFactsNotEvents is the test this feature turns on.
//
// A model asked to "record what happened" will happily fill a world bible with
// a minute-by-minute account of one conversation, which is useless later and
// expensive forever. What is wanted is the opposite: the handful of things
// that became permanently true. Only a real model shows which one you get.
func TestLiveLearnRecordsFactsNotEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("live test skipped in -short mode")
	}
	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancelProbe()
	models, err := client.Probe(probeCtx)
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

	w := World{ID: 1, Name: "The Drowned Coast", Description: "Maps here go out of date."}

	// A scene carrying durable facts (a place, a rule, a person's trade) mixed
	// with things that are purely of the moment (where someone is standing).
	turns := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: `*Vesper set down the pin.* "Kestrel Bay is three days north, and the ferries there have never once run on time."`},
		{Role: ollama.RoleUser, Content: `"Is that why nobody maps it?"` + " *I sat down by the window.*"},
		{Role: ollama.RoleAssistant, Content: `*She laughed.* "Nobody maps it because the Cartographers' Guild forbids charting anything east of the Sever. Has done for sixty years." *She rubbed at her wrist, where the guild mark had been struck through.* "I was expelled for it."`},
		{Role: ollama.RoleUser, Content: `"Expelled." *I picked up the lantern and put it down again.* "For a coastline."`},
		{Role: ollama.RoleAssistant, Content: `"For a coastline." *Vesper turned the map face down.* "Ask me again when the tide is out."`},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	entries, err := Learn(ctx, client, model, w, nil, turns, "Vesper", "Wren", ollama.Options{NumCtx: 8192})
	if err != nil {
		if ctx.Err() != nil {
			t.Skipf("model did not finish in time: %v", err)
		}
		t.Fatalf("Learn: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the model learned nothing from a scene that established several things")
	}

	var all strings.Builder
	for _, e := range entries {
		t.Logf("  %-28s conf %.2f  keys %v", e.Name, e.Confidence, e.Keys)
		t.Logf("      %s", e.Content)
		all.WriteString(strings.ToLower(e.Name + " " + e.Content + " "))
	}
	blob := all.String()

	// The durable facts should be in there somewhere.
	for _, want := range []string{"kestrel bay", "guild"} {
		if !strings.Contains(blob, want) {
			t.Errorf("nothing was recorded about %q:\n%s", want, blob)
		}
	}

	for _, e := range entries {
		if len(e.Keys) == 0 {
			t.Errorf("%q has no triggers, so nothing could ever recall it", e.Name)
		}
		for _, k := range e.Keys {
			if len(k) < minKeyLen {
				t.Errorf("%q has trigger %q, short enough to match everything", e.Name, k)
			}
			if commonKeys[strings.ToLower(k)] {
				t.Errorf("%q has trigger %q, a word that appears in every scene", e.Name, k)
			}
		}
		if e.Confidence <= 0 || e.Confidence > 1 {
			t.Errorf("%q has confidence %v, outside 0..1", e.Name, e.Confidence)
		}
		if len(e.Content) > 600 {
			t.Errorf("%q is %d chars, far past the sixty words asked for", e.Name, len(e.Content))
		}
		// Events belong to the scene, not the world. A lorebook entry about
		// someone putting a lantern down is the failure this guards.
		if strings.Contains(strings.ToLower(e.Content), "lantern") {
			t.Errorf("%q recorded a moment rather than a fact:\n%s", e.Name, e.Content)
		}
	}

	// And what it learned has to be recallable by what a later scene would say.
	hits := Match(entries, `"How far is Kestrel Bay from here?"`, BudgetChars)
	if len(hits) == 0 {
		t.Error("nothing matched a question naming a subject it had just learned")
	}
}
