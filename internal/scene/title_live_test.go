package scene

import (
	"context"
	"testing"
	"time"
)

// A General Chat and a Novel Chat are named by the model after their first
// reply. Live: ASTRAL_TEST_MODEL names the model.
func TestLiveSuggestTitle(t *testing.T) {
	client, model := designModel(t)
	for _, c := range []struct{ first, reply string }{
		{"How do I keep sourdough starter alive while I'm away for two weeks?",
			"Feed it, then put it in the fridge. A starter kept cold slows right down and will be fine for two weeks."},
		{"A smuggler's daughter in a drowned city finds out her father sold her to the harbour guild.",
			"*The salt air bit into Miri's lungs as she crouched on the rusted pier of the Lower Sump, watching the Guild's skiffs.*"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		title, err := SuggestTitle(ctx, client, model, c.first, c.reply)
		cancel()
		if err != nil || title == "" {
			t.Fatalf("no title: %q %v", title, err)
		}
		t.Logf("%q -> %q", c.first[:30], title)
	}
}
