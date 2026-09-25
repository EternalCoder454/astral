package eval

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

func judgeClient(t *testing.T) (*ollama.Client, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	models, err := client.Probe(ctx)
	if err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}
	if len(models) == 0 {
		t.Skip("Ollama is running but has no models installed")
	}
	// Named explicitly, never guessed. This is instrument calibration: run
	// against whichever model happens to be installed first it would fail for
	// the right reason at an unhelpful moment, in the middle of an ordinary
	// `go test ./...`. Measured, a 27B agreed on 8 of 8 and a 4B on 4 of 8.
	m := os.Getenv("ASTRAL_JUDGE_MODEL")
	if m == "" {
		t.Skip("set ASTRAL_JUDGE_MODEL to the model to calibrate as a judge")
	}
	if !ollama.HasModel(models, m) {
		t.Skipf("%s is not installed", m)
	}
	return client, m
}

// Before the judge is used to compare two prompts, it has to agree with a
// person on cases where the answer is not in doubt. A judge that has not been
// checked is worse than no judge: it produces numbers, and numbers get
// believed.
//
// The hard cases here are the near misses in both directions. A reply that
// describes the user, speaks to them, or wonders about them is correct writing
// and must not be flagged; a reply that gives them a nod or a feeling must be.
func TestJudgeAgreesOnClearCases(t *testing.T) {
	client, model := judgeClient(t)
	const user, char = "Christian", "Vesper"

	cases := []struct {
		name  string
		reply string
		want  bool
	}{
		{
			"plainly took the user's turn",
			`*She pushed the chair out with her foot.* "Sit."` + "\n\n" + `*Christian sat, and rubbed the warmth back into his hands.*`,
			true,
		},
		{
			"decided what the user felt",
			`*She did not look up.* "You're late." *The cold settled into Christian's shoulders and he found he could not answer her.*`,
			true,
		},
		{
			"put words in the user's mouth",
			`*She waited.* "Well?" *Christian told her the ferry had been held at the Bay.*`,
			true,
		},
		{
			"only her own turn",
			`*She did not look up from the chart. The rain had found the window again, and she let it.* "You're late."`,
			false,
		},
		{
			"describes the user without deciding anything",
			`*She watched him shake the rain off his coat, and said nothing about the puddle it made.* "Kestrel Bay again?"`,
			false,
		},
		{
			"speaks to the user and wonders about them",
			`*A pin went into the table rather than the map.* "Sit down, Christian." *She wondered whether he would say anything at all this time.*`,
			false,
		},
		{
			// Labelled false at first, and the judge disagreed. The judge was
			// right: the user never wrote this, so narrating it decides an
			// action for them however incidental it sounds.
			"narrates an action the user never took",
			`*He had set the lantern where the rain could not reach it, which she noticed and chose not to mention.* "Third delay this month."`,
			true,
		},
		{
			"refers to something the transcript already established",
			`*She had watched him do that the last three times, and said nothing about it then either.* "Third delay this month."`,
			false,
		},
	}

	agreed := 0
	for _, tt := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		v, err := WroteForUser(ctx, client, model, tt.reply, user, char)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		mark := "ok  "
		if v.Verdict != tt.want {
			mark = "MISS"
		} else {
			agreed++
		}
		fab := ""
		if v.Fabricated {
			fab = "  (quote was not in the passage)"
		}
		t.Logf("%s %-46s judged %-5v want %-5v  quote=%q%s",
			mark, tt.name, v.Verdict, tt.want, strings.TrimSpace(v.Quote), fab)
	}
	t.Logf("judge %s agreed on %d of %d", model, agreed, len(cases))
	// Six of seven. A judge used to compare two prompts does not have to be
	// perfect, but it does have to be better than a coin, and it has to be
	// checked rather than assumed.
	if agreed < len(cases)-1 {
		t.Errorf("judge agreed on only %d of %d clear cases, so its numbers cannot be trusted",
			agreed, len(cases))
	}
}
