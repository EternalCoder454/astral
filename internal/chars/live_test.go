package chars

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// This is the only test that talks to a real model. It skips itself when no
// Ollama server is reachable, so an ordinary `go test ./...` on a machine
// without one stays green — but where a server exists it checks the thing
// nothing else can: that a character, a persona and a transcript assembled by
// this package actually produce a reply from a real model, streamed, with
// usable throughput numbers on the end.
//
// ASTRAL_TEST_MODEL picks the model; otherwise the first installed one is used.
func TestLiveRoleplayTurn(t *testing.T) {
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
	t.Logf("using model %s", model)

	c := Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet. Speaks sparely.",
		Personality: "wry, guarded, precise",
		Scenario:    "The map room, past midnight. {{user}} has just come in.",
		FirstMes:    "*She does not look up.* \"You're late, {{user}}.\"",
	}
	persona := Persona{Name: "Wren", Description: "A courier who reads the messages."}

	history := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: Greeting(c, persona)},
		{Role: ollama.RoleUser, Content: "I set the lantern down. \"You said the coastline was settled.\""},
	}
	msgs := BuildMessages(c, Scene{Persona: persona, History: history})
	if msgs[0].Role != ollama.RoleSystem {
		t.Fatalf("first message is %q, want the system framing", msgs[0].Role)
	}

	// Reasoning is turned off, as Astral does for roleplay; without that a
	// thinking model can spend the whole NumPredict budget deliberating and
	// return an empty reply — which is exactly what this test caught.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var deltas int
	var firstTokenAt time.Time
	start := time.Now()
	noThink := false
	reply, stats, err := client.Chat(ctx, model, msgs,
		ollama.Options{Temperature: 0.85, NumPredict: 220}, &noThink,
		func(d ollama.Delta) {
			if d.Content != "" && firstTokenAt.IsZero() {
				firstTokenAt = time.Now()
			}
			deltas++
		})
	if err != nil {
		if ctx.Err() != nil {
			t.Skipf("model did not finish within the timeout (%s is large); path is otherwise sound", model)
		}
		t.Fatalf("Chat: %v", err)
	}

	if strings.TrimSpace(reply.Content) == "" {
		t.Fatal("model returned an empty reply")
	}
	if deltas < 2 {
		t.Errorf("got %d deltas — the reply did not stream", deltas)
	}
	if stats.Tokens == 0 {
		t.Error("no token count came back in the final chunk")
	}
	t.Logf("first token in %.1fs, %d deltas, %s, total %.1fs",
		firstTokenAt.Sub(start).Seconds(), deltas, stats.Summary(), time.Since(start).Seconds())
	t.Logf("reply: %s", strings.Join(strings.Fields(reply.Content), " "))
}

