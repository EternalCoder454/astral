package ui

import (
	"testing"

	"astral/internal/store"
)

func TestKeepCountCoversWhatIsNeeded(t *testing.T) {
	heights := []int{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}
	for _, tc := range []struct {
		name              string
		need, least, want int
		spacing           int
		heightsOverride   []int
	}{
		{name: "enough rows for the pixels", need: 450, least: 2, want: 5},
		{name: "never fewer than least", need: 50, least: 4, want: 4},
		{name: "never more than there are", need: 5000, least: 2, want: 10},
		{name: "spacing counts", need: 450, least: 1, spacing: 50, want: 3},
		{name: "short rows keep more", need: 450, least: 1, heightsOverride: []int{400, 20, 20, 20, 20}, want: 5},
	} {
		h := heights
		if tc.heightsOverride != nil {
			h = tc.heightsOverride
		}
		if got := keepCount(h, tc.spacing, tc.need, tc.least); got != tc.want {
			t.Errorf("%s: keepCount = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestOlderThanSplitsAtTheFirstKeptRow(t *testing.T) {
	msgs := []store.Message{{ID: 3}, {ID: 5}, {ID: 8}, {ID: 13}}
	older, ok := olderThan(msgs, 8)
	if !ok || len(older) != 2 || older[1].ID != 5 {
		t.Errorf("olderThan(8) = %v, %v; want the two before it", older, ok)
	}
	if older, ok := olderThan(msgs, 3); !ok || len(older) != 0 {
		t.Errorf("olderThan(first) = %v, %v; want none, found", older, ok)
	}
	if _, ok := olderThan(msgs, 99); ok {
		t.Error("olderThan found an id that is not there")
	}
}
