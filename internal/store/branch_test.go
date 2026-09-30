package store

import (
	"fmt"
	"strings"
	"testing"

	"astral/internal/chars"
)

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

// A continuation carries the pinned moments and the last messages, with the
// record standing for the rest, and its transcript starts at the carried
// messages.
func TestContinueChat(t *testing.T) {
	s := openTest(t)
	ch, err := s.NewChat(0, "The tide came in early", "m", KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := 1; i <= 10; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		id, err := s.AddMessage(Message{ChatID: ch.ID, Role: role, Content: fmt.Sprintf("line %d", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, i := range []int{1, 8} { // line 2, before the carried part, and line 9, inside it
		if err := s.SetMessagePinned(ids[i], true); err != nil {
			t.Fatal(err)
		}
	}
	next, err := s.ContinueChat(ch.ID, ids[6], "The story so far.", ContinueTitle(ch.Title))
	if err != nil {
		t.Fatal(err)
	}
	if next.Title != "The tide came in early, Part 2" || next.Summary != "The story so far." {
		t.Errorf("title %q, record %q", next.Title, next.Summary)
	}
	shown, err := s.MessagesAfter(next.ID, next.SummaryUpto)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range shown {
		got = append(got, m.Content)
	}
	if strings.Join(got, ",") != "line 7,line 8,line 9,line 10" {
		t.Errorf("the transcript starts with %v, want lines 7 to 10", got)
	}
	pins, err := s.Pinned(next.ID, next.SummaryUpto)
	if err != nil || len(pins) != 1 || pins[0].Content != "line 2" {
		t.Errorf("recalled pins %v, %v; want line 2", pins, err)
	}
	all, _ := s.Messages(next.ID)
	if len(all) != 5 || !all[3].Pinned {
		t.Errorf("copied %d messages, want 5, with line 9 still pinned", len(all))
	}
	// The original is untouched.
	if old, _ := s.Messages(ch.ID); len(old) != 10 {
		t.Errorf("the original has %d messages", len(old))
	}
	for in, want := range map[string]string{"": "Part 2", "Night, Part 2": "Night, Part 3", "Night": "Night, Part 2"} {
		if got := ContinueTitle(in); got != want {
			t.Errorf("ContinueTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// A continuation's record survives being branched, though its bookmark sits
// on no turn of its own; and a branch from before the end starts without the
// end's setting and state.
func TestBranchCarriesTheRecordAndNotTheEnd(t *testing.T) {
	s := openTest(t)
	ch, _ := s.NewChat(0, "Harbour", "m", KindRoleplay)
	var ids []int64
	for i := 1; i <= 8; i++ {
		id, _ := s.AddMessage(Message{ChatID: ch.ID, Role: "user", Content: fmt.Sprintf("line %d", i)})
		ids = append(ids, id)
	}
	next, err := s.ContinueChat(ch.ID, ids[5], "The story so far.", "Harbour, Part 2")
	if err != nil {
		t.Fatal(err)
	}
	carried, _ := s.Messages(next.ID)
	b, err := s.BranchChat(next.ID, carried[0].ID, "branch")
	if err != nil {
		t.Fatal(err)
	}
	if b.Summary != "The story so far." {
		t.Errorf("the branch of a continuation lost its record: %q", b.Summary)
	}
	if shown, _ := s.MessagesAfter(b.ID, b.SummaryUpto); len(shown) != 1 {
		t.Errorf("the branch shows %d turns after its record, want 1", len(shown))
	}

	if err := s.SetChatSetting(ch.ID, "The ferry at dawn"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChatState(ch.ID, chars.SceneState{Holding: "the brass key"}); err != nil {
		t.Fatal(err)
	}
	early, _ := s.BranchChat(ch.ID, ids[2], "early")
	if early.Setting != "" || !early.State.Empty() {
		t.Errorf("a branch from before the end took the end's setting %q and state %+v", early.Setting, early.State)
	}
	late, _ := s.BranchChat(ch.ID, ids[7], "late")
	if late.Setting != "The ferry at dawn" || late.State.Holding != "the brass key" {
		t.Errorf("a branch from the end lost its setting %q and state %+v", late.Setting, late.State)
	}
	// A setting you keep yourself is not Astral's to clear.
	if err := s.SetChatSettingAuto(ch.ID, false); err != nil {
		t.Fatal(err)
	}
	mine, _ := s.BranchChat(ch.ID, ids[2], "mine")
	if mine.Setting != "The ferry at dawn" || mine.State.Holding != "the brass key" || mine.SettingAuto {
		t.Errorf("a branch cleared a setting written by hand: %q, %+v, auto %v", mine.Setting, mine.State, mine.SettingAuto)
	}
}