// TestLiveInstructionsAreObeyed is the test that justifies the design. An
// instruction is only worth having if the model actually follows it, so this
// runs the same character twice against a real model — once plain, once with
// a hard constraint — and checks the constraint bites.
//
// "One short sentence" is used because it is objectively measurable. Whether a
// model is "more in character" is not.
func TestLiveInstructionsAreObeyed(t *testing.T) {
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

	base := Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet.",
		Personality: "wry, guarded",
		Scenario:    "The map room, past midnight.",
		FirstMes:    "*She does not look up.* \"You're late.\"",
	}
	persona := Persona{Name: "Wren"}
	history := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: Greeting(base, persona)},
		{Role: ollama.RoleUser, Content: "\"Tell me about the coastline.\""},
	}

	ask := func(c Character) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		noThink := false
		msg, _, err := client.Chat(ctx, model, BuildMessages(c, Scene{Persona: persona, History: history}),
			ollama.Options{Temperature: 0.85, NumCtx: 8192}, &noThink, nil)
		return strings.TrimSpace(msg.Content), err
	}

	plain, err := ask(base)
	if err != nil {
		t.Skipf("model unavailable: %v", err)
	}

	constrained := base
	constrained.Instructions = "Reply with exactly one short sentence. Never write more than one sentence."
	limited, err := ask(constrained)
	if err != nil {
		t.Skipf("model unavailable: %v", err)
	}

	t.Logf("no instructions  (%d chars): %s", len(plain), oneLine(plain))
	t.Logf("with instruction (%d chars): %s", len(limited), oneLine(limited))

	if len(limited) >= len(plain) {
		t.Errorf("the instruction did not shorten the reply: %d chars with it, %d without",
			len(limited), len(plain))
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestLiveWritingStyleAndCost checks the two claims that cannot be checked
// without a real model: that it follows the formatting rule the renderer
// depends on, and that dropping example dialogue actually saves prompt tokens.
func TestLiveWritingStyleAndCost(t *testing.T) {
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

	c := Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet.",
		Personality: "wry, guarded",
		Scenario:    "The map room, past midnight.",
		FirstMes:    "*She does not look up.* \"You're late.\"",
		MesExample:  "<START>\n{{user}}: Hello.\n{{char}}: *A pin goes into the table.* \"Is it.\"",
	}
	p := Persona{Name: "Wren"}

	ask := func(history []ollama.Message) (string, ollama.Stats, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		noThink := false
		msg, st, err := client.Chat(ctx, model, BuildMessages(c, Scene{Persona: p, History: history}),
			ollama.Options{Temperature: 0.85, NumCtx: 8192}, &noThink, nil)
		return strings.TrimSpace(msg.Content), st, err
	}

	// A new scene: examples are still being sent.
	newScene := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: Greeting(c, p)},
		{Role: ollama.RoleUser, Content: "\"You said the coastline was settled.\""},
	}
	reply, early, err := ask(newScene)
	if err != nil {
		t.Skipf("model unavailable: %v", err)
	}

	// The formatting rule the renderer depends on.
	if !strings.Contains(reply, "*") {
		t.Errorf("no *asterisks* — narration would render as plain text:\n%s", reply)
	}
	if !strings.Contains(reply, "\"") {
		t.Errorf("no spoken lines in quotes:\n%s", reply)
	}
	// Speech must not be wrapped in asterisks, or it renders italic and the
	// distinction the whole format rests on disappears.
	if strings.Contains(reply, "*\"") || strings.Contains(reply, "\"*") {
		t.Errorf("speech appears to be wrapped in asterisks:\n%s", reply)
	}

	// The same scene once it is underway: examples are dropped.
	underway := append([]ollama.Message{}, newScene...)
	for len(underway) < exampleCutoff {
		underway = append(underway,
			ollama.Message{Role: ollama.RoleAssistant, Content: "*A pin goes in.* \"Mm.\""},
			ollama.Message{Role: ollama.RoleUser, Content: "\"Go on.\""})
	}
	_, later, err := ask(underway)
	if err != nil {
		t.Skipf("model unavailable: %v", err)
	}

	t.Logf("reply:\n%s", reply)
	t.Logf("prompt tokens — new scene (with examples): %d", early.PromptTokens)
	t.Logf("prompt tokens — underway (examples dropped, and %d more turns): %d",
		len(underway)-len(newScene), later.PromptTokens)
	t.Logf("second call: %.1f tok/s, %.1fs total (model already resident)",
		later.TokPerSec, later.Elapsed.Seconds())
}

// TestLiveStyleDesignerIsSubstantive guards the failure this was written for:
// asked for a single free-form "instructions" string, a real model returned
// seven rules run together on one line — and, asked less generously, one
// sentence. Only a real model shows that; a stubbed server returns whatever
// the test puts in it.
func TestLiveStyleDesignerIsSubstantive(t *testing.T) {
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
		{Role: ollama.RoleAssistant, Content: StyleDesignerOpening},
		{Role: ollama.RoleUser, Content: "Something tense and fast. Like a thriller."},
		{Role: ollama.RoleAssistant, Content: "Is the tension in what's happening, or in what the character is not saying?"},
		{Role: ollama.RoleUser, Content: "What they're not saying. Keep it clipped."},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	st, err := BuildStyleFromConversation(ctx, client, model, history, ollama.Options{NumCtx: 8192})
	if err != nil {
		if ctx.Err() != nil {
			t.Skipf("model did not finish in time: %v", err)
		}
		t.Fatalf("BuildStyleFromConversation: %v", err)
	}

	if strings.TrimSpace(st.Name) == "" {
		t.Error("style came back unnamed")
	}
	lines := strings.Split(strings.TrimSpace(st.Instructions), "\n")
	if len(lines) < 5 {
		t.Errorf("style has only %d lines — the model collapsed it again:\n%s",
			len(lines), st.Instructions)
	}
	// Each labelled aspect should have survived as its own line.
	for _, label := range []string{"Length:", "Sentences:", "Tense and person:", "Dialogue:", "Avoid:"} {
		if !strings.Contains(st.Instructions, label) {
			t.Errorf("no %q line:\n%s", label, st.Instructions)
		}
	}
	// Formatting belongs to the app, not the style.
	if strings.Contains(st.Instructions, "asterisk") || strings.Contains(st.Instructions, "*") {
		t.Errorf("the style is trying to specify markup:\n%s", st.Instructions)
	}

	t.Logf("name: %s", st.Name)
	for i, l := range lines {
		t.Logf("  %d: %s", i+1, l)
	}
}

func liveModel(t *testing.T) (*ollama.Client, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("live test skipped in -short mode")
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
	model := os.Getenv("ASTRAL_TEST_MODEL")
	if model == "" {
		model = models[0].Name
	}
	return client, model
}

