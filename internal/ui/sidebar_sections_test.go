package ui

import (
	"reflect"
	"testing"
	"time"

	"astral/internal/store"
)

func chatAt(id, character int64, name string, ago time.Duration) store.Chat {
	return store.Chat{ID: id, CharacterID: character, CharacterName: name,
		UpdatedAt: time.Now().Add(-ago)}
}

func idsOf(sec chatSection) []int64 {
	var ids []int64
	for _, ch := range sec.Chats {
		ids = append(ids, ch.ID)
	}
	return ids
}

// Grouped by character, a section is a character, the sections run in the
// order of each one's latest chat, and a chat with nobody in it goes to the
// end whatever its age.
func TestSectionsByCharacter(t *testing.T) {
	chats := []store.Chat{
		chatAt(1, 0, "", time.Minute),         // no character, and the newest of all
		chatAt(2, 20, "Mira", 2*time.Minute),  // Mira's latest
		chatAt(3, 10, "Aldo", 3*time.Minute),  // Aldo's latest
		chatAt(4, 20, "Mira", 4*time.Minute),  // an older Mira chat
		chatAt(5, 10, "Aldo", 5*time.Minute),  // an older Aldo chat
		chatAt(6, 30, "Mira", 6*time.Minute),  // another character with the same name
		chatAt(7, 40, "", 7*time.Minute),      // a character that has since been deleted
		chatAt(8, 20, "Mira", 90*time.Second), // a scene whose lead is Mira, and newer than her others
	}
	// Handed over out of order on purpose.
	shuffled := []store.Chat{chats[4], chats[0], chats[7], chats[2], chats[6], chats[1], chats[5], chats[3]}

	for name, in := range map[string][]store.Chat{"in order": chats, "shuffled": shuffled} {
		got := sectionsByCharacter(in)
		var titles []string
		for _, sec := range got {
			titles = append(titles, sec.Title)
		}
		if want := []string{"Mira", "Aldo", "Mira", otherChats}; !reflect.DeepEqual(titles, want) {
			t.Fatalf("%s: section titles = %v, want %v", name, titles, want)
		}
		if want := []int64{8, 2, 4}; !reflect.DeepEqual(idsOf(got[0]), want) {
			t.Errorf("%s: Mira = %v, want %v", name, idsOf(got[0]), want)
		}
		if want := []int64{3, 5}; !reflect.DeepEqual(idsOf(got[1]), want) {
			t.Errorf("%s: Aldo = %v, want %v", name, idsOf(got[1]), want)
		}
		if want := []int64{6}; !reflect.DeepEqual(idsOf(got[2]), want) {
			t.Errorf("%s: the second Mira = %v, want %v", name, idsOf(got[2]), want)
		}
		if want := []int64{1, 7}; !reflect.DeepEqual(idsOf(got[3]), want) {
			t.Errorf("%s: %s = %v, want %v", name, otherChats, idsOf(got[3]), want)
		}
	}

	if got := sectionsByCharacter(nil); len(got) != 0 {
		t.Errorf("no chats gave %d sections", len(got))
	}
}

// The archive comes out of the list before either grouping sees it, and the
// two halves keep their order.
func TestSplitArchived(t *testing.T) {
	chats := []store.Chat{{ID: 1}, {ID: 2, Archived: true}, {ID: 3}, {ID: 4, Archived: true}}
	live, archived := splitArchived(chats)
	if len(live) != 2 || live[0].ID != 1 || live[1].ID != 3 {
		t.Errorf("live = %v", live)
	}
	if len(archived) != 2 || archived[0].ID != 2 || archived[1].ID != 4 {
		t.Errorf("archived = %v", archived)
	}
}

// Archiving a chat changes nothing else about it, so the list has to notice
// from the flag alone, or the chat would stay where it was until something
// else redrew the list.
func TestChatSignatureSeesArchive(t *testing.T) {
	chats := []store.Chat{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}}
	before := chatSignature(chats)
	chats[1].Archived = true
	if chatSignature(chats) == before {
		t.Error("archiving a chat did not change the list's signature")
	}
}
