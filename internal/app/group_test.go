package app

import (
	"testing"

	"astral/internal/chars"
)

func TestCastChangeNoteSaysWhatChanged(t *testing.T) {
	vesper := chars.Character{ID: 1, Name: "Vesper"}
	kestrel := chars.Character{ID: 2, Name: "Kestrel"}
	ash := chars.Character{ID: 3, Name: "Ash"}

	for _, tc := range []struct {
		name          string
		before, after []chars.Character
		want          string
	}{
		{"joined", []chars.Character{vesper, kestrel}, []chars.Character{vesper, kestrel, ash},
			"Ash joined the scene"},
		{"left", []chars.Character{vesper, kestrel, ash}, []chars.Character{vesper, kestrel},
			"Ash left the scene"},
		{"swapped", []chars.Character{vesper, kestrel}, []chars.Character{vesper, ash},
			"Ash joined the scene, Kestrel left"},
		{"unchanged", []chars.Character{vesper, kestrel}, []chars.Character{vesper, kestrel},
			"The cast is unchanged"},
		// Reordering is not a change of cast, and saying so would be noise every
		// time somebody opened the picker and closed it again.
		{"reordered", []chars.Character{vesper, kestrel}, []chars.Character{kestrel, vesper},
			"The cast is unchanged"},
	} {
		if got := castChangeNote(tc.before, tc.after); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGroupTitleReadsAsAScene(t *testing.T) {
	name := func(n string) chars.Character { return chars.Character{Name: n} }
	for _, tc := range []struct {
		cast []chars.Character
		want string
	}{
		{nil, "New scene"},
		{[]chars.Character{name("Vesper")}, "Vesper"},
		{[]chars.Character{name("Vesper"), name("Kestrel")}, "Vesper and Kestrel"},
		{[]chars.Character{name("Vesper"), name("Kestrel"), name("Ash")}, "Vesper, Kestrel and Ash"},
		{[]chars.Character{name("Vesper"), name("Kestrel"), name("Ash"), name("Bell")},
			"Vesper, Kestrel and 2 others"},
		{[]chars.Character{name("Vesper"), name("Kestrel"), name("Ash"), name("Bell"), name("Maur")},
			"Vesper, Kestrel and 3 others"},
	} {
		if got := groupTitle(tc.cast); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
