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

// Correcting a turn has to survive: the transcript is the prompt, so an edit
// that is only on screen fixes nothing about the next reply.
func TestSetMessageContent(t *testing.T) {
	s := openTest(t)
	ch, err := s.NewChat(0, "scene", "m", KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddMessage(Message{ChatID: ch.ID, Role: "assistant", Content: "She sat down abruptly."})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.AddMessage(Message{ChatID: ch.ID, Role: "user", Content: "untouched"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMessageContent(id, "*She sat down slowly.*"); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.Messages(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages", len(msgs))
	}
	if msgs[0].Content != "*She sat down slowly.*" {
		t.Errorf("the edit did not stick: %q", msgs[0].Content)
	}
	if msgs[1].ID != other || msgs[1].Content != "untouched" {
		t.Errorf("editing one turn changed another: %+v", msgs[1])
	}
	// And the order is unchanged, so an edited turn does not jump to the end.
	if msgs[0].ID != id {
		t.Errorf("the edited turn moved: first is %d, want %d", msgs[0].ID, id)
	}
}
