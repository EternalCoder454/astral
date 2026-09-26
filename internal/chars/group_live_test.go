package chars

import (
	"context"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// A group scene asks something of the model that a two-hander does not: it has
// to mark who is speaking, and it has to decide who stays quiet. Neither can be
// settled by reading the prompt and deciding it sounds convincing, because the
// failure modes are exactly the ones that sound fine written down — the labels
// drift into prose, or every character says one line in order, every turn,
// forever.
//
// So these run against a real model, and skip themselves where there is none.
// ASTRAL_TEST_MODEL picks it.

// liveModel, which finds a server and a model, is next door in live_test.go.

func liveCast() []Character {
	return []Character{
		{
			ID: 1, Name: "Vesper", Personality: "wry, guarded, precise",
			Description: "A cartographer of coastlines that have not settled yet. Speaks sparely and does not look up from her work.",
			Scenario:    "The map room, past midnight. {{user}} has just come in out of the rain.",
		},
		{
			ID: 2, Name: "Kestrel", Personality: "restless, blunt, early for everything",
			Description: "A courier who has been waiting in the map room for two hours and has read everything on the table.",
		},
		{
			ID: 3, Name: "Ash", Personality: "tired, formal, unwilling to be drawn in",
			Description: "The harbourmaster's clerk, here to collect a signature and leave.",
		},
	}
}

func livePersona() Persona {
	return Persona{Name: "Wren", Description: "A courier who reads the messages they carry."}
}

// liveTurn plays one turn of a group scene and returns the reply.
func liveTurn(t *testing.T, client *ollama.Client, model string, cast []Character, hist []ollama.Message) string {
	t.Helper()
	p := livePersona()
	sc := Scene{Persona: p, History: hist, Budget: Plan(8192, 400, len(BuildGroupSystem(cast, p)))}
	msgs := BuildGroupMessages(cast, sc)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	no := false
	reply, _, err := client.Chat(ctx, model, msgs,
		ollama.Options{Temperature: 0.8, NumCtx: 8192, NumPredict: 400}, &no, nil)
	if err != nil {
		t.Fatalf("the model failed: %v", err)
	}
	_, content := ollama.SplitThinking(reply.Content)
	return strings.TrimSpace(content)
}

// TestLiveGroupReplyIsLabelled is the format. Everything downstream depends on
// it: a reply with no labels is one character saying the whole turn, and the
// transcript that results teaches the model to do it again.
func TestLiveGroupReplyIsLabelled(t *testing.T) {
	client, model := liveModel(t)
	t.Logf("model: %s", model)
	cast := liveCast()
	names := CastNames(cast)

	hist := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: Label("Vesper", GreetingAt(cast[0], livePersona(), 0))},
		{Role: ollama.RoleUser, Content: "I set the lantern down. \"Somebody tell me why the coastline moved.\""},
	}
	reply := liveTurn(t, client, model, cast, hist)
	t.Logf("reply:\n%s", reply)

	beats := SplitBeats(reply, names)
	if len(beats) == 0 {
		t.Fatal("the reply split into no beats at all")
	}
	for i, b := range beats {
		if b.Name == "" {
			t.Errorf("beat %d is attributed to nobody: %q", i, b.Text)
		}
	}
	// And no label survived into the prose, which is what a near-miss format
	// looks like from the reader's side.
	for _, b := range beats {
		for _, n := range names {
			if strings.Contains(b.Text, "\n"+n+":") {
				t.Errorf("a label was left inside a beat by %s: %q", b.Name, b.Text)
			}
		}
	}
}

// TestLiveGroupDoesNotWriteTheUser is the rule a group breaks more easily than a
// two-hander, because the model is already writing several people and one more
// is a small step.
func TestLiveGroupDoesNotWriteTheUser(t *testing.T) {
	client, model := liveModel(t)
	cast := liveCast()
	hist := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: Label("Vesper", "*She did not look up.* \"You're late.\"")},
		{Role: ollama.RoleUser, Content: "\"I know. What do you want me to do about it?\""},
	}
	reply := liveTurn(t, client, model, cast, hist)
	t.Logf("reply:\n%s", reply)

	for _, b := range SplitBeats(reply, CastNames(cast)) {
		if strings.EqualFold(b.Name, "Wren") {
			t.Errorf("the model wrote a turn as the user: %q", b.Text)
		}
	}
	if strings.Contains(reply, "\nWren:") || strings.HasPrefix(reply, "Wren:") {
		t.Errorf("the model labelled a beat with the user's name:\n%s", reply)
	}
}

// TestLiveGroupIsNotARollCall is the quality of the thing rather than its
// format. Three turns, and if every one of them has all three characters
// speaking once in the same order then the scene is a list and the framing has
// not worked.
func TestLiveGroupIsNotARollCall(t *testing.T) {
	client, model := liveModel(t)
	cast := liveCast()
	names := CastNames(cast)

	hist := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: Label("Vesper", "*She did not look up from the chart.* \"You're late.\"")},
	}
	prompts := []string{
		"\"Somebody tell me why the coastline moved.\"",
		"*I pull the chart towards me.* \"Show me where.\"",
		"\"And whose signature is missing?\"",
	}
	lists := 0
	for i, p := range prompts {
		hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: p})
		reply := liveTurn(t, client, model, cast, hist)
		t.Logf("turn %d:\n%s\n", i+1, reply)
		hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: reply})

		beats := SplitBeats(reply, names)
		spoke := map[string]bool{}
		inOrder := len(beats) >= len(names)
		for j, b := range beats {
			spoke[b.Name] = true
			if j < len(names) && b.Name != names[j] {
				inOrder = false
			}
		}
		if inOrder && len(spoke) == len(names) {
			lists++
		}
		t.Logf("  turn %d: %d beats, %d of %d characters spoke", i+1, len(beats), len(spoke), len(names))
	}
	if lists == len(prompts) {
		t.Errorf("all %d turns were a roll call: every character speaking once, in cast order. "+
			"The framing is not getting the turn taking across", lists)
	}
}

// TestLiveGroupTalksToEachOther is the reason for a group at all. Somebody has to
// address somebody other than the user, or this is three two-handers in one
// window.
func TestLiveGroupTalksToEachOther(t *testing.T) {
	client, model := liveModel(t)
	cast := liveCast()
	names := CastNames(cast)

	hist := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: Label("Vesper", "*She did not look up.* \"You're late.\"")},
		{Role: ollama.RoleUser, Content: "\"Don't look at me. Ask Kestrel, she was here first.\""},
	}
	reply := liveTurn(t, client, model, cast, hist)
	t.Logf("reply:\n%s", reply)

	// One character naming another is the cheap, reliable signal. It is not the
	// whole of talking to each other, but a reply in which nobody mentions
	// anybody is not it.
	crossed := false
	for _, b := range SplitBeats(reply, names) {
		for _, n := range names {
			if !strings.EqualFold(n, b.Name) && strings.Contains(b.Text, n) {
				crossed = true
			}
		}
	}
	if !crossed {
		t.Errorf("nobody addressed or mentioned anybody else; the cast is not in the same room:\n%s", reply)
	}
}
