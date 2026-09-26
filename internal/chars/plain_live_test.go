package chars

import (
	"context"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// The plain recap prompt asks for different things from the roleplay one: what
// was decided and what is still open, rather than who is standing where. Whether
// it gets them is a question only a model can answer.
//
// The conversation below is seeded with specifics that cannot be reconstructed
// from a general summary — a version, a path, a number, a reversal — because
// those are exactly what a summariser drops when it is trying to be concise.
func TestLivePlainRecapKeepsTheSpecifics(t *testing.T) {
	client, model := liveModel(t)

	aged := []ollama.Message{
		{Role: ollama.RoleUser, Content: "I need to pick a store for an index that has to survive a crash. Flat file or SQLite?"},
		{Role: ollama.RoleAssistant, Content: "SQLite, if durability matters. A flat file needs you to implement atomic replace and fsync yourself."},
		{Role: ollama.RoleUser, Content: "Go with SQLite then. I'm on modernc.org/sqlite v1.52.0, no cgo."},
		{Role: ollama.RoleAssistant, Content: "That works. Set synchronous to full for an authoritative store."},
		{Role: ollama.RoleUser, Content: "Done. The db lives at ~/.local/share/astral/astral.db. Max open conns is 1."},
		{Role: ollama.RoleAssistant, Content: "One connection avoids the writer lock contention entirely. Sensible."},
		{Role: ollama.RoleUser, Content: "Actually scrap synchronous full for the notes index, normal is fine there. Keep full for the chat log."},
		{Role: ollama.RoleAssistant, Content: "Understood: normal for the index, full for the log."},
		{Role: ollama.RoleUser, Content: "Still open: whether to quarantine a corrupt db or rebuild it. Don't know yet."},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p := Persona{Name: "Wren"}
	got, err := CompactPlain(ctx, client, model, "", aged, p,
		ollama.Options{NumCtx: 8192}, Plan(8192, 0, 400))
	if err != nil {
		t.Fatalf("compacting failed: %v", err)
	}
	t.Logf("model %s recorded:\n%s", model, got)

	low := strings.ToLower(got)
	// The specifics. Each of these is a thing that cannot be recovered from a
	// summary that dropped it.
	for _, want := range []string{"sqlite", "1.52.0", "astral.db"} {
		if !strings.Contains(low, strings.ToLower(want)) {
			t.Errorf("the record lost %q, which cannot be reconstructed from it", want)
		}
	}
	// The reversal. A record that keeps the original and not the correction is
	// worse than no record, because the next turn acts on the wrong one.
	if !strings.Contains(low, "normal") {
		t.Error("the record lost the correction, so the next turn would use the value that was withdrawn")
	}
	// And the open question, which is the thing a conversation is resumed to
	// settle.
	if !strings.Contains(low, "quarantin") && !strings.Contains(low, "rebuild") {
		t.Error("the record lost the one question that was still open")
	}
	if len(got)*2 >= totalChars(aged) {
		t.Errorf("the record is %d characters against %d of conversation, which saves nothing",
			len(got), totalChars(aged))
	}
}
