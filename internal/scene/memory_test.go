package scene

import (
	"path/filepath"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

func memoryStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	st, _, err := store.Open(filepath.Join(dir, "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestALongSceneRecallsWhatItsRecapDropped(t *testing.T) {
	st := memoryStore(t)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper", Description: "A cartographer."})
	ch, _ := st.NewChat(id, "Scene", "m", store.KindRoleplay)
	add := func(role, text string) int64 {
		mid, err := st.AddMessage(store.Message{ChatID: ch.ID, Role: role, Content: text})
		if err != nil {
			t.Fatal(err)
		}
		return mid
	}
	add(ollama.RoleAssistant, `*She pressed the brass key into your palm.* "The Gannet sails at dawn. Promise me you will be on it, whatever happens tonight."`)
	upto := add(ollama.RoleUser, `"Tell me about the maps," I say, and she does, for an hour, until the candle gutters.`)
	// The scene was folded into a recap up to here.
	if err := st.SetChatSummary(ch.ID, "Wren arrived late. Vesper talked about maps.", upto); err != nil {
		t.Fatal(err)
	}
	ch, _ = st.Chat(ch.ID)
	hist := []ollama.Message{
		{Role: ollama.RoleAssistant, Content: `*The harbour bell rang twice.* "It is nearly light."`},
		{Role: ollama.RoleUser, Content: `"Is the Gannet still sailing at dawn?"`},
	}
	cfg := store.DefaultConfig()
	cfg.PersonaName = "Wren"
	ca, _ := st.Character(id)
	msgs := Build(st, cfg, ch, ca, hist)

	var block string
	for _, m := range msgs {
		if strings.HasPrefix(m.Content, chars.MemoryHeading) {
			block = m.Content
		}
	}
	if !strings.Contains(block, "Promise me you will be on it") {
		t.Fatalf("the promise was not recalled:\n%s", block)
	}
	if !strings.Contains(block, "Vesper: ") {
		t.Errorf("a recalled moment has no speaker:\n%s", block)
	}
	// It goes after the transcript and before the closing block, in the part
	// of the prompt that changes every turn anyway.
	last := msgs[len(msgs)-1].Content
	if !strings.HasPrefix(last, "[Before you write") {
		t.Errorf("the closing block is no longer last")
	}
}

func TestAShortSceneHasNothingToRecall(t *testing.T) {
	st := memoryStore(t)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper"})
	ch, _ := st.NewChat(id, "Scene", "m", store.KindRoleplay)
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant,
		Content: `"The Gannet sails at dawn. Promise me you will be on it, whatever happens tonight."`})
	ca, _ := st.Character(id)
	msgs := Build(st, store.DefaultConfig(), ch, ca, []ollama.Message{{Role: ollama.RoleUser, Content: "The Gannet?"}})
	for _, m := range msgs {
		if strings.HasPrefix(m.Content, chars.MemoryHeading) {
			t.Fatalf("a scene with no recap recalled something:\n%s", m.Content)
		}
	}
}

func TestExcerptCutsAtASentence(t *testing.T) {
	s := strings.Repeat("She turned the map over. ", 40)
	got := excerpt(s, 120)
	if len(got) > 130 || !strings.HasSuffix(got, ". …") {
		t.Errorf("got %q", got)
	}
	if excerpt("Short.", 120) != "Short." {
		t.Error("a short moment was changed")
	}
}
