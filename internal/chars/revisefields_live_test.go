package chars

import (
	"context"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// TestLiveReviseFillsTheNewFields guards the half of a new field nobody sees
// until it is missing. Appearance and Speech were added to the character, the
// card and the editor, and the designer's schema was not told about them, so a
// revision quietly returned a character with both of them empty and looked
// like it had worked.
func TestLiveReviseFillsTheNewFields(t *testing.T) {
	client, model := liveModel(t)
	weak := Character{
		ID: 9, Name: "Mara",
		Description: "Mysterious. A woman with a past.",
		Personality: "complicated",
		Scenario:    "You meet her.",
		FirstMes:    `"Hello."`,
	}
	p := Persona{Name: "Wren"}
	hist := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: ReviseOpening(weak)},
		{Role: ollama.RoleUser, Content: "She's a locksmith who lies about where she learned it. Give her a real voice and a body, she has neither."},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	// The interview turn, then the extraction, as the app does it.
	no := false
	reply, _, err := client.Chat(ctx, model,
		append([]ollama.Message{{Role: ollama.RoleSystem, Content: ReviseSystem(weak, p)}}, hist...),
		ollama.Options{NumCtx: 8192, Temperature: 0.5, NumPredict: 400}, &no, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, said := ollama.SplitThinking(reply.Content)
	hist = append(hist,
		ollama.Message{Role: ollama.RoleAssistant, Content: strings.TrimSpace(said)},
		ollama.Message{Role: ollama.RoleUser, Content: "Good. Write her."})

	written, err := BuildFromConversation(ctx, client, model, hist, ollama.Options{NumCtx: 8192})
	if err != nil {
		t.Fatal(err)
	}
	out := Revise(weak, written)
	t.Logf("appearance: %q", out.Appearance)
	t.Logf("speech:     %q", out.Speech)
	if strings.TrimSpace(out.Appearance) == "" {
		t.Error("the revision left appearance empty")
	}
	if strings.TrimSpace(out.Speech) == "" {
		t.Error("the revision left the voice empty")
	}
}
