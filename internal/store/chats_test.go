package store

import "testing"

// The per-scene direction and the style it was written under both survive a
// round trip. Both are read back on every turn to decide what the prompt says,
// so a column that silently fails to persist would show up as the feature
// quietly not working rather than as an error.
func TestChatNoteAndStyleRoundTrip(t *testing.T) {
	s := openTest(t)
	ch, err := s.NewChat(0, "A scene", "m", KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := s.Chat(ch.ID); err != nil {
		t.Fatal(err)
	} else if got.Note != "" || got.StyleName != "" {
		t.Errorf("a new chat starts with note=%q style=%q, want both empty", got.Note, got.StyleName)
	}

	const note = "{{char}} is about to work out that {{user}} lied."
	if err := s.SetChatNote(ch.ID, note); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChatStyle(ch.ID, "Clipped"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Chat(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != note {
		t.Errorf("note = %q, want %q", got.Note, note)
	}
	if got.StyleName != "Clipped" {
		t.Errorf("style = %q, want %q", got.StyleName, "Clipped")
	}

	// Clearing has to actually clear: a direction is meant to be finished with.
	if err := s.SetChatNote(ch.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Chat(ch.ID); err != nil {
		t.Fatal(err)
	} else if got.Note != "" {
		t.Errorf("note = %q after clearing, want empty", got.Note)
	}
}
