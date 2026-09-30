package store

import (
	"testing"

	"astral/internal/world"
)

// When an entry is sent is stored with it, an entry that never set any of it
// is sent whenever it is triggered, and the model relearning an entry does not
// undo what was set.
func TestLoreSendingRulesRoundTrip(t *testing.T) {
	s := openTest(t)
	wid, _ := s.SaveWorld(world.World{Name: "Coast"})

	plain := world.Entry{WorldID: wid, Name: "Plain", Keys: []string{"plain"}, Content: "Nothing set.", Enabled: true}
	if _, err := s.SaveLoreEntry(plain); err != nil {
		t.Fatal(err)
	}
	set := world.Entry{WorldID: wid, Name: "Set", Keys: []string{"set"}, Content: "All set.", Enabled: true,
		Chance: 35, Wait: 12, Group: "  Rumours "}
	id, err := s.SaveLoreEntry(set)
	if err != nil {
		t.Fatal(err)
	}
	byName := func() map[string]world.Entry {
		entries, err := s.LoreEntries(wid)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]world.Entry{}
		for _, e := range entries {
			m[e.Name] = e
		}
		return m
	}
	got := byName()
	if e := got["Plain"]; e.Chance != 100 || e.Wait != 0 || e.Group != "" {
		t.Errorf("an entry with none set came back as %d, %d, %q", e.Chance, e.Wait, e.Group)
	}
	if e := got["Set"]; e.ID != id || e.Chance != 35 || e.Wait != 12 || e.Group != "Rumours" {
		t.Errorf("chance, wait and group came back as %d, %d, %q", e.Chance, e.Wait, e.Group)
	}

	// Edited again, by a person, they change.
	set.Chance, set.Wait, set.Group = 90, 0, ""
	if _, err := s.SaveLoreEntry(set); err != nil {
		t.Fatal(err)
	}
	if e := byName()["Set"]; e.Chance != 90 || e.Wait != 0 || e.Group != "" {
		t.Errorf("an edit came back as %d, %d, %q", e.Chance, e.Wait, e.Group)
	}

	// The model writing more about an entry it wrote leaves them alone.
	learned := world.Entry{WorldID: wid, Name: "Learned", Keys: []string{"learned"}, Content: "First.", Enabled: true, Auto: true,
		Chance: 20, Wait: 4, Group: "Guesses"}
	if _, err := s.SaveLoreEntry(learned); err != nil {
		t.Fatal(err)
	}
	learned.Content, learned.Chance, learned.Wait, learned.Group = "Second.", 0, 0, ""
	if _, err := s.SaveLoreEntry(learned); err != nil {
		t.Fatal(err)
	}
	if e := byName()["Learned"]; e.Content != "Second." || e.Chance != 20 || e.Wait != 4 || e.Group != "Guesses" {
		t.Errorf("relearning came back as %q, %d, %d, %q", e.Content, e.Chance, e.Wait, e.Group)
	}
}

func TestCountChatMessages(t *testing.T) {
	s := openTest(t)
	ch, err := s.NewChat(0, "Talk", "m", KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountChatMessages(ch.ID); err != nil || n != 0 {
		t.Fatalf("an empty chat has %d messages, err %v", n, err)
	}
	for _, role := range []string{"user", "assistant", "user"} {
		if _, err := s.AddMessage(Message{ChatID: ch.ID, Role: role, Content: "hello"}); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := s.CountChatMessages(ch.ID); n != 3 {
		t.Errorf("counted %d messages, want 3", n)
	}
	// A reply after the last message you wrote is not counted through it.
	if _, err := s.AddMessage(Message{ChatID: ch.ID, Role: "assistant", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountThroughLastUser(ch.ID); err != nil || n != 3 {
		t.Errorf("counted %d through the last message you wrote, err %v, want 3", n, err)
	}
}
