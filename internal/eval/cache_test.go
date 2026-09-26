package eval

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
)

var cacheTurns = flag.Int("cacheturns", 5, "how many turns to play while watching the prefix cache")

// Ollama reuses the work it did for however much of a prompt is byte-identical
// to the last one, and Astral's message order is built around keeping that
// prefix stable. Measured on a scene at a realistic size, it is worth about
// four times:
//
//	turn 1  5454 tok at 1079 tok/s   (cold)
//	turn 2  5534 tok at 4293 tok/s
//	turn 4  5689 tok at 4432 tok/s
//
// Two things that look like the cache failing and are not. A prompt of around
// fourteen hundred tokens shows no gain at all, because at that size the fixed
// cost of a request is most of the measurement. And a trailing system message
// that changes every turn — which the anchor is — does not destroy the prefix:
// the template does not hoist it to the front, and a changed tail still reads
// at 3187 tokens a second against 927 cold.
//
// This is here because the failure is invisible. Nothing breaks, replies just
// take longer to start. It asserts only that later turns beat the first, which
// is what a prefix broken early in the prompt would stop being true.
func TestPrefixCacheHoldsAcrossTurns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	probe, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	installed, err := client.Probe(probe)
	cancel()
	if err != nil || len(installed) == 0 {
		t.Skip("no Ollama server reachable")
	}
	model := os.Getenv("ASTRAL_TEST_MODEL")
	if model == "" {
		model = installed[0].Name
	}

	c := chars.Character{
		Name:        "Vesper Quill",
		Description: strings.Repeat("A cartographer, impatient and precise. ", 300),
		Scenario:    strings.Repeat("A map room above a harbour, in the rain. ", 200),
	}
	p := chars.Persona{Name: "Christian"}
	opts := ollama.Options{NumCtx: 16384, NumPredict: 60, Temperature: 0.8}

	var history []ollama.Message
	var cold float64
	openers := []string{
		`"You're late again." *I set the chart down.*`,
		`"The ferry was held at Kestrel Bay."`,
		`"Who is paying the harbourmaster?"`,
		`"You said the Guild expelled you."`,
		`"Six years is a long time."`,
		`"I am not asking as a courier."`,
	}
	t.Logf("model %s", model)
	for turn := 0; turn < *cacheTurns; turn++ {
		history = append(history, ollama.Message{
			Role: ollama.RoleUser, Content: openers[turn%len(openers)],
		})
		sc := chars.Scene{
			Persona: p, History: history,
			Budget: chars.Plan(opts.NumCtx, opts.NumPredict, len(chars.BuildSystem(c, p))),
		}
		msgs := chars.BuildMessages(c, sc)

		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		noThink := false
		reply, stats, err := client.Chat(ctx, model, msgs, opts, &noThink, nil)
		cancel()
		if err != nil {
			t.Fatalf("turn %d: %v", turn+1, err)
		}
		history = append(history, ollama.Message{Role: ollama.RoleAssistant, Content: reply.Content})

		rate := stats.PromptTokPerSec
		t.Logf("  turn %d: prompt %5d tok processed at %9.0f tok/s   reply %3d tok",
			turn+1, stats.PromptTokens, rate, stats.Tokens)
		if turn == 0 {
			cold = rate
			continue
		}
		// Deliberately a weak bound. The point is to notice a change that
		// stops the reuse happening at all, not to hold a ratio that depends
		// on the model, the scene and how much of the prompt changed.
		if cold > 0 && rate > 0 && rate < cold {
			t.Errorf("turn %d read its prompt slower than the first turn did "+
				"(%.0f against %.0f tok/s): something early in the prompt is "+
				"changing between turns, and the whole prefix is being re-read",
				turn+1, rate, cold)
		}
	}
	fmt.Fprintln(os.Stderr)
}