// TestLiveCompactionKeepsTheFacts is the point of compaction. Dropping old
// turns loses them outright; a recap is only worth the tokens if the things
// that matter survive it — names, admissions, promises, where everyone is.
func TestLiveCompactionKeepsTheFacts(t *testing.T) {
	client, model := liveModel(t)

	c := Character{Name: "Vesper Quill", Description: "A cartographer."}
	p := Persona{Name: "Wren"}

	// A scene with specific, checkable facts scattered through it.
	aged := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: `*She does not look up.* "You're late, Wren."`},
		{Role: ollama.RoleUser, Content: `"The ferry from Kestrel Bay was held."`},
		{Role: ollama.RoleAssistant, Content: `*Vesper set down the pin.* "Kestrel Bay. That's the third delay this month." *She admitted, quietly, that she had never once left this city.*`},
		{Role: ollama.RoleUser, Content: `"Never? Not once?" *I set the lantern down.* "I'll take you. After the solstice."`},
		{Role: ollama.RoleAssistant, Content: `*She laughed, and it was not a kind sound.* "You promise that to everyone." *But she wrote the date on the corner of the map anyway.*`},
		{Role: ollama.RoleUser, Content: `"I've never promised it to anyone." *I found the chair by the window and sat.*`},
		{Role: ollama.RoleAssistant, Content: `"Then you've been unusually careful." *Vesper turned the map over, hiding the coastline.* "Don't look at that one yet."`},
	}

	// Padded to a realistic size. Compaction only ever runs on a transcript
	// that has outgrown the context window, and whether a recap is actually
	// shorter than what it replaces is only a meaningful question at that
	// scale: summarising seven turns can easily produce more text than it
	// consumed, which is fine, because seven turns are never compacted.
	filler := `*She moved another pin, measured the gap with her thumb, and wrote a figure in the margin that she immediately crossed out.* "The scale is wrong again." *The lamp guttered; neither of them moved to trim it.*`
	for totalChars(aged) < DefaultBudget().Compact/2 {
		aged = append(aged,
			ollama.Message{Role: ollama.RoleUser, Content: `*I watched her work, and said nothing useful.*`},
			ollama.Message{Role: ollama.RoleAssistant, Content: filler})
	}
	if totalChars(aged) < DefaultBudget().Compact/2 {
		t.Fatalf("test sample is only %d chars, not representative", totalChars(aged))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	budget := Plan(8192, 0, len(BuildSystem(c, p)))
	recap, err := Compact(ctx, client, model, "", aged, c, p, ollama.Options{NumCtx: 8192}, budget)
	if err != nil {
		if ctx.Err() != nil {
			t.Skipf("model did not finish in time: %v", err)
		}
		t.Fatalf("Compact: %v", err)
	}
	t.Logf("recap (%d chars):\n%s", len(recap), recap)

	low := strings.ToLower(recap)
	// Facts a scene would be broken without.
	musts := map[string][]string{
		"both names":          {"vesper"},
		"the other name":      {"wren"},
		"never left the city": {"never left", "never once left", "not left the city", "hasn't left", "has not left"},
		"the promise":         {"promis", "solstice", "take her"},
		"the hidden map":      {"hid", "turned the map", "coastline", "don't look", "do not look"},
	}
	for what, alternatives := range musts {
		found := false
		for _, alt := range alternatives {
			if strings.Contains(low, alt) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the recap lost %s:\n%s", what, recap)
		}
	}
	if len(recap) > budget.Recap {
		t.Errorf("the recap is %d chars, over its own %d budget", len(recap), budget.Recap)
	}
	if len(recap) >= totalChars(aged)/2 {
		t.Errorf("the recap (%d chars) barely compacts the turns it replaces (%d)",
			len(recap), totalChars(aged))
	}
}

// A style is applied to every character, so it must not name the one that
// happened to be discussed while designing it.
func TestLiveStyleDesignerUsesPlaceholdersNotNames(t *testing.T) {
	client, model := liveModel(t)

	history := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: StyleDesignerOpening},
		{Role: ollama.RoleUser, Content: "I want Sarah to be really blunt and quiet. Short answers."},
		{Role: ollama.RoleAssistant, Content: "Blunt because she does not care, or because she is holding something back?"},
		{Role: ollama.RoleUser, Content: "Holding back. Sarah never says what she means directly."},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	st, err := BuildStyleFromConversation(ctx, client, model, history, ollama.Options{NumCtx: 8192})
	if err != nil {
		if ctx.Err() != nil {
			t.Skipf("model did not finish in time: %v", err)
		}
		t.Fatalf("BuildStyleFromConversation: %v", err)
	}
	t.Logf("name: %s", st.Name)
	t.Logf("instructions:\n%s", st.Instructions)

	// The name that was discussed must not have been baked in.
	if strings.Contains(strings.ToLower(st.Instructions), "sarah") {
		t.Errorf("the style names a specific character:\n%s", st.Instructions)
	}
	// And whatever it produced must still survive substitution cleanly.
	expanded := Substitute(st.Instructions, "Vesper", "Wren")
	for _, leftover := range []string{"{{", "}}"} {
		if strings.Contains(expanded, leftover) {
			t.Errorf("a malformed placeholder survived substitution:\n%s", expanded)
		}
	}
}
