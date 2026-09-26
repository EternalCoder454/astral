package store

import (
	"testing"

	"astral/internal/chars"
)

func castFixture(t *testing.T, s *Store, names ...string) []int64 {
	t.Helper()
	ids := make([]int64, 0, len(names))
	for _, n := range names {
		id, err := s.SaveCharacter(chars.Character{Name: n})
		if err != nil {
			t.Fatalf("saving %s: %v", n, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestCastKeepsTheOrderItWasGiven(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel", "Ash")
	ch, err := s.NewChat(0, "A scene", "", KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately not the order they were created in: cast order is the order
	// they are introduced to the model and where the opening comes from.
	want := []int64{ids[2], ids[0], ids[1]}
	if err := s.SetCast(ch.ID, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Cast(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d members, want 3", len(got))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("member %d is %d, want %d", i, got[i].ID, id)
		}
	}
}

func TestCastCapsAtTheLimit(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "A", "B", "C", "D", "E", "F", "G")
	ch, _ := s.NewChat(0, "A crowd", "", KindRoleplay)
	if err := s.SetCast(ch.ID, ids); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Cast(ch.ID)
	if len(got) != MaxCast {
		t.Errorf("stored %d members, want the cap of %d", len(got), MaxCast)
	}
}

func TestCastIgnoresRepeats(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel")
	ch, _ := s.NewChat(0, "A scene", "", KindRoleplay)
	if err := s.SetCast(ch.ID, []int64{ids[0], ids[0], ids[1], ids[1]}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Cast(ch.ID)
	if len(got) != 2 {
		t.Errorf("got %d members, want 2: a character cannot be in a scene twice", len(got))
	}
}

// TestCastSetsTheChatsCharacter is what keeps every existing screen working: the
// sidebar's name and tint, the window title and reopening a scene all read
// chats.character_id, and none of them knows what a cast is.
func TestCastSetsTheChatsCharacter(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel")
	ch, _ := s.NewChat(0, "A scene", "", KindRoleplay)
	if err := s.SetCast(ch.ID, ids); err != nil {
		t.Fatal(err)
	}
	again, err := s.Chat(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.CharacterID != ids[0] {
		t.Errorf("the chat's character is %d, want the first of the cast (%d)", again.CharacterID, ids[0])
	}
	if again.CharacterName != "Vesper" {
		t.Errorf("the chat joined to %q, want Vesper", again.CharacterName)
	}
}

func TestCastIsReplacedNotAddedTo(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel", "Ash")
	ch, _ := s.NewChat(0, "A scene", "", KindRoleplay)
	if err := s.SetCast(ch.ID, ids); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCast(ch.ID, ids[:2]); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Cast(ch.ID)
	if len(got) != 2 {
		t.Errorf("got %d members after narrowing the cast, want 2", len(got))
	}
}

func TestCastGoesWithTheChat(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel")
	ch, _ := s.NewChat(0, "A scene", "", KindRoleplay)
	if err := s.SetCast(ch.ID, ids); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChat(ch.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Cast(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("%d cast rows outlived their chat", len(got))
	}
}

func TestMessagesRememberWhoSpoke(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel")
	ch, _ := s.NewChat(0, "A scene", "", KindRoleplay)
	if _, err := s.AddMessage(Message{
		ChatID: ch.ID, Role: "assistant", Content: "You're late.", CharacterID: ids[0],
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMessage(Message{
		ChatID: ch.ID, Role: "assistant", Content: "Told you.", CharacterID: ids[1],
	}); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.Messages(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].CharacterID != ids[0] || msgs[1].CharacterID != ids[1] {
		t.Errorf("speakers came back as %d and %d, want %d and %d",
			msgs[0].CharacterID, msgs[1].CharacterID, ids[0], ids[1])
	}
	// And the content is stored without a name glued to the front of it, so
	// renaming a character renames their old lines too.
	if msgs[0].Content != "You're late." {
		t.Errorf("stored content is %q, want the prose alone", msgs[0].Content)
	}
}

func TestCastCountsIsOneQueryForEveryChat(t *testing.T) {
	s := openTest(t)
	ids := castFixture(t, s, "Vesper", "Kestrel", "Ash")
	group, _ := s.NewChat(0, "A group", "", KindRoleplay)
	solo, _ := s.NewChat(ids[0], "A two-hander", "", KindRoleplay)
	if err := s.SetCast(group.ID, ids); err != nil {
		t.Fatal(err)
	}
	counts, err := s.CastCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts[group.ID] != 3 {
		t.Errorf("the group counted %d, want 3", counts[group.ID])
	}
	if n, ok := counts[solo.ID]; ok {
		t.Errorf("a scene with one character should have no cast rows, counted %d", n)
	}
}
