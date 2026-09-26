package chars

import (
	"strings"
	"testing"
)

var cast = []string{"Vesper", "Kestrel", "Ash"}

func TestSplitBeatsAttributesEachSpeaker(t *testing.T) {
	reply := `Vesper: *She did not look up from the chart.* "You're late."

Kestrel: "She's been saying that since noon."

Vesper: *A pin went into the table.* "Sit."`

	got := SplitBeats(reply, cast)
	want := []Beat{
		{"Vesper", "*She did not look up from the chart.* \"You're late.\""},
		{"Kestrel", "\"She's been saying that since noon.\""},
		{"Vesper", "*A pin went into the table.* \"Sit.\""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d beats, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("beat %d:\n got %q by %q\nwant %q by %q",
				i, got[i].Text, got[i].Name, want[i].Text, want[i].Name)
		}
	}
}

func TestSplitBeatsAcceptsTheDecorationsModelsAdd(t *testing.T) {
	// Every one of these is a real shape a model produces when asked for
	// "Name:" labels. A label read as prose puts a stray "**Kestrel:**" in the
	// middle of someone else's paragraph, which is worse than not splitting.
	for _, reply := range []string{
		"**Vesper:** \"You're late.\"\n\n**Kestrel:** \"Told you.\"",
		"**Vesper**: \"You're late.\"\n\n**Kestrel**: \"Told you.\"",
		"*Vesper:* \"You're late.\"\n\n*Kestrel:* \"Told you.\"",
		"## Vesper:\n\"You're late.\"\n\n## Kestrel:\n\"Told you.\"",
		"- Vesper: \"You're late.\"\n- Kestrel: \"Told you.\"",
		"Vesper:\n\"You're late.\"\n\nKestrel:\n\"Told you.\"",
	} {
		got := SplitBeats(reply, cast)
		if len(got) == 0 {
			t.Errorf("%q split into nothing", reply)
			continue
		}
		if got[0].Name != "Vesper" {
			t.Errorf("%q: first beat is by %q, want Vesper", reply, got[0].Name)
		}
		for _, b := range got {
			if strings.Contains(b.Text, "Vesper:") || strings.Contains(b.Text, "Kestrel:") {
				t.Errorf("%q: a label survived into the prose: %q", reply, b.Text)
			}
		}
	}
}

func TestSplitBeatsLeavesAnUnlabelledOpeningUnattributed(t *testing.T) {
	// A model that starts writing before naming anyone. The caller decides who
	// that was; the splitter must not guess, because guessing wrong puts one
	// character's words under another's face.
	got := SplitBeats("\"You're late.\"\n\nKestrel: \"Told you.\"", cast)
	if len(got) != 2 {
		t.Fatalf("got %d beats, want 2: %#v", len(got), got)
	}
	if got[0].Name != "" {
		t.Errorf("the opening beat is attributed to %q, want nobody", got[0].Name)
	}
	if got[1].Name != "Kestrel" {
		t.Errorf("second beat is by %q, want Kestrel", got[1].Name)
	}
}

