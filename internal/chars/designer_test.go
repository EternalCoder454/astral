package chars

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// designServer stands in for Ollama's structured-output endpoint: it records
// what was asked and replies with the card it is given.
func designServer(t *testing.T, card string, sawRequest func(map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if sawRequest != nil {
			sawRequest(req)
		}
		resp, _ := json.Marshal(map[string]any{
			"message":       map[string]string{"role": "assistant", "content": card},
			"done":          true,
			"done_reason":   "stop",
			"eval_count":    120,
			"eval_duration": 1e9,
		})
		fmt.Fprintln(w, string(resp))
	}))
}

const designedCard = `{
  "name": "Odile Marchetti",
  "description": "A locksmith who has never once picked a lock she was paid to.",
  "personality": "dry, patient, quietly furious",
  "scenario": "Her shop, twenty minutes after closing. {{user}} has knocked anyway.",
  "first_mes": "*She does not turn the sign around.* \"We're shut.\"",
  "mes_example": "{{user}}: I need a favour.\n{{char}}: \"Everyone does.\"",
  "tags": ["noir", "slow-burn"]
}`

func TestBuildFromConversation(t *testing.T) {
	var req map[string]any
	srv := designServer(t, designedCard, func(r map[string]any) { req = r })
	defer srv.Close()

	history := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: "Tell me anything to start."},
		{Role: ollama.RoleUser, Content: "a locksmith with a grudge"},
		{Role: ollama.RoleAssistant, Content: "Is the grudge about the job, or someone specific?"},
		{Role: ollama.RoleUser, Content: "the job. she hates what it made her."},
	}
	c, err := BuildFromConversation(context.Background(),
		ollama.NewClient(srv.URL), "m", history, ollama.Options{})
	if err != nil {
		t.Fatalf("BuildFromConversation: %v", err)
	}
	if c.Name != "Odile Marchetti" {
		t.Errorf("Name = %q", c.Name)
	}
	if !strings.Contains(c.FirstMes, "We're shut") {
		t.Errorf("FirstMes = %q", c.FirstMes)
	}
	if strings.Join(c.Tags, ",") != "noir,slow-burn" {
		t.Errorf("Tags = %v", c.Tags)
	}

	// The request must carry a schema — that is what makes the reply parse
	// rather than merely usually parse.
	if req["format"] == nil {
		t.Error("no schema was sent; the model was only asked nicely for JSON")
	}
	// And it must not be streamed: there is nothing to show of a half-built object.
	if stream, ok := req["stream"].(bool); !ok || stream {
		t.Errorf("stream = %v, want false for a structured call", req["stream"])
	}
	// Reasoning off, or the model spends its budget deliberating before JSON
	// that is constrained anyway.
	if think, ok := req["think"].(bool); !ok || think {
		t.Errorf("think = %v, want false", req["think"])
	}

	// The whole design conversation has to reach the model, or it invents a
	// different character than the one that was discussed.
	msgs, _ := req["messages"].([]any)
	var blob strings.Builder
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok {
			blob.WriteString(fmt.Sprint(mm["content"]))
		}
	}
	for _, want := range []string{"locksmith with a grudge", "hates what it made her", DesignerSystem[:40]} {
		if !strings.Contains(blob.String(), want) {
			t.Errorf("request did not carry %q", want)
		}
	}
}

func TestBuildFromConversationNeedsAConversation(t *testing.T) {
	if _, err := BuildFromConversation(context.Background(),
		ollama.NewClient("http://127.0.0.1:1"), "m", nil, ollama.Options{}); err == nil {
		t.Error("building from an empty conversation was allowed")
	}
}

// A model that returns something schema-shaped but useless must be reported,
// not saved as a nameless character.
func TestBuildFromConversationRejectsUnusableResult(t *testing.T) {
	srv := designServer(t, `{"description":"no name here"}`, nil)
	defer srv.Close()

	_, err := BuildFromConversation(context.Background(),
		ollama.NewClient(srv.URL), "m",
		[]ollama.Message{{Role: ollama.RoleUser, Content: "hi"}}, ollama.Options{})
	if err == nil {
		t.Fatal("a card with no name was accepted")
	}
	if !strings.Contains(err.Error(), "usable") {
		t.Errorf("error does not explain the problem: %v", err)
	}
}

