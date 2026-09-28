package scene

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// A long file attached to a designer has to reach the model whole: facts from
// its start, middle and end are asked for back. Before the window grew to fit,
// the start of a file this size was dropped without a word.
func TestLiveBigFileReachesTheModel(t *testing.T) {
	client, model := designModel(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = false

	var b strings.Builder
	filler := "The harbour district keeps its own ledgers, its own saints' days and its own grudges, and nobody from uphill is told any of it. "
	b.WriteString("# Kestrel Bay notes\n\nThe lighthouse keeper's name is Oswin Tarrow.\n\n")
	for b.Len() < 60000 {
		b.WriteString(filler)
	}
	b.WriteString("\n\nThe smugglers' password is 'green lantern'.\n\n")
	for b.Len() < 120000 {
		b.WriteString(filler)
	}
	b.WriteString("\n\nThe mayor secretly owns the ferry company.\n")
	file := chars.AttachedFile("kestrel.md", b.String())

	hist := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: chars.DesignerOpening},
		{Role: ollama.RoleUser, Content: "Read my notes before we start.\n\n" + file},
		{Role: ollama.RoleAssistant, Content: "I have read them."},
		{Role: ollama.RoleUser, Content: "Answer in one line each, from the notes: the lighthouse keeper's name, the smugglers' password, and who owns the ferry company."},
	}
	ch := store.Chat{Kind: store.KindDesigner}
	msgs := Build(nil, cfg, ch, chars.Character{}, hist)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	opts := FitContext(ctx, client, model, ch.Kind, OptionsFor(cfg, ch.Kind), msgs)
	t.Logf("%d characters sent, window %d tokens (was %d)", len(file), opts.NumCtx, cfg.NumCtx)
	no := false
	start := time.Now()
	reply, stats, err := client.Chat(ctx, model, msgs, opts, &no, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("answered in %v, %s:\n%s", time.Since(start).Round(time.Second), stats.Summary(), reply.Content)
	low := strings.ToLower(reply.Content)
	for _, want := range []string{"oswin", "green lantern", "mayor"} {
		if !strings.Contains(low, want) {
			t.Errorf("the reply does not have %q, so that part of the file did not arrive", want)
		}
	}
	fmt.Println()
}
