package store

import (
	"strings"
	"testing"

	"astral/internal/chars"
)

func TestABranchKeepsTheVectorsOfWhatItCopies(t *testing.T) {
	st := openTest(t)
	cid, _ := st.SaveCharacter(chars.Character{Name: "Vesper"})
	ch, _ := st.NewChat(cid, "Scene", "m", KindRoleplay)
	long := func(s string) string { return s + strings.Repeat(" and on it went", 6) }
	var last int64
	for _, s := range []string{"one", "two"} {
		id, err := st.AddMessage(Message{ChatID: ch.ID, Role: "assistant", Content: long(s)})
		if err != nil {
			t.Fatal(err)
		}
		last = id
	}
	pend, _ := st.MessagesWithoutVector(ch.ID, "embed", 10)
	if err := st.SaveMessageVectors("embed", pend, [][]float32{{1, 0}, {0, 1}}); err != nil {
		t.Fatal(err)
	}
	br, err := st.BranchChat(ch.ID, last, "Branch")
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := st.Messages(br.ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("the branch has %d messages, %v", len(msgs), err)
	}
	got, err := st.MessageVectors(br.ID, msgs[1].ID, "embed")
	if err != nil || len(got) != 2 || got[1].Vec[1] != 1 {
		t.Fatalf("the branch's vectors = %+v, %v", got, err)
	}
	if orig, _ := st.MessageVectors(ch.ID, last, "embed"); len(orig) != 2 {
		t.Errorf("branching took the original's vectors: %d", len(orig))
	}
}