// The designed character has to survive the round trip into a real roleplay
// prompt — this is the join between the two halves of the feature.
func TestDesignedCharacterProducesAUsablePrompt(t *testing.T) {
	c, err := ParseCard([]byte(designedCard))
	if err != nil {
		t.Fatal(err)
	}
	persona := Persona{Name: "Wren"}
	msgs := BuildMessages(c, persona, "", nil)
	sys := msgs[0].Content
	for _, want := range []string{"Odile Marchetti", "dry, patient", "twenty minutes after closing"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
	if strings.Contains(sys, "{{user}}") {
		t.Error("placeholder survived into the system prompt")
	}
	if g := Greeting(c, persona); !strings.Contains(g, "We're shut") {
		t.Errorf("Greeting = %q", g)
	}
}

// The live counterpart: a real model designing a real character. Skipped when
// no Ollama server is reachable.
func TestLiveDesignCharacter(t *testing.T) {
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

	history := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: DesignerOpening},
		{Role: ollama.RoleUser, Content: "A lighthouse keeper who has been writing letters to someone who never replies."},
		{Role: ollama.RoleAssistant, Content: "Does he know they're not being read, or is that the part he won't let himself think about?"},
		{Role: ollama.RoleUser, Content: "He knows. He keeps writing anyway. Make him warm rather than tragic."},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	start := time.Now()
	c, err := BuildFromConversation(ctx, client, model, history, ollama.Options{NumCtx: 8192})
	if err != nil {
		if ctx.Err() != nil {
			t.Skipf("model did not finish in time: %v", err)
		}
		t.Fatalf("BuildFromConversation: %v", err)
	}

	// Every field the roleplay actually depends on must come back filled.
	for _, f := range []struct{ name, value string }{
		{"name", c.Name},
		{"description", c.Description},
		{"personality", c.Personality},
		{"scenario", c.Scenario},
		{"first_mes", c.FirstMes},
	} {
		if strings.TrimSpace(f.value) == "" {
			t.Errorf("%s came back empty", f.name)
		}
	}
	t.Logf("built in %.1fs", time.Since(start).Seconds())
	t.Logf("name: %s", c.Name)
	t.Logf("personality: %s", c.Personality)
	t.Logf("scenario: %s", strings.Join(strings.Fields(c.Scenario), " "))
	t.Logf("first_mes: %s", strings.Join(strings.Fields(c.FirstMes), " "))
	t.Logf("tags: %v", c.Tags)
}

func TestBuildStyleFromConversation(t *testing.T) {
	var req map[string]any
	card := `{"name":"Sparse","length":"One paragraph.","sentences":"Short.",
	          "tense":"Present tense, third person.","description":"Minimal.",
	          "dialogue":"Clipped and indirect.","avoid":"Adverbs."}`
	srv := designServer(t, card, func(r map[string]any) { req = r })
	defer srv.Close()

	st, err := BuildStyleFromConversation(context.Background(),
		ollama.NewClient(srv.URL), "m",
		[]ollama.Message{{Role: ollama.RoleUser, Content: "sparse and cold"}}, ollama.Options{})
	if err != nil {
		t.Fatalf("BuildStyleFromConversation: %v", err)
	}
	if st.Name != "Sparse" {
		t.Errorf("Name = %q", st.Name)
	}

	// Every aspect must come through as its own labelled line. Asking for one
	// free-form string got a single run-on line out of a real model, which is
	// what the decomposed schema exists to prevent.
	lines := strings.Split(st.Instructions, "\n")
	if len(lines) != 6 {
		t.Errorf("got %d instruction lines, want 6:\n%s", len(lines), st.Instructions)
	}
	for _, want := range []string{"Length: One paragraph.", "Sentences: Short.",
		"Tense and person: Present tense, third person.", "Description: Minimal.",
		"Dialogue: Clipped and indirect.", "Avoid: Adverbs."} {
		if !strings.Contains(st.Instructions, want) {
			t.Errorf("missing %q:\n%s", want, st.Instructions)
		}
	}

	if req["format"] == nil {
		t.Error("no schema was sent")
	}
	// The style must not be told about markup: the app owns that, and
	// repeating it only competes with the framing.
	msgs, _ := req["messages"].([]any)
	var blob strings.Builder
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok {
			blob.WriteString(fmt.Sprint(mm["content"]))
		}
	}
	if !strings.Contains(strings.ToLower(blob.String()), "do not mention asterisks") {
		t.Error("the extraction prompt does not warn the model off formatting")
	}
}

// A field answered with whitespace must be skipped, not rendered as a heading
// with nothing after it.
func TestAssembleStyleSkipsBlankFields(t *testing.T) {
	got := assembleStyle(map[string]string{
		"length": "Two paragraphs.", "sentences": "   ", "tense": "Past tense.",
	})
	if strings.Contains(got, "Sentences:") {
		t.Errorf("a blank field produced a heading:\n%s", got)
	}
	if !strings.Contains(got, "Length: Two paragraphs.") || !strings.Contains(got, "Tense and person: Past tense.") {
		t.Errorf("assembled = %q", got)
	}
}

func TestBuildStyleRejectsIncompleteResult(t *testing.T) {
	srv := designServer(t, `{"name":"Sparse"}`, nil)
	defer srv.Close()
	if _, err := BuildStyleFromConversation(context.Background(),
		ollama.NewClient(srv.URL), "m",
		[]ollama.Message{{Role: ollama.RoleUser, Content: "hi"}}, ollama.Options{}); err == nil {
		t.Error("a style with no instructions was accepted")
	}
}
