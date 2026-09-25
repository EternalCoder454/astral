package eval

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

// The oldest complaint in roleplay is the model taking your turn: deciding
// that you nodded, that you felt the cold, that you agreed. Astral forbids it
// twice, in the system prompt and again in the anchor, and both times in the
// negative: "never write, decide or narrate {{user}}'s words, thoughts or
// actions".
//
// A common piece of advice says that is the wrong shape, and that a model
// follows "do this" better than "do not do that". It is a plausible claim and
// it is testable, so rather than take a position this measures both wordings
// on the same scenes with the same model and lets the judge count.
//
//	go test ./internal/eval/ -run TestFraming -v \
//	    -models huihui_ai/qwen3.6-abliterated:27b -runs 6
const (
	negativeRule = "Never write, decide or narrate %s's words, thoughts or actions."
	positiveRule = "Stop your reply at the moment %[1]s would act or speak, and leave that for %[1]s to write."
)

// Turns that tempt a model to take the user's turn. A scene where the user has
// just acted decisively gives the model nothing to take, so it would measure
// nothing — an earlier version of this list was passive rather than inviting
// and came back zero against zero, control included.
//
// What actually tempts it is delegation and joint movement: a turn that hands
// over the initiative, or a situation where the obvious next sentence has both
// people doing something. Travel is the classic one, because "they walked down
// to the harbour" is a single natural sentence that decides for two people.
var temptingTurns = []string{
	"I let her take the lead. Whatever she wants to do next, I'll go along with it.",
	"\"Fine. Show me, then.\"",
	"*I hold out my hand for the chart and wait for her to decide.*",
	"\"You tell me what happens next. I've run out of ideas.\"",
	"I nod, and let her walk me through the rest of the evening.",
	"*I follow her out into the rain, and down toward the water.*",
	"\"Whatever you say.\"",
	"I give up arguing and just do what she asks.",
}

func TestFramingPositiveVersusNegative(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	if *modelList == "" {
		t.Skip("no models given: pass -models a,b,c")
	}
	judge := os.Getenv("ASTRAL_JUDGE_MODEL")
	if judge == "" {
		t.Skip("set ASTRAL_JUDGE_MODEL to a model calibrated by TestJudgeAgreesOnClearCases")
	}

	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	probeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	installed, err := client.Probe(probeCtx)
	if err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}

	const user, charName = "Christian", "Vesper Quill"
	c := chars.Character{
		Name:        charName,
		Description: "A cartographer of places that have not happened yet. She speaks as if every sentence costs her something.",
		Scenario:    "Her map room, late, during a storm.",
	}
	base := []ollama.Message{
		{Role: ollama.RoleUser, Content: "I push the door open and shake the rain off my coat."},
		{Role: ollama.RoleAssistant, Content: `*She did not look up from the chart.* "You're late."`},
	}

	// Swap the wording in the assembled prompt. The two rules say the same
	// thing and differ only in shape, which is the whole point.
	//
	// "none" removes the rule altogether. It is the control, and it is not
	// optional: a comparison that comes back zero against zero has either
	// found that both wordings work or found that the test cannot detect the
	// failure at all, and only the control tells the two apart.
	swap := func(msgs []ollama.Message, variant string) []ollama.Message {
		neg := fmt.Sprintf(negativeRule, user)
		negAlt := "Never write, decide or narrate " + user + "'s words, thoughts, or actions, wait for them."
		var repl string
		switch variant {
		case "positive":
			repl = fmt.Sprintf(positiveRule, user)
		case "none":
			repl = ""
		default:
			return msgs
		}
		out := make([]ollama.Message, len(msgs))
		copy(out, msgs)
		for i, m := range out {
			if m.Role != ollama.RoleSystem {
				continue
			}
			body := strings.ReplaceAll(m.Content, neg, repl)
			body = strings.ReplaceAll(body, negAlt, repl)
			out[i].Content = body
		}
		return out
	}

	for _, model := range strings.Split(*modelList, ",") {
		model = strings.TrimSpace(model)
		if model == "" || !ollama.HasModel(installed, model) {
			continue
		}
		type tally struct{ took, total int }
		variants := []string{"negative", "positive", "none"}
		results := map[string]*tally{"negative": {}, "positive": {}, "none": {}}
		var examples []string

		for _, turn := range temptingTurns {
			hist := append(append([]ollama.Message{}, base...),
				ollama.Message{Role: ollama.RoleUser, Content: turn})
			sc := chars.Scene{
				Persona: chars.Persona{Name: user, Style: chars.DefaultStyle()},
				History: hist,
			}
			msgs := chars.BuildMessages(c, sc)

			for _, variant := range variants {
				ctx, cancelRun := context.WithTimeout(context.Background(), 4*time.Minute)
				noThink := false
				reply, _, err := client.Chat(ctx, model, swap(msgs, variant),
					ollama.Options{NumCtx: 8192, Temperature: 0.85, TopP: 0.92, NumPredict: 400},
					&noThink, nil)
				cancelRun()
				if err != nil {
					t.Fatalf("%s/%s: %v", model, variant, err)
				}
				body := strings.TrimSpace(reply.Content)

				jctx, cancelJudge := context.WithTimeout(context.Background(), 4*time.Minute)
				v, err := WroteForUser(jctx, client, judge, body, user, charName)
				cancelJudge()
				if err != nil {
					t.Fatalf("judging %s/%s: %v", model, variant, err)
				}
				results[variant].total++
				if v.Verdict {
					results[variant].took++
					examples = append(examples, fmt.Sprintf("  [%s] %q\n     after: %q",
						variant, strings.TrimSpace(v.Quote), turn))
				}
			}
		}
		t.Logf("%s, judged by %s:", short(model), short(judge))
		for _, variant := range variants {
			r := results[variant]
			t.Logf("  %-9s took the user's turn in %d of %d replies", variant, r.took, r.total)
		}
		if results["none"].took == 0 {
			t.Logf("  NOTE: with the rule removed entirely the model still never took the "+
				"user's turn, so these scenes do not tempt %s and the comparison above "+
				"shows nothing either way", short(model))
		}
		for _, e := range examples {
			t.Logf("%s", e)
		}
	}
}
