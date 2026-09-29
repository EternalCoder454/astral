package store

import (
	"strings"
	"testing"

	"astral/internal/chars"
)

func TestMessageVectorsRoundTripAndFollowTheirMessage(t *testing.T) {
	st := openTest(t)
	cid, err := st.SaveCharacter(chars.Character{Name: "Vesper"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := st.NewChat(cid, "Scene", "m", KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}
	long := func(s string) string { return s + strings.Repeat(" and on it went", 6) }
	add := func(text string) int64 {
		id, err := st.AddMessage(Message{ChatID: ch.ID, Role: "assistant", Content: text})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b, c := add(long("first")), add(long("second")), add(long("third"))
	add("I nod.")

	// Oldest first, and a one-line message is not worth a vector.
	pend, err := st.MessagesWithoutVector(ch.ID, "embed", 10)
	if err != nil || len(pend) != 3 || pend[0].ID != a || pend[2].ID != c {
		t.Fatalf("pending = %+v, %v", pend, err)
	}
	if two, _ := st.MessagesWithoutVector(ch.ID, "embed", 2); len(two) != 2 {
		t.Fatalf("the limit was ignored: %d", len(two))
	}
	if has, _ := st.HasMessageVectors(ch.ID); has {
		t.Fatal("a chat with no vectors says it has some")
	}

	if err := st.SaveMessageVectors("embed", pend[:2], [][]float32{{1, 0, 0.5}, {0, 1, 0}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.MessageVectors(ch.ID, b, "embed")
	if err != nil || len(got) != 2 || got[0].ID != a || got[0].Vec[2] != 0.5 || got[1].ID != b {
		t.Fatalf("vectors = %+v, %v", got, err)
	}
	if before, _ := st.MessageVectors(ch.ID, a, "embed"); len(before) != 1 {
		t.Errorf("the bookmark was ignored: %d", len(before))
	}
	if other, _ := st.MessageVectors(ch.ID, b, "another"); len(other) != 0 {
		t.Errorf("vectors of one model were listed for another")
	}
	if has, _ := st.HasMessageVectors(ch.ID); !has {
		t.Error("a chat with vectors says it has none")
	}
	// What is left to do is what lacks a vector from this model.
	if left, _ := st.MessagesWithoutVector(ch.ID, "embed", 10); len(left) != 1 || left[0].ID != c {
		t.Errorf("left = %+v", left)
	}
	if left, _ := st.MessagesWithoutVector(ch.ID, "another", 10); len(left) != 3 {
		t.Errorf("another model still has %d to do, want 3", len(left))
	}

	// Saying the same thing again keeps the vector; changing it drops it.
	if err := st.SetMessageContent(b, long("second")); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.MessageVectors(ch.ID, b, "embed"); len(got) != 2 {
		t.Errorf("an edit that changed nothing dropped a vector")
	}
	if err := st.SetMessageContent(a, long("changed")); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.MessageVectors(ch.ID, b, "embed"); len(got) != 1 || got[0].ID != b {
		t.Errorf("an edited message kept its vector: %+v", got)
	}
	if err := st.DeleteMessage(b); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.MessageVectors(ch.ID, c, "embed"); len(got) != 0 {
		t.Errorf("a deleted message kept its vector: %+v", got)
	}

	// A vector made from words the message no longer has is not kept.
	stale := PendingMessage{ID: c, Content: long("what it said before")}
	if err := st.SaveMessageVectors("embed", []PendingMessage{stale}, [][]float32{{1, 1, 1}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.MessageVectors(ch.ID, c, "embed"); len(got) != 0 {
		t.Errorf("a stale vector was saved: %+v", got)
	}
	if err := st.SaveMessageVectors("embed", pend, nil); err == nil {
		t.Error("a mismatched batch was accepted")
	}
}

func TestMomentsByIDKeepsTheOrderGiven(t *testing.T) {
	st := openTest(t)
	cid, _ := st.SaveCharacter(chars.Character{Name: "Vesper"})
	ch, _ := st.NewChat(cid, "Scene", "m", KindRoleplay)
	long := func(s string) string { return s + strings.Repeat(" and on it went", 6) }
	var ids []int64
	for _, s := range []string{"one", "two", "three"} {
		id, err := st.AddMessage(Message{ChatID: ch.ID, Role: "assistant", Content: long(s)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	got, err := st.MomentsByID(ch.ID, []int64{ids[2], 999999, ids[0]})
	if err != nil || len(got) != 2 || got[0].ID != ids[2] || got[1].ID != ids[0] {
		t.Fatalf("moments = %+v, %v", got, err)
	}
}
