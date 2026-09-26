package chars

import (
	"context"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// Whether a revision improves a card is not something the prompt can answer. The
// failure that matters is the polite one: a model handed a card and asked to help
// spends its replies saying how good it already is, and the card comes back with
// the same sentences in a different order.
//
// The card below has the usual problem on purpose. It is all adjectives, which is
// the thing a model cannot act on.
func TestLiveReviseImprovesAWeakCard(t *testing.T) {
	client, model := liveModel(t)

	weak := Character{
		ID:          9,
		Name:        "Mara",
		Description: "Mysterious. Complex. Interesting. A woman with a past.",
		Personality: "complicated",
		Scenario:    "You meet her.",
		FirstMes:    "\"Hello.\"",
	}
	p := Persona{Name: "Wren"}

	// The interview, of the shape one actually has.
	history := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: ReviseOpening(weak)},
		{Role: ollama.RoleUser, Content: "What do you think is weakest about her?"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	no := false
	critique, _, err := client.Chat(ctx, model, append(
		[]ollama.Message{{Role: ollama.RoleSystem, Content: ReviseSystem(weak, p)}}, history...),
		ollama.Options{NumCtx: 8192, Temperature: 0.4, NumPredict: 400}, &no, nil)
	if err != nil {
		t.Fatalf("the interview failed: %v", err)
	}
	_, said := ollama.SplitThinking(critique.Content)
	t.Logf("the designer said:\n%s", strings.TrimSpace(said))

	// It has to criticise rather than compliment. This is the whole reason the
	// prompt tells it to open with what is weakest.
	low := strings.ToLower(said)
	criticises := false
	for _, w := range []string{"adjective", "vague", "generic", "abstract", "tell us nothing",
		"not much to", "nothing to act", "no specific", "cliché", "cliche", "weak"} {
		if strings.Contains(low, w) {
			criticises = true
			break
		}
	}
	if !criticises {
		t.Errorf("the designer did not say what was wrong with an all-adjectives card:\n%s", said)
	}

	// And the card it then writes has to be better: concrete, and not the same
	// words back.
	history = append(history,
		ollama.Message{Role: ollama.RoleAssistant, Content: said},
		ollama.Message{Role: ollama.RoleUser, Content: "Agreed. Make her a locksmith who lies about where she learned it, and write her properly."})

	written, err := BuildFromConversation(ctx, client, model, history, ollama.Options{NumCtx: 8192})
	if err != nil {
		t.Fatalf("writing the card failed: %v", err)
	}
	revised := Revise(weak, written)
	t.Logf("revised description: %s", revised.Description)

	if revised.ID != weak.ID {
		t.Errorf("the revision lost the character's row: %d", revised.ID)
	}
	if strings.EqualFold(strings.TrimSpace(revised.Description), strings.TrimSpace(weak.Description)) {
		t.Error("the description came back unchanged")
	}
	if len(revised.Description) < len(weak.Description) {
		t.Errorf("the description got shorter, which for this card means less to act on: %q", revised.Description)
	}
	// The thing that was asked for has to be in it.
	if !strings.Contains(strings.ToLower(revised.Description+" "+revised.Personality), "locksmith") {
		t.Errorf("what was asked for is not in the card: %q", revised.Description)
	}
}
