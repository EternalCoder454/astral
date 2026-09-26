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

// padding is the shape of a record filling itself with things that did not
// happen. A record of absences is unbounded, so it is the one failure that can
// use the whole budget without repeating itself once, which is why the
// deduplication cannot see it.
var padding = []string{
	"did not mention", "did not correct", "did not contradict", "did not move to",
	"was not mentioned", "no mention of", "did not say anything", "did not reply",
}

func paddingStatements(s string) int {
	n := 0
	for _, st := range splitStatements(s) {
		low := strings.ToLower(st.text)
		for _, p := range padding {
			if strings.Contains(low, p) {
				n++
				break
			}
		}
	}
	return n
}

// noAbsences is the rule under test: a closed one, in the form this project has
// measured to work, rather than a request to be less verbose.
const noAbsences = `
Record only what happened. Never record that something did not happen, was not mentioned, was not corrected, or was not contradicted. What did not happen is endless, and a record of it tells the next model nothing.`

// TestMeasureCompaction is the instrument, not a check. It sends one scene to
// one model repeatedly under each arm and reports what comes back.
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
		system string
	}{
		{"as shipped", compactSystem},
		{"no absences", compactSystem + "\n" + noAbsences},
	}
	for _, arm := range arms {
		var rawTotal, dedupedTotal, padTotal int
		runs := 0
		for i := 0; i < *measureRuns; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			opts := ollama.Options{NumCtx: 8192, Temperature: 0.2, NumPredict: recapReplyTokens}
			noThink := false
			reply, _, err := client.Chat(ctx, model, []ollama.Message{
				{Role: ollama.RoleSystem, Content: arm.system},
				{Role: ollama.RoleUser, Content: prompt},
			}, opts, &noThink, nil)
			cancel()
			if err != nil {
				t.Logf("  %s run %d: %v", arm.name, i+1, err)
				continue
			}
			raw := stripPromptEcho(strings.TrimSpace(reply.Content))
			deduped := dedupeRecap(raw)
			pad := paddingStatements(deduped)
			rawTotal, dedupedTotal, padTotal = rawTotal+len(raw), dedupedTotal+len(deduped), padTotal+pad
			runs++
			t.Logf("  %-12s run %d: %5d raw, %5d deduped (%2d%% of input), %2d padding statements",
				arm.name, i+1, len(raw), len(deduped), 100*len(deduped)/in, pad)
		}
		if runs > 0 {
			t.Logf("  %-12s mean over %d: %d raw, %d deduped, %.1f padding",
				arm.name, runs, rawTotal/runs, dedupedTotal/runs, float64(padTotal)/float64(runs))
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
