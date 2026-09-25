package eval

// What a scene model costs, measured the way it is actually felt: the wait
// before the first word, and the speed the words then arrive at.
//
// Both matter and they are not the same number. Generation speed is what a
// model card talks about; prompt-processing speed is what you wait through
// when a long scene has to be read before anything is written. Astral's prompt
// ordering exists to keep the second number small by not re-reading what has
// not changed, so it is worth being able to see it.
//
//	go test ./internal/eval/ -run TestThroughput -v \
//	    -models "huihui_ai/qwen3.6-abliterated:27b,huihui_ai/qwen3-abliterated:30b"

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
)

// A scene long enough that reading it is a real cost, in the shape Astral
// actually sends: a system prompt, a transcript, then lore and the anchor.
func throughputScene(turns int) (chars.Character, chars.Scene) {
	c := chars.Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet. Tall, ink to the elbows, never without her brass dividers. She speaks as if every sentence costs her something.",
		Personality: "wry, guarded, precise",
		Scenario:    "Her map room, late, during a storm.",
	}
	p := chars.Persona{Name: "Christian", Style: chars.DefaultStyle()}
	var hist []ollama.Message
	for i := 0; i < turns; i++ {
		if i%2 == 0 {
			hist = append(hist, ollama.Message{Role: ollama.RoleUser,
				Content: "I set another ruined chart on the desk and wait for her to say something about it."})
			continue
		}
		hist = append(hist, ollama.Message{Role: ollama.RoleAssistant,
			Content: `*She did not look up. The rain had found the window again, and she let it.* "The ferries are late because someone is paying for them to be late."` +
				"\n\n" + `*A pin went into the table rather than the map, a small and deliberate violence.* "Sit. You are dripping on the Sever."`})
	}
	return c, chars.Scene{
		Persona: p,
		History: hist,
		Lore:    "## Kestrel Bay\nA port city three days north. Its ferries have never once run on time.\n",
		Budget:  chars.Plan(8192, 0, 2500),
	}
}

func TestThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	if *modelList == "" {
		t.Skip("no models given: pass -models a,b,c")
	}
	models := strings.Split(*modelList, ",")

	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	probeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	installed, err := client.Probe(probeCtx)
	if err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}

	c, sc := throughputScene(20)
	msgs := chars.BuildMessages(c, sc)
	promptChars := 0
	for _, m := range msgs {
		promptChars += len(m.Content)
	}
	t.Logf("prompt: %d messages, %d characters\n", len(msgs), promptChars)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%-34s %10s %10s %10s %10s %8s\n",
		"MODEL", "PROMPT tk", "PROMPT t/s", "GEN tk", "GEN t/s", "WALL"))

	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if !ollama.HasModel(installed, model) {
			t.Logf("skipping %s: not installed", model)
			continue
		}
		noThink := false

		// Warm, then measure. A cold model's first request is a disk read.
		warm, cancelWarm := context.WithTimeout(context.Background(), 10*time.Minute)
		_, _, err := client.Chat(warm, model, []ollama.Message{{Role: ollama.RoleUser, Content: "Say OK."}},
			ollama.Options{NumCtx: 8192, NumPredict: 4}, &noThink, nil)
		cancelWarm()
		if err != nil {
			b.WriteString(fmt.Sprintf("%-34s  %v\n", short(model), err))
			continue
		}

		ctx, cancelRun := context.WithTimeout(context.Background(), 10*time.Minute)
		start := time.Now()
		_, stats, err := client.Chat(ctx, model, msgs,
			ollama.Options{NumCtx: 8192, Temperature: 0.85, TopP: 0.92, NumPredict: chars.DefaultReplyTokens},
			&noThink, nil)
		wall := time.Since(start)
		cancelRun()
		if err != nil {
			b.WriteString(fmt.Sprintf("%-34s  %v\n", short(model), err))
			continue
		}
		b.WriteString(fmt.Sprintf("%-34s %10d %10.1f %10d %10.1f %7.1fs\n",
			short(model), stats.PromptTokens, stats.PromptTokPerSec,
			stats.Tokens, stats.TokPerSec, wall.Seconds()))

		if l, err := client.Running(context.Background()); err == nil {
			if got, ok := ollama.FindLoaded(l, model); ok && got.Spilled() {
				b.WriteString(fmt.Sprintf("%-34s  (only %.0f%% on the GPU)\n", "", 100*got.OnGPU()))
			}
		}
	}
	t.Log(b.String())
}
