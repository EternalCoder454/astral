package world

import (
	"strings"
	"testing"
)

// sentNames is the names of what Explain says goes out, in order.
func sentNames(hits []Hit) []string {
	var out []string
	for _, h := range hits {
		if h.Sent() {
			out = append(out, h.Entry.Name)
		}
	}
	return out
}

// The same entry on the same turn is decided the same way however often it is
// asked, or What the Model Sees would say one thing and the reply do another.
// Over many turns the share sent is the chance, and a chance of 100 is every
// turn.
func TestChanceIsDecidedByTheTurn(t *testing.T) {
	e := entry(7, "Rumour", "Somebody saw a light on the water.", "light")
	e.Chance = 40
	sent := 0
	for turn := 0; turn < 5000; turn++ {
		first := Explain([]Entry{e}, "a light", BudgetChars, turn)
		again := Explain([]Entry{e}, "a light", BudgetChars, turn)
		if first[0].Skipped != again[0].Skipped {
			t.Fatalf("turn %d was decided two ways", turn)
		}
		if first[0].Sent() {
			sent++
		}
	}
	if sent < 1800 || sent > 2200 {
		t.Errorf("a chance of 40 sent %d of 5000 turns, want about 2000", sent)
	}

	// Another entry rolls for itself rather than in step with this one.
	other := e
	other.ID = 8
	same := 0
	for turn := 0; turn < 1000; turn++ {
		a := Explain([]Entry{e}, "a light", BudgetChars, turn)[0].Sent()
		b := Explain([]Entry{other}, "a light", BudgetChars, turn)[0].Sent()
		if a == b {
			same++
		}
	}
	if same > 800 {
		t.Errorf("two entries decided alike on %d of 1000 turns", same)
	}

	// Set to 100, or never set, it is every turn.
	for _, chance := range []int{0, 100} {
		e.Chance = chance
		for turn := 0; turn < 200; turn++ {
			if h := Explain([]Entry{e}, "a light", BudgetChars, turn); !h[0].Sent() {
				t.Fatalf("a chance of %d was left out on turn %d: %q", chance, turn, h[0].Skipped)
			}
		}
	}
}

func TestChanceLeftOutSaysSo(t *testing.T) {
	e := entry(3, "Rumour", "Somebody saw a light.", "light")
	e.Chance = 1
	for turn := 0; turn < 200; turn++ {
		h := Explain([]Entry{e}, "a light", BudgetChars, turn)[0]
		if h.Skipped == "" {
			continue
		}
		if h.Skipped != "Chance of 1 percent, not this turn." || h.Dropped || h.Key != "light" {
			t.Fatalf("left out as %+v", h)
		}
		return
	}
	t.Error("a chance of 1 percent was sent on every one of 200 turns")
}

func TestWaitHoldsAnEntryBackUntilTheSceneHasGrown(t *testing.T) {
	late := entry(1, "The Reveal", "She is the harbourmaster's daughter.", "harbourmaster")
	late.Wait = 20
	always := entry(2, "Always", "Tides.", "tides")
	always.Wait = 20
	always.Constant = true

	h := Explain([]Entry{late, always}, "the harbourmaster and the tides", BudgetChars, 19)
	if got := sentNames(h); strings.Join(got, ",") != "Always" {
		t.Errorf("at 19 messages sent %v, want only the constant entry, which ignores Wait", got)
	}
	for _, x := range h {
		if x.Entry.Name == "The Reveal" && (x.Skipped != "Waits for 20 messages." || x.Dropped) {
			t.Errorf("the waiting entry was reported as %+v", x)
		}
	}
	if got := sentNames(Explain([]Entry{late}, "the harbourmaster", BudgetChars, 20)); len(got) != 1 {
		t.Errorf("at 20 messages sent %v, want the entry", got)
	}

	late.Wait = 1
	if s := Explain([]Entry{late}, "the harbourmaster", BudgetChars, 0)[0].Skipped; s != "Waits for 1 message." {
		t.Errorf("reason = %q", s)
	}

	// A caller with no scene to count is not made to wait.
	late.Wait = 20
	if got := names(Match([]Entry{late}, "the harbourmaster", BudgetChars)); len(got) != 1 {
		t.Errorf("Match sent %v, want the entry", got)
	}
}

// Of a group only the first triggered entry goes: by priority, then by id. An
// entry that is not being sent for another reason does not take the place, and
// a constant entry is in the running.
func TestGroupSendsOneEntry(t *testing.T) {
	a := entry(1, "Smuggler", "Talk of a smuggler.", "tavern")
	a.Group = "Rumours"
	b := entry(2, "Ghost", "Talk of a ghost.", "tavern")
	b.Group = "rumours"
	b.Priority = 5
	c := entry(3, "Storm", "Talk of a storm.", "tavern")
	c.Group = "Rumours"
	c.Priority = 5
	solo := entry(4, "Solo", "Not in a group.", "tavern")

	h := Explain([]Entry{a, b, c, solo}, "the tavern", BudgetChars, 10)
	if got := strings.Join(sentNames(h), ","); got != "Ghost,Solo" {
		t.Errorf("sent %v, want the highest priority of the group by lowest id, and the one outside it", got)
	}
	for _, x := range h {
		switch x.Entry.Name {
		case "Smuggler":
			if x.Skipped != "Another entry in the group Rumours went instead." || x.Dropped {
				t.Errorf("Smuggler reported as %+v", x)
			}
		case "Storm":
			if x.Skipped == "" {
				t.Errorf("Storm was sent beside Ghost")
			}
		}
	}

	// The winner waiting means the next in line goes, not nobody.
	b.Wait = 50
	if got := strings.Join(sentNames(Explain([]Entry{a, b, c, solo}, "the tavern", BudgetChars, 10)), ","); got != "Storm,Solo" {
		t.Errorf("with Ghost waiting sent %v, want Storm and Solo", got)
	}

	// A constant entry is sent every turn and still gives way to a higher
	// priority member of its group, and takes the place of a lower one.
	k := Entry{ID: 9, Name: "Custom", Content: "Always told.", Enabled: true, Constant: true, Group: "Rumours", Priority: 9}
	if got := strings.Join(sentNames(Explain([]Entry{a, c, k}, "the tavern", BudgetChars, 10)), ","); got != "Custom" {
		t.Errorf("sent %v, want only the constant entry of the group", got)
	}
}

