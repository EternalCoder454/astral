package store

import "testing"

func TestBranchChat(t *testing.T) {
	s := openTest(t)
	ch, err := s.NewChat(0, "Harbour", "m", KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	s.SetChatNote(ch.ID, "move them to the docks")
	var ids []int64
	for i, text := range []string{"one", "two", "three", "four", "five"} {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		id, err := s.AddMessage(Message{ChatID: ch.ID, Role: role, Content: text})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	s.SetMessagePinned(ids[1], true)
	s.SetMessagePinned(ids[4], true)
	s.SetChatSummary(ch.ID, "they met", ids[1])

	b, err := s.BranchChat(ch.ID, ids[2], BranchTitle("Harbour"))
	if err != nil {
		t.Fatal(err)
	}
	if b.ID == ch.ID || b.Title != "Harbour (Branch)" || b.Note != "move them to the docks" || b.Model != "m" {
		t.Fatalf("branch is %+v", b)
	}
	msgs, _ := s.Messages(b.ID)
	if len(msgs) != 3 || msgs[2].Content != "three" {
		t.Fatalf("branch has %d messages, last %q", len(msgs), msgs[len(msgs)-1].Content)
	}
	if !msgs[1].Pinned || msgs[0].Pinned {
		t.Fatal("the pin on the second message did not come across")
	}
	if b.Summary != "they met" || b.SummaryUpto != msgs[1].ID {
		t.Fatalf("recap %q up to %d, want up to %d", b.Summary, b.SummaryUpto, msgs[1].ID)
	}
	// The original is untouched.
	if orig, _ := s.Messages(ch.ID); len(orig) != 5 {
		t.Fatalf("the original lost messages: %d", len(orig))
	}

	// A recap reaching past the branch point is not carried.
	s.SetChatSummary(ch.ID, "they met and fought", ids[3])
	b2, err := s.BranchChat(ch.ID, ids[2], BranchTitle(b.Title))
	if err != nil {
		t.Fatal(err)
	}
	if b2.Summary != "" || b2.SummaryUpto != 0 || b2.Title != "Harbour (Branch)" {
		t.Fatalf("second branch %+v", b2)
	}

	pins, err := s.Pinned(ch.ID, ids[3])
	if err != nil || len(pins) != 1 || pins[0].ID != ids[1] {
		t.Fatalf("pins before the fourth message: %v %v", pins, err)
	}
	if _, err := s.BranchChat(ch.ID, 999999, "x"); err == nil {
		t.Fatal("branching at a message that is not there should fail")
	}
}