func TestSplitBeatsIgnoresANameThatIsNotALabel(t *testing.T) {
	// The name in narration, and the name inside a line. Neither is a label,
	// and treating them as one splits a paragraph in half.
	reply := "Vesper: *Kestrel had said the same thing, and Vesper had ignored her then too.* \"No.\""
	got := SplitBeats(reply, cast)
	if len(got) != 1 {
		t.Fatalf("got %d beats, want 1: %#v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "Kestrel had said") {
		t.Errorf("the body was mangled: %q", got[0].Text)
	}
}

func TestSplitBeatsIgnoresACharacterNotInTheScene(t *testing.T) {
	got := SplitBeats("Vesper: \"Hello.\"\n\nHarbourmaster: \"No.\"", cast)
	if len(got) != 1 {
		t.Fatalf("got %d beats, want 1: %#v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "Harbourmaster") {
		t.Errorf("a name from outside the cast should stay in the prose: %q", got[0].Text)
	}
}

// chunkings returns the same text split several ways, including one character at
// a time, which is roughly how it actually arrives.
func chunkings(s string) [][]string {
	var out [][]string
	single := make([]string, 0, len(s))
	for _, r := range s {
		single = append(single, string(r))
	}
	out = append(out, single)
	for _, size := range []int{2, 3, 7, 13, 64} {
		var parts []string
		for i := 0; i < len(s); i += size {
			j := min(i+size, len(s))
			parts = append(parts, s[i:j])
		}
		out = append(out, parts)
	}
	return out
}

func TestBeatStreamAgreesWithOnePass(t *testing.T) {
	replies := []string{
		"Vesper: \"You're late.\"\n\nKestrel: \"Told you.\"",
		"**Vesper:** *She did not look up.*\n\n**Ash:** \"Both of you, stop.\"",
		"No labels here at all, just prose.",
		"Vesper:",
		"Vesper: a\nKestrel: b\nAsh: c\nVesper: d",
		"Ves is not a label. Vesper: this is.",
	}
	for _, reply := range replies {
		want := SplitBeats(reply, cast)
		for _, parts := range chunkings(reply) {
			var bs BeatStream
			bs.Names = cast
			var got []Beat
			for _, p := range parts {
				got = append(got, bs.Next(p)...)
			}
			got = MergeBeats(append(got, bs.Done()...))
			if !sameBeats(got, want) {
				t.Errorf("reply %q in chunks of %d:\n got %#v\nwant %#v",
					reply, len(parts[0]), got, want)
				break
			}
		}
	}
}

func sameBeats(a, b []Beat) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FuzzBeatStream is the invariant that matters: however the reply is chopped up
// on the way in, the beats are the same, and no character of the reply is
// invented or lost.
func FuzzBeatStream(f *testing.F) {
	f.Add("Vesper: hello\n\nKestrel: hi")
	f.Add("**Ash:** \n\nVesper:")
	f.Add("Ves")
	f.Add("V:e:s:p:e:r:")
	f.Add("\n\n\nVesper:\n\n\n")
	f.Add("Kestrel:00\nKestrel:00")
	f.Add("Kestrel:0\nKestrel:Kestrel:")
	f.Fuzz(func(t *testing.T, reply string) {
		want := SplitBeats(reply, cast)

		for _, size := range []int{1, 2, 5, 17} {
			var bs BeatStream
			bs.Names = cast
			var got []Beat
			for i := 0; i < len(reply); i += size {
				got = append(got, bs.Next(reply[i:min(i+size, len(reply))])...)
			}
			got = MergeBeats(append(got, bs.Done()...))
			if !sameBeats(got, want) {
				t.Fatalf("chunks of %d disagree with one pass\nreply %q\n got %#v\nwant %#v",
					size, reply, got, want)
			}
		}

		// And nothing is added in bulk: the prose can only shrink, because all
		// this does is take labels off and trim.
		total := 0
		for _, b := range want {
			total += len(b.Text)
		}
		if total > len(reply) {
			t.Fatalf("beats hold %d bytes but the reply was %d: %#v", total, len(reply), want)
		}
	})
}

func TestLabelPutsTheNameBackOn(t *testing.T) {
	if got := Label("Vesper", "  \"Hello.\"  "); got != `Vesper: "Hello."` {
		t.Errorf("Label gave %q", got)
	}
	if got := Label("", "\"Hello.\""); got != `"Hello."` {
		t.Errorf("an unnamed speaker should get no label, got %q", got)
	}
	if got := Label("Vesper", "   "); got != "" {
		t.Errorf("an empty line should stay empty, got %q", got)
	}
}

// TestLabelRoundTrips is the pair of the two: what Label writes, SplitBeats
// must read back. The transcript goes out labelled and comes back labelled, so
// a disagreement here would quietly reattribute a scene.
func TestLabelRoundTrips(t *testing.T) {
	in := []Beat{
		{"Vesper", "*She did not look up.* \"You're late.\""},
		{"Kestrel", "\"Told you.\"\n\n*She did not move from the door.*"},
		{"Ash", "\"Enough.\""},
	}
	var parts []string
	for _, b := range in {
		parts = append(parts, Label(b.Name, b.Text))
	}
	got := SplitBeats(strings.Join(parts, "\n\n"), cast)
	if !sameBeats(got, in) {
		t.Errorf("round trip changed the scene:\n got %#v\nwant %#v", got, in)
	}
}

// TestALabelAlwaysWins documents the one ambiguity in the format: a line that
// starts with a cast member's name and a colon is that character speaking, even
// in the middle of someone else's beat. It has to be read that way round — a
// model that writes a label mid-reply is switching speaker, which is the whole
// mechanism — and the alternative would be a scene where a name in the wrong
// place silently stopped working.
func TestALabelAlwaysWins(t *testing.T) {
	got := SplitBeats("Vesper: \"Come in.\"\nKestrel: \"Already did.\"", cast)
	if len(got) != 2 {
		t.Fatalf("got %d beats, want 2: %#v", len(got), got)
	}
	if got[0].Name != "Vesper" || got[1].Name != "Kestrel" {
		t.Errorf("got %q then %q, want Vesper then Kestrel", got[0].Name, got[1].Name)
	}
}
