package chars

import (
	"context"
	"flag"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

var measureRuns = flag.Int("compactruns", 3, "how many times to summarise the same scene")

// TestMeasureCompaction is the instrument, not a check. It sends one scene to
// one model repeatedly, with the old sampler setting and the new one, and
// reports how much of what it replaced each record costs.
func TestMeasureCompaction(t *testing.T) {
	client, model := liveModel(t)
	c := Character{Name: "Vesper Quill", Description: "A cartographer."}
	p := Persona{Name: "Wren"}
	aged := compactionFixture()
	prompt := compactPrompt("", aged, c, p)
	in := totalChars(aged)
	t.Logf("model %s, %d chars of transcript, %d runs each", model, in, *measureRuns)

	arms := []struct {
		name   string
		repeat int
	}{
		{"default (64)", 0},
		{"wide (700)", recapReplyTokens},
	}
	for _, arm := range arms {
		var rawTotal, dedupedTotal int
		for i := 0; i < *measureRuns; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			opts := ollama.Options{NumCtx: 8192, Temperature: 0.2, NumPredict: recapReplyTokens}
			opts.RepeatLastN = arm.repeat
			noThink := false
			reply, _, err := client.Chat(ctx, model, []ollama.Message{
				{Role: ollama.RoleSystem, Content: compactSystem},
				{Role: ollama.RoleUser, Content: prompt},
			}, opts, &noThink, nil)
			cancel()
			if err != nil {
				t.Logf("  %s run %d: %v", arm.name, i+1, err)
				continue
			}
			raw := strings.TrimSpace(reply.Content)
			deduped := dedupeRecap(raw)
			rawTotal += len(raw)
			dedupedTotal += len(deduped)
			t.Logf("  %-13s run %d: %5d chars raw (%2d%% of input), %5d deduped, %d statements dropped",
				arm.name, i+1, len(raw), 100*len(raw)/in, len(deduped),
				len(splitStatements(raw))-len(splitStatements(deduped)))
		}
		if *measureRuns > 0 {
			t.Logf("  %-13s mean: %d raw, %d deduped", arm.name,
				rawTotal / *measureRuns, dedupedTotal / *measureRuns)
		}
	}
}

// compactionFixture is the scene TestLiveCompactionKeepsTheFacts uses, padded
// to the size at which compaction actually runs.
func compactionFixture() []ollama.Message {
	aged := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: `*She does not look up.* "You're late, Wren."`},
		{Role: ollama.RoleUser, Content: `"The ferry from Kestrel Bay was held."`},
		{Role: ollama.RoleAssistant, Content: `*Vesper set down the pin.* "Kestrel Bay. That's the third delay this month." *She admitted, quietly, that she had never once left this city.*`},
		{Role: ollama.RoleUser, Content: `"Never? Not once?" *I set the lantern down.* "I'll take you. After the solstice."`},
		{Role: ollama.RoleAssistant, Content: `*She laughed, and it was not a kind sound.* "You promise that to everyone." *But she wrote the date on the corner of the map anyway.*`},
		{Role: ollama.RoleUser, Content: `"I've never promised it to anyone." *I found the chair by the window and sat.*`},
		{Role: ollama.RoleAssistant, Content: `"Then you've been unusually careful." *Vesper turned the map over, hiding the coastline.* "Don't look at that one yet."`},
	}
	filler := `*She moved another pin, measured the gap with her thumb, and wrote a figure in the margin that she immediately crossed out.* "The scale is wrong again." *The lamp guttered; neither of them moved to trim it.*`
	for totalChars(aged) < DefaultBudget().Compact/2 {
		aged = append(aged,
			ollama.Message{Role: ollama.RoleUser, Content: `*I watched her work, and said nothing useful.*`},
			ollama.Message{Role: ollama.RoleAssistant, Content: filler})
	}
	return aged
}
