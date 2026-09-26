package world

import (
	"strings"
	"testing"
)

func sampleWorld() (World, []Entry) {
	return World{
			Name:        "Sever Reach",
			Description: "A port city where the tide comes in wrong.",
			Rules:       "Nobody sails after dark.",
		}, []Entry{
			{Name: "The Harbour", Keys: []string{"harbour", "docks"}, Content: "Silted since the war.", Enabled: true},
			{Name: "Tide Charts", Keys: []string{"charts"}, Content: "Reissued every spring.", Enabled: true, Constant: true, Priority: 3},
			{Name: "Learned Thing", Keys: []string{"rumour"}, Content: "Overheard in play.", Enabled: false, Auto: true},
		}
}

func TestWorldFileRoundTrips(t *testing.T) {
	w, entries := sampleWorld()
	data, err := Encode(w, entries, "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.World.Name != w.Name || got.World.Description != w.Description || got.World.Rules != w.Rules {
		t.Errorf("the world changed: %#v", got.World)
	}
	if len(got.Entries) != len(entries) {
		t.Fatalf("got %d entries, want %d", len(got.Entries), len(entries))
	}
	for i, e := range got.Entries {
		if e.Name != entries[i].Name || e.Content != entries[i].Content {
			t.Errorf("entry %d changed: %#v", i, e)
		}
		if e.Constant != entries[i].Constant || e.Priority != entries[i].Priority {
			t.Errorf("entry %d lost its settings: %#v", i, e)
		}
		if e.Auto != entries[i].Auto {
			t.Errorf("entry %d lost whether the model wrote it: %#v", i, e)
		}
	}
	// A constant entry is always in force, so it cannot arrive switched off: it
	// would be sent on every turn by one rule and never by the other.
	for _, e := range got.Entries {
		if e.Constant && !e.Enabled {
			t.Errorf("%q is constant but disabled", e.Name)
		}
	}
	// And nothing from one database travels to another.
	if strings.Contains(string(data), `"world_id"`) || strings.Contains(string(data), `"id"`) {
		t.Errorf("the file carries ids from the database it came out of:\n%s", data)
	}
}

func TestWorldFileRefusesWhatItCannotRead(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"not json", `nonsense`},
		{"another format", `{"format":"astral-character","name":"x","entries":[]}`},
		{"from the future", `{"format":"astral-world","version":99,"name":"x","entries":[]}`},
		{"no name", `{"format":"astral-world","version":1,"name":"  ","entries":[]}`},
	} {
		if _, err := Decode([]byte(tc.in)); err == nil {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

// A file with no format field at all is read anyway, so a world someone
// hand-wrote or trimmed is not refused over a line of bookkeeping.
func TestWorldFileAcceptsAHandWrittenOne(t *testing.T) {
	got, err := Decode([]byte(`{"name":"Small","entries":[
	  {"name":"A Place","keys":["place"],"content":"It is there.","enabled":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.World.Name != "Small" || len(got.Entries) != 1 {
		t.Errorf("got %#v", got)
	}
}

// An imported file is a document from outside this machine, so its entries are
// checked rather than trusted: the same key rules as one the model just wrote.
func TestWorldFileDropsEntriesNothingCouldTrigger(t *testing.T) {
	got, err := Decode([]byte(`{"format":"astral-world","version":1,"name":"W","entries":[
	  {"name":"Fine","keys":["fine"],"content":"kept"},
	  {"name":"","keys":["nameless"],"content":"dropped"},
	  {"name":"Empty","keys":["empty"],"content":"  "},
	  {"name":"Twice","keys":["twice"],"content":"first"},
	  {"name":"twice","keys":["again"],"content":"second"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got.Entries), entryNames(got.Entries))
	}
	for _, e := range got.Entries {
		if len(e.Keys) == 0 {
			t.Errorf("%q would never be sent", e.Name)
		}
	}
}

func TestWorldFilenameIsSafe(t *testing.T) {
	for in, want := range map[string]string{
		"Sever Reach":        "sever-reach",
		"The 8th Ward":       "the-8th-ward",
		"  ../../etc/passwd": "etcpasswd",
		"!!!":                "world",
		"":                   "world",
	} {
		if got := Filename(World{Name: in}); got != want {
			t.Errorf("Filename(%q) = %q, want %q", in, got, want)
		}
	}
}
