package scene

import (
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// The recap is the whole point of compacting a plain conversation, and it only
// works if it is sent. Without this the turns it replaced would be dropped from
// the history and the record of them would go nowhere, which is worse than the
// bug it fixes.
func TestPlainCarriesItsRecap(t *testing.T) {
	cfg := store.Config{PersonaName: "Wren"}
	ch := store.Chat{ID: 3, Kind: store.KindAssistant, Summary: "Chose SQLite over a flat file."}
	hist := []ollama.Message{{Role: ollama.RoleUser, Content: "Where were we?"}}

	msgs := Build(nil, cfg, ch, chars0(), hist)
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "Chose SQLite over a flat file.") {
			if m.Role != ollama.RoleSystem {
				t.Errorf("the recap arrived as a %s turn, not as notes", m.Role)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("the recap of a plain conversation is never sent, so compacting one loses it")
	}
	// And it comes before the turns it precedes, so the conversation reads in
	// order.
	if msgs[len(msgs)-1].Content != "Where were we?" {
		t.Errorf("the live turn is not last: %q", msgs[len(msgs)-1].Content)
	}
}

func TestPlainCarriesTheUserAndTheirRules(t *testing.T) {
	cfg := store.Config{
		PersonaName:        "Wren",
		PersonaDescription: "A courier who reads the messages.",
		Rulebook:           []store.Rule{{Text: "British spelling.", Enabled: true}},
	}
	msgs := Build(nil, cfg, store.Chat{ID: 1, Kind: store.KindAssistant}, chars0(), nil)
	sys := msgs[0].Content
	for _, want := range []string{"Wren", "A courier who reads the messages.", "British spelling."} {
		if !strings.Contains(sys, want) {
			t.Errorf("a general chat never hears %q", want)
		}
	}
}

// And the designers do not get the user's rules. Their product has to parse, and
// a rule about how prose should read would break an interview.
func TestDesignersDoNotGetTheRules(t *testing.T) {
	cfg := store.Config{Rulebook: []store.Rule{{Text: "Every reply is one paragraph.", Enabled: true}}}
	for _, kind := range []string{store.KindDesigner, store.KindStyleDesigner} {
		msgs := Build(nil, cfg, store.Chat{ID: 1, Kind: kind}, chars0(), nil)
		if strings.Contains(msgs[0].Content, "Every reply is one paragraph.") {
			t.Errorf("%s was given the user's standing rules", kind)
		}
	}
}

func TestPlainBudgetAccountsForTheFraming(t *testing.T) {
	bare := PlainBudget(store.Config{NumCtx: 8192})
	loaded := PlainBudget(store.Config{
		NumCtx:             8192,
		PersonaDescription: strings.Repeat("a long description. ", 80),
		Rulebook:           []store.Rule{{Text: strings.Repeat("a long rule. ", 40), Enabled: true}},
	})
	if loaded.History >= bare.History {
		t.Errorf("a persona and a rulebook left %d characters for the conversation and an empty one left %d: "+
			"the framing has to come out of the transcript", loaded.History, bare.History)
	}
}

// chars0 is the absent character a plain conversation has.
func chars0() chars.Character { return chars.Character{} }