// What is not sent takes no room and names nothing: the entry a waiting entry
// mentions is not brought in on its account.
func TestEntriesLeftOutTakeNoRoomAndBringNothing(t *testing.T) {
	late := entry(1, "The Reveal", "She answers to the Guild. "+strings.Repeat("x", 150), "harbourmaster")
	late.Wait = 20
	guild := entry(2, "The Guild", "Cartographers.", "Guild")
	fits := entry(3, "Docks", "Silted.", "docks")

	h := Explain([]Entry{late, guild, fits}, "the harbourmaster at the docks", 60, 5)
	if got := strings.Join(sentNames(h), ","); got != "Docks" {
		t.Errorf("sent %v, want only Docks: the waiting entry neither fits nor names the guild", got)
	}
	for _, x := range h {
		if x.Entry.Name == "The Guild" {
			t.Errorf("the guild was brought in by an entry that was not sent: %+v", x)
		}
	}
}

// When an entry is sent travels with it, and an entry that never set any of it
// leaves nothing in the file, so it reads as a world from before they existed.
func TestWorldFileCarriesWhenAnEntryIsSent(t *testing.T) {
	w, entries := sampleWorld()
	entries[0].Chance, entries[0].Wait, entries[0].Group = 40, 12, "Rumours"
	data, err := Encode(w, entries, "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if e := got.Entries[0]; e.Chance != 40 || e.Wait != 12 || e.Group != "Rumours" {
		t.Errorf("chance, wait and group came back as %d, %d, %q", e.Chance, e.Wait, e.Group)
	}
	// The rest were never set: no fields in the file, and the defaults back.
	if strings.Count(string(data), `"chance"`) != 1 || strings.Count(string(data), `"group"`) != 1 {
		t.Errorf("unset entries wrote settings into the file:\n%s", data)
	}
	if e := got.Entries[1]; e.Chance != 100 || e.Wait != 0 || e.Group != "" {
		t.Errorf("an entry with none set came back as %d, %d, %q", e.Chance, e.Wait, e.Group)
	}

	// A hand-edited file is checked like the rest of it.
	odd, err := Decode([]byte(`{"name":"W","entries":[
	  {"name":"Alpha","keys":["alpha"],"content":"x","chance":500,"wait":-3,"group":"  Two   words  "}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if e := odd.Entries[0]; e.Chance != 100 || e.Wait != 0 || e.Group != "Two words" {
		t.Errorf("out of range settings came back as %d, %d, %q", e.Chance, e.Wait, e.Group)
	}
}

// A group's entry that does not fit gives its place to the next of the group
// that does, rather than leaving the group out.
func TestGroupGivesWayWhenItsEntryDoesNotFit(t *testing.T) {
	big := entry(1, "Long Rumour", strings.Repeat("A long rumour. ", 20), "tavern")
	big.Group, big.Priority = "Rumours", 5
	small := entry(2, "Short Rumour", "A short one.", "tavern")
	small.Group = "Rumours"
	other := entry(3, "Also Short", "Another.", "tavern")
	other.Group = "Rumours"

	h := Explain([]Entry{big, small, other}, "the tavern", 60, 10)
	if got := strings.Join(sentNames(h), ","); got != "Short Rumour" {
		t.Fatalf("sent %v, want the next of the group in the place of the one that did not fit", got)
	}
	for _, x := range h {
		switch x.Entry.Name {
		case "Long Rumour":
			if !x.Dropped || x.Skipped != "" {
				t.Errorf("the entry that did not fit was reported as %+v", x)
			}
		case "Also Short":
			if x.Skipped != "Another entry in the group Rumours went instead." {
				t.Errorf("the third of the group was reported as %+v", x)
			}
		}
	}
}

// An entry a sent entry mentions never takes the place of one of its group
// that is already going, however it ranks: the one going has already named
// what it names.
func TestAMentionedEntryDoesNotDisplaceItsGroup(t *testing.T) {
	harbour := entry(1, "Harbourmaster", "She answers to the Guild.", "harbourmaster")
	harbour.Group = "People"
	guild := entry(2, "The Guild", "Cartographers who own the docks.", "Guild")
	guild.Group, guild.Priority = "people", 9

	h := Explain([]Entry{harbour, guild}, "the harbourmaster", BudgetChars, 10)
	if got := strings.Join(sentNames(h), ","); got != "Harbourmaster" {
		t.Fatalf("sent %v, want the direct match kept", got)
	}
	if h[1].Entry.Name != "The Guild" || h[1].Via != "Harbourmaster" || h[1].Skipped == "" {
		t.Errorf("the mentioned entry was reported as %+v", h[1])
	}
}
