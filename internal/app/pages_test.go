//go:build linux

package app

import (
	"testing"

	"astral/internal/chars"
)

func TestLeadCast(t *testing.T) {
	a := chars.Character{ID: 1, Name: "A"}
	b := chars.Character{ID: 2, Name: "B"}
	if got := leadCast(a, nil); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("no cast should be just the character, got %v", got)
	}
	if got := leadCast(a, []chars.Character{a}); len(got) != 1 {
		t.Fatalf("a cast of one should be just the character, got %v", got)
	}
	if got := leadCast(a, []chars.Character{a, b}); len(got) != 2 {
		t.Fatalf("a scene should keep everyone, got %v", got)
	}
}

func TestSameCast(t *testing.T) {
	a := chars.Character{ID: 1, Name: "A"}
	edited := a
	edited.Name = "Renamed"
	b := chars.Character{ID: 2, Name: "B"}
	cases := []struct {
		name string
		x, y []chars.Character
		want bool
	}{
		{"same", []chars.Character{a}, []chars.Character{a}, true},
		{"character edited", []chars.Character{a}, []chars.Character{edited}, false},
		{"someone cast", []chars.Character{a}, []chars.Character{a, b}, false},
	}
	for _, c := range cases {
		if got := sameCast(c.x, c.y); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
