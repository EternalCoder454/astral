package scene

import (
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

func trio() []chars.Character {
	return []chars.Character{
		{ID: 1, Name: "Vesper", Description: "A cartographer."},
		{ID: 2, Name: "Kestrel", Description: "A courier."},
		{ID: 3, Name: "Ash", Description: "A clerk."},
	}
}

// TestBuildForLeavesATwoHanderAlone is the one that matters most. Every scene
// that already exists has one character in it, and if the prompt changes by a
// byte then every one of them re-reads its whole context the first time it is
// reopened. The assertion is equality with what Build produces, not similarity.
func TestBuildForLeavesATwoHanderAlone(t *testing.T) {
	cfg := store.Config{PersonaName: "Wren", NumCtx: 8192}
	ch := store.Chat{ID: 7, Kind: store.KindRoleplay}
	ca := chars.Character{ID: 1, Name: "Vesper", Description: "A cartographer."}
	hist := []ollama.Message{
		{Role: ollama.RoleUser, Content: "Hello."},
		{Role: ollama.RoleAssistant, Content: "*She did not look up.*"},
		{Role: ollama.RoleUser, Content: "Still here."},
	}

	want := Build(nil, cfg, ch, ca, hist)
	for _, cast := range [][]chars.Character{
		{ca},
		nil,
	} {
		got := BuildFor(nil, cfg, ch, cast, hist)
		if cast == nil {
			// No character at all is the assistant framing, which Build also
			// produces from a nameless character.
			want = Build(nil, cfg, ch, chars.Character{}, hist)
		}
		if len(got) != len(want) {
			t.Fatalf("cast of %d gave %d messages, want %d", len(cast), len(got), len(want))
		}
		for i := range want {
			if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
				t.Errorf("cast of %d, message %d differs:\n got %s %q\nwant %s %q",
					len(cast), i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
			}
		}
	}
}

func TestBuildForUsesTheGroupFramingWithACast(t *testing.T) {
	cfg := store.Config{PersonaName: "Wren", NumCtx: 8192}
	ch := store.Chat{ID: 7, Kind: store.KindRoleplay}
	msgs := BuildFor(nil, cfg, ch, trio(), []ollama.Message{
		{Role: ollama.RoleUser, Content: "Hello."},
	})
	if len(msgs) < 2 {
		t.Fatalf("got %d messages", len(msgs))
	}
	sys := msgs[0].Content
	for _, want := range []string{"Vesper", "Kestrel", "Ash"} {
		if !strings.Contains(sys, want) {
			t.Errorf("the system prompt never mentions %q", want)
		}
	}
	if !strings.Contains(sys, "Not everyone speaks every turn") {
		t.Error("the group framing is missing from a group prompt")
	}
}

func TestBuildForIgnoresACastOnAChatThatCannotHaveOne(t *testing.T) {
	cfg := store.Config{PersonaName: "Wren"}
	for _, kind := range []string{store.KindDesigner, store.KindStyleDesigner, store.KindAssistant} {
		msgs := BuildFor(nil, cfg, store.Chat{ID: 1, Kind: kind}, trio(), nil)
		if len(msgs) == 0 {
			t.Fatalf("%s built nothing", kind)
		}
		if strings.Contains(msgs[0].Content, "Not everyone speaks every turn") {
			t.Errorf("%s was turned into a roleplay by a stray cast row", kind)
		}
	}
}

func TestHistoryPutsTheNamesBackOn(t *testing.T) {
	names := map[int64]string{1: "Vesper", 2: "Kestrel"}
	nameOf := func(id int64) string { return names[id] }

	got := History([]store.Message{
		{Role: ollama.RoleUser, Content: "Hello."},
		{Role: ollama.RoleAssistant, Content: "You're late.", CharacterID: 1},
		{Role: ollama.RoleAssistant, Content: "Told you.", CharacterID: 2},
		{Role: ollama.RoleUser, Content: "Both of you, stop."},
	}, nameOf)

	if len(got) != 3 {
		t.Fatalf("got %d turns, want 3: the two beats are one reply", len(got))
	}
	if got[1].Role != ollama.RoleAssistant {
		t.Fatalf("turn 1 is a %s", got[1].Role)
	}
	if !strings.Contains(got[1].Content, "Vesper: You're late.") {
		t.Errorf("the first beat lost its label: %q", got[1].Content)
	}
	if !strings.Contains(got[1].Content, "Kestrel: Told you.") {
		t.Errorf("the second beat is missing or unlabelled: %q", got[1].Content)
	}
}

// TestHistoryRoundTripsThroughTheSplitter is the loop the scene actually makes:
// stored beats go out labelled, and what comes back has to split into the same
// beats. A disagreement here silently reattributes a scene.
func TestHistoryRoundTripsThroughTheSplitter(t *testing.T) {
	names := map[int64]string{1: "Vesper", 2: "Kestrel"}
	stored := []store.Message{
		{Role: ollama.RoleAssistant, Content: "*She did not look up.* \"You're late.\"", CharacterID: 1},
		{Role: ollama.RoleAssistant, Content: "\"Told you.\"", CharacterID: 2},
	}
	out := History(stored, func(id int64) string { return names[id] })
	if len(out) != 1 {
		t.Fatalf("got %d turns, want the two beats merged into one", len(out))
	}
	beats := chars.SplitBeats(out[0].Content, []string{"Vesper", "Kestrel"})
	if len(beats) != 2 {
		t.Fatalf("split back into %d beats, want 2: %#v", len(beats), beats)
	}
	if beats[0].Name != "Vesper" || beats[0].Text != stored[0].Content {
		t.Errorf("first beat came back as %q by %q", beats[0].Text, beats[0].Name)
	}
	if beats[1].Name != "Kestrel" || beats[1].Text != stored[1].Content {
		t.Errorf("second beat came back as %q by %q", beats[1].Text, beats[1].Name)
	}
}

func TestHistoryLeavesATwoHanderUnlabelled(t *testing.T) {
	// A scene with one character records no speaker, so nothing is added and the
	// prompt is the one it has always been.
	got := History([]store.Message{
		{Role: ollama.RoleUser, Content: "Hello."},
		{Role: ollama.RoleAssistant, Content: "*She did not look up.*"},
	}, nil)
	if len(got) != 2 {
		t.Fatalf("got %d turns, want 2", len(got))
	}
	if got[1].Content != "*She did not look up.*" {
		t.Errorf("a two-hander's reply was changed to %q", got[1].Content)
	}
}

func TestHistorySkipsEmptyTurns(t *testing.T) {
	got := History([]store.Message{
		{Role: ollama.RoleUser, Content: "  "},
		{Role: ollama.RoleAssistant, Content: "Here."},
	}, nil)
	if len(got) != 1 || got[0].Content != "Here." {
		t.Errorf("got %#v, want the one turn with anything in it", got)
	}
}

func TestGroupBudgetAccountsForEveryCard(t *testing.T) {
	cfg := store.Config{NumCtx: 8192}
	p := chars.Persona{Name: "Wren"}
	cast := trio()
	for i := range cast {
		cast[i].Description = strings.Repeat("a long description. ", 60)
	}
	group := GroupBudget(cfg, cast, p)
	solo := Budget(cfg, cast[:1][0], p)
	if group.History >= solo.History {
		t.Errorf("three cards left %d characters for the transcript and one left %d: "+
			"the extra cards have to come out of the transcript, not out of the window",
			group.History, solo.History)
	}
}
