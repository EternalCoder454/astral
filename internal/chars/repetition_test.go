package chars

import (
	"strings"
	"testing"

	"astral/internal/ollama"
)

func replies(texts ...string) []ollama.Message {
	var out []ollama.Message
	for _, t := range texts {
		out = append(out,
			ollama.Message{Role: ollama.RoleUser, Content: "I wait."},
			ollama.Message{Role: ollama.RoleAssistant, Content: t})
	}
	return out
}

func TestOverusedFindsAHabit(t *testing.T) {
	r := Overused(replies(
		`*The ghost of a smile touched her lips as she set the chart down.* "Late again."`,
		`*She turned the dividers over.* "Nine days is not three."`,
		`*The ghost of a smile touched her lips.* "Sit. You are dripping on the Sever."`,
	))
	if len(r.Phrases) == 0 {
		t.Fatal("found nothing, and one phrase was used twice")
	}
	if !strings.Contains(r.Phrases[0], "ghost of a smile touched her lips") {
		t.Errorf("want the repeated phrase first, got %q", r.Phrases)
	}
	// Its pieces are not named separately: one slot, one habit.
	for _, p := range r.Phrases[1:] {
		if strings.Contains("the ghost of a smile touched her lips", p) {
			t.Errorf("a piece of the chosen phrase was named as well: %q", p)
		}
	}
}

func TestOverusedIgnoresGrammar(t *testing.T) {
	// "and she said" and "in the room" repeat in every scene ever written and
	// are not a habit anyone can hear.
	r := Overused(replies(
		`*And she said nothing, in the room with the rain.*`,
		`*And she said it twice, in the room where the maps were.*`,
		`*And she said it again.*`,
	))
	for _, p := range r.Phrases {
		t.Errorf("function words reported as a habit: %q", p)
	}
}

func TestOverusedNeedsTwoReplies(t *testing.T) {
	// Repeating something inside one reply can be deliberate (a refrain, a
	// correction), and the closing block cannot help with a reply already
	// written. Only recurrence across replies counts.
	r := Overused(replies(
		`*A slow breath. A slow breath. A slow breath, and then she spoke.*`,
	))
	if !r.Empty() {
		t.Errorf("a single reply produced %v", r)
	}
}

func TestOverusedNoticesTheSameOpening(t *testing.T) {
	r := Overused(replies(
		`*She tilts her head.* "No."`,
		`*She tilts the lamp toward the map.* "There."`,
		`*She tilts back in the chair.* "Go on."`,
		`"Fine," *she says.*`,
	))
	if r.Opening != "she tilts" {
		t.Errorf("want the repeated opening, got %q", r.Opening)
	}
	// Opening with "She" is ordinary and not a tic.
	r = Overused(replies(
		`*She looks up.* "No."`,
		`*She turns the lamp.* "There."`,
		`*She leans back.* "Go on."`,
	))
	if r.Opening != "" {
		t.Errorf("a shared first word was reported as a tic: %q", r.Opening)
	}
}

func TestOverusedIsBounded(t *testing.T) {
	var texts []string
	for i := 0; i < 8; i++ {
		texts = append(texts, `*Cold iron smell. Brass dividers gleam. Rain hammers glass. Candle smoke curls.
Harbour bells toll. Salt wind bites. Ink stains fingers. Maps curl slowly. Gulls wheel overhead. Tide charts lie.*`)
	}
	r := Overused(replies(texts...))
	if len(r.Phrases) > maxOverused {
		t.Errorf("named %d phrases, the limit is %d", len(r.Phrases), maxOverused)
	}
}

func TestOverusedLooksOnlyAtRecentReplies(t *testing.T) {
	old := `*The ghost of a smile touched her lips.*`
	h := replies(old, old)
	// Six fresh replies push the old pair out of the window.
	for i, s := range []string{"one", "two", "three", "four", "five", "six"} {
		_ = i
		h = append(h, replies(`*A different thing happens, number `+s+`.*`)...)
	}
	for _, p := range Overused(h).Phrases {
		if strings.Contains(p, "ghost") {
			t.Errorf("a phrase from outside the window was reported: %q", p)
		}
	}
}

func TestRepetitionScore(t *testing.T) {
	earlier := []string{`*The ghost of a smile touched her lips.* "Late again."`}
	if s := RepetitionScore(`*The ghost of a smile touched her lips.*`, earlier); s < 0.99 {
		t.Errorf("a rerun scored %.2f, want 1", s)
	}
	if s := RepetitionScore(`*Rain found the window, and the lamp guttered once.*`, earlier); s != 0 {
		t.Errorf("a fresh reply scored %.2f, want 0", s)
	}
	if s := RepetitionScore("", earlier); s != 0 {
		t.Errorf("an empty reply scored %.2f", s)
	}
}

func TestSentencesKeepContractionsWhole(t *testing.T) {
	got := sentencesOf(`"Don't." She's gone… Then, quietly: nothing.`)
	want := [][]string{{"don't"}, {"she's", "gone"}, {"then", "quietly"}, {"nothing"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Errorf("sentence %d: got %v want %v", i, got[i], want[i])
		}
	}
}

func TestOverusedNoticesTheSameSwearWord(t *testing.T) {
	r := Overused(replies(
		`"Fuck. The generator again."`,
		`"Fucking thing." *She kicks it.*`,
		`"Don't start, kid."`,
		`"Well, fuck me."`,
	))
	if len(r.Swears) != 1 || r.Swears[0] != "fuck" {
		t.Errorf("want fuck reported, got %v", r.Swears)
	}
}

func TestOverusedLeavesVarietyAlone(t *testing.T) {
	// Swearing with range is what was asked for. Four different words across
	// four replies is a character who swears, not a habit.
	r := Overused(replies(
		`"Shit. The generator again."`,
		`"Goddamn thing." *She kicks it.*`,
		`"Bloody hell, kid."`,
		`"Well, fuck me."`,
	))
	if len(r.Swears) != 0 {
		t.Errorf("variety was reported as repetition: %v", r.Swears)
	}
}

func TestSwearWordsDoNotMatchInsideOtherWords(t *testing.T) {
	r := Overused(replies(
		`"Hello. Pass the class notes, the shell and the assembly."`,
		`"Hello again. The shell is cracked, pass it."`,
		`"Hello. Assume nothing. Pass."`,
	))
	if len(r.Swears) != 0 {
		t.Errorf("ordinary words were read as swearing: %v", r.Swears)
	}
}

func TestFreshWordingKeepsTheSwearingButNotTheWord(t *testing.T) {
	got := freshWording(Repetition{Swears: []string{"fuck"}}, "Dagny", "Wren")
	for _, want := range []string{`"fuck"`, "keep it if it suits the character", "different word"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
