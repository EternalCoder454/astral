package store

import (
	"testing"

	"astral/internal/ollama"
)

func TestMomentsFindsTheDetailTheRecapDropped(t *testing.T) {
	s := openTest(t)
	ch, err := s.NewChat(0, "A scene", "m", KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	add := func(role, text string) int64 {
		id, err := s.AddMessage(Message{ChatID: ch.ID, Role: role, Content: text})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	promise := add(ollama.RoleAssistant, `*She pressed the brass key into your palm.* "The Gannet sails at dawn. Promise me you will be on it, whatever happens tonight."`)
	add(ollama.RoleUser, "I nod.")
	add(ollama.RoleAssistant, `*She turned back to the chart and did not look up again for a long time, the rain doing all the talking.*`)
	upto := add(ollama.RoleUser, `"What about the storm?" I ask, watching the harbour lights.`)
	later := add(ollama.RoleAssistant, `"The Gannet has sailed through worse." *She said it too quickly.*`)

	got, err := s.Moments(ch.ID, upto, "Is the Gannet still sailing at dawn?", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != promise {
		t.Fatalf("want the promise first, got %+v", got)
	}
	for _, m := range got {
		if m.ID > upto {
			t.Errorf("recalled message %d, which the model can still see", m.ID)
		}
		if len(m.Content) < minMomentChars {
			t.Errorf("recalled a line too short to be worth it: %q", m.Content)
		}
	}
	_ = later
}

func TestTheIndexFollowsEditsAndDeletes(t *testing.T) {
	s := openTest(t)
	ch, _ := s.NewChat(0, "A scene", "m", KindRoleplay)
	id, _ := s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleAssistant,
		Content: "The lighthouse keeper is called Marius, and he has not left the rock in eleven years."})
	if err := s.SetMessageContent(id, "The lighthouse keeper is called Odile, and she has not left the rock in eleven years."); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Moments(ch.ID, id, "Marius", 4); len(got) != 0 {
		t.Errorf("an edited-away name is still found: %+v", got)
	}
	if got, _ := s.Moments(ch.ID, id, "Odile", 4); len(got) != 1 {
		t.Errorf("the edit was not indexed: %+v", got)
	}
	if err := s.DeleteMessage(id); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Moments(ch.ID, id, "Odile", 4); len(got) != 0 {
		t.Errorf("a deleted message is still found: %+v", got)
	}
}

func TestMomentsStayInTheirOwnChat(t *testing.T) {
	s := openTest(t)
	a, _ := s.NewChat(0, "A", "m", KindRoleplay)
	b, _ := s.NewChat(0, "B", "m", KindRoleplay)
	id, _ := s.AddMessage(Message{ChatID: a.ID, Role: ollama.RoleAssistant,
		Content: "The Gannet sails at dawn, and nobody on board knows what is in the hold."})
	if got, _ := s.Moments(b.ID, id+10, "Gannet dawn", 4); len(got) != 0 {
		t.Errorf("another scene's message was recalled: %+v", got)
	}
}

func TestAScenePlayedBeforeTheIndexIsIndexedOnce(t *testing.T) {
	s := openTest(t)
	ch, _ := s.NewChat(0, "Old", "m", KindRoleplay)
	id, _ := s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleAssistant,
		Content: "The cartographer's guild expelled her for charting east of the Sever, and she has never said why she did it."})
	// As a database from before this version would be: messages, no index.
	for _, q := range []string{
		`DROP TRIGGER messages_fts_insert`, `DROP TRIGGER messages_fts_delete`,
		`DROP TRIGGER messages_fts_update`, `DROP TABLE messages_fts`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.migrateMemory()
	if got, _ := s.Moments(ch.ID, id, "why was she expelled from the guild", 4); len(got) != 1 {
		t.Errorf("the older scene was not indexed: %+v", got)
	}
}
