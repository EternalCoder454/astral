package world

import (
	"strings"
	"testing"
)

func TestParseDraftReadsAWorldAndItsLorebook(t *testing.T) {
	raw := []byte(`{
	  "name": "Sever Reach",
	  "description": "A port city where the tide comes in wrong.",
	  "rules": "Nobody sails after dark.\nThe harbourmaster's word is law on the water.",
	  "entries": [
	    {"name": "The Harbour", "keys": ["harbour", "docks"], "content": "Silted since the war."},
	    {"name": "Vesper Quill", "keys": ["vesper"], "content": "Charts coastlines that have not settled."}
	  ]
	}`)
	d, err := ParseDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.World.Name != "Sever Reach" {
		t.Errorf("name is %q", d.World.Name)
	}
	if !strings.Contains(d.World.Rules, "Nobody sails after dark.") {
		t.Errorf("rules are %q", d.World.Rules)
	}
	if len(d.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(d.Entries))
	}
	for _, e := range d.Entries {
		if !e.Enabled {
			t.Errorf("%q arrived switched off", e.Name)
		}
		// Written with the user in the room rather than learned behind their
		// back, so it needs no review and is not marked as the model's.
		if e.Auto {
			t.Errorf("%q is marked as the model's own work", e.Name)
		}
	}
}

// TestParseDraftAddsTheNameAsAKey is the difference between a lorebook that
// works and one that looks like it does. An entry is only ever sent when a key
// appears in the conversation, and the one word a conversation about a subject is
// certain to use is its name.
func TestParseDraftAddsTheNameAsAKey(t *testing.T) {
	raw := []byte(`{"name":"W","description":"d","rules":"r","entries":[
	  {"name":"Harbourmaster","keys":["docks"],"content":"Holds the tide charts."}]}`)
	d, err := ParseDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Entries) != 1 {
		t.Fatalf("got %d entries", len(d.Entries))
	}
	found := false
	for _, k := range d.Entries[0].Keys {
		if strings.EqualFold(k, "Harbourmaster") {
			found = true
		}
	}
	if !found {
		t.Errorf("the entry's own name is not a key: %v", d.Entries[0].Keys)
	}
}

func TestParseDraftDropsEntriesNothingCouldTrigger(t *testing.T) {
	// A key of "it" fires on everything and a key that is a sentence fires on
	// nothing. Both are rejected, and an entry left with neither a usable key nor
	// a usable name is dropped rather than stored looking functional.
	raw := []byte(`{"name":"W","description":"d","rules":"r","entries":[
	  {"name":"ok","keys":["a"],"content":"kept, because its name works as a key"},
	  {"name":"","keys":["harbour"],"content":"no name at all"},
	  {"name":"Fine","keys":["fine"],"content":""}]}`)
	d, err := ParseDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range d.Entries {
		if e.Name == "" || e.Content == "" {
			t.Errorf("an unusable entry survived: %#v", e)
		}
		if len(e.Keys) == 0 {
			t.Errorf("%q has no keys, so it would never be sent", e.Name)
		}
	}
}

func TestParseDraftDropsARepeatedName(t *testing.T) {
	// Identity in a lorebook is (world, name), so two entries with one name are
	// one entry that the second silently overwrote.
	raw := []byte(`{"name":"W","description":"d","rules":"r","entries":[
	  {"name":"The Harbour","keys":["harbour"],"content":"First."},
	  {"name":"the harbour","keys":["docks"],"content":"Second."}]}`)
	d, err := ParseDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Entries) != 1 {
		t.Errorf("got %d entries, want 1: %v", len(d.Entries), entryNames(d.Entries))
	}
	if d.Entries[0].Content != "First." {
		t.Errorf("the surviving entry is %q, want the first", d.Entries[0].Content)
	}
}

func TestParseDraftRefusesAWorldWithNoName(t *testing.T) {
	if _, err := ParseDraft([]byte(`{"name":"  ","description":"d","rules":"r","entries":[]}`)); err == nil {
		t.Error("a world with no name was accepted; it would be unopenable in the list")
	}
}

func TestParseDraftRefusesRubbish(t *testing.T) {
	if _, err := ParseDraft([]byte(`not json at all`)); err == nil {
		t.Error("an unparseable answer was accepted")
	}
}

// TestDesignerPromptTeachesWhatAKeyIs guards the one instruction that cannot be
// repaired downstream. An entry whose keys are abstractions never fires, and a
// world full of those looks like it is working right up until it is needed.
func TestDesignerPromptTeachesWhatAKeyIs(t *testing.T) {
	low := strings.ToLower(extractInstruction)
	for _, want := range []string{"keys are the words", "never use abstractions"} {
		if !strings.Contains(low, want) {
			t.Errorf("the extraction prompt no longer explains keys: missing %q", want)
		}
	}
	if !strings.Contains(strings.ToLower(DesignerSystem), "at most two questions") {
		t.Error("the interview no longer limits how many questions a message may ask")
	}
}

func entryNames(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}
