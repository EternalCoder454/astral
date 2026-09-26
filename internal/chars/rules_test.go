package chars

import (
	"context"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// The rulebook reaches the model through Persona.GlobalInstructions, which is
// the slot the old freeform instruction block used. This is the test that the
// slot still goes to both places that matter: stated once near the front, and
// restated in the closing block, which is the position a model actually obeys.
//
// It is here rather than in internal/store because store knows how to render a
// rulebook and this package decides where a rendered one goes.
func TestRulesReachBothPositions(t *testing.T) {
	p := Persona{
		Name:               "Wren",
		GlobalInstructions: "1. Keep replies to two paragraphs.\n2. Nobody names their own feelings.",
	}
	c := Character{Name: "Vesper", Description: "A cartographer."}

	sys := BuildSystem(c, p)
	if !strings.Contains(sys, "Keep replies to two paragraphs.") {
		t.Error("the rules never reach the system prompt")
	}
	if !strings.Contains(sys, "take priority over the general guidance above") {
		t.Error("the rules arrive without being marked as the user's own, so they do not outrank the framing")
	}

	anchor := Anchor(c, Scene{Persona: p}, "Wren")
	if !strings.Contains(anchor, "Nobody names their own feelings.") {
		t.Error("the rules never reach the closing block, which is the position a model obeys")
	}
}

// And the same for a scene with a cast, which assembles its prompt separately and
// so could lose them without anything else noticing.
func TestRulesReachAGroupScene(t *testing.T) {
	p := Persona{Name: "Wren", GlobalInstructions: "1. Keep replies to two paragraphs."}
	cast := threeHanded()

	if sys := BuildGroupSystem(cast, p); !strings.Contains(sys, "Keep replies to two paragraphs.") {
		t.Error("the rules never reach a group's system prompt")
	}
	if a := GroupAnchor(cast, Scene{Persona: p}, "Wren"); !strings.Contains(a, "Keep replies to two paragraphs.") {
		t.Error("the rules never reach a group's closing block")
	}
}

// TestLiveRuleIsObeyed is the only test that can answer whether the rulebook is
// worth having. That a rule reaches the prompt is checked above and proves
// nothing about whether a model follows it.
//
// The rule chosen is one that can be counted rather than judged: paragraphs per
// reply. Measured on a 27B over four turns, the same scene gave 2.75 paragraphs
// a turn with no rule and exactly 1.00 with "every reply is exactly one
// paragraph", which is the difference between a feature and a text box.
func TestLiveRuleIsObeyed(t *testing.T) {
	client, model := liveModel(t)
	c := Character{
		Name: "Vesper", Personality: "wry, guarded",
		Description: "A cartographer of coastlines that have not settled yet.",
		Scenario:    "The map room, past midnight.",
	}
	prompts := []string{
		"\"Tell me about the coastline.\"",
		"*I sit down.* \"All of it.\"",
		"\"And the harbour?\"",
	}

	run := func(rules string) float64 {
		p := Persona{Name: "Wren", GlobalInstructions: rules}
		var hist []ollama.Message
		paragraphs := 0
		for _, q := range prompts {
			hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: q})
			sc := Scene{Persona: p, History: hist, Budget: Plan(8192, 400, len(BuildSystem(c, p)))}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			no := false
			reply, _, err := client.Chat(ctx, model, BuildMessages(c, sc),
				ollama.Options{Temperature: 0.8, NumCtx: 8192, NumPredict: 400}, &no, nil)
			cancel()
			if err != nil {
				t.Fatalf("the model failed: %v", err)
			}
			_, body := ollama.SplitThinking(reply.Content)
			body = strings.TrimSpace(body)
			hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: body})
			for _, block := range strings.Split(body, "\n\n") {
				if strings.TrimSpace(block) != "" {
					paragraphs++
				}
			}
		}
		return float64(paragraphs) / float64(len(prompts))
	}

	free := run("")
	bound := run("1. Every reply is exactly one paragraph. Never more.")
	t.Logf("model %s: %.2f paragraphs a turn with no rule, %.2f with one", model, free, bound)

	if bound >= free {
		t.Errorf("the rule changed nothing: %.2f paragraphs a turn with it against %.2f without",
			bound, free)
	}
	// Loose enough not to fail on one stray reply, tight enough to mean the rule
	// was followed rather than merely noticed.
	if bound > 1.5 {
		t.Errorf("a rule asking for one paragraph produced %.2f a turn", bound)
	}
}
