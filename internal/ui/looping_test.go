package ui

import (
	"strings"
	"testing"
)

// The block this is built from is real: a 27B model collapsed mid-reply and
// cycled a chain of synonyms until it hit the token limit.
const collapsed = "starting beginning initiating commencing launching opening unveiling " +
	"revealing exposing disclosing presenting exhibiting displaying performing acting " +
	"doing working laboring exerting straining stressing pressuring tensing conflicting " +
	"struggling battling fighting warring contesting competing rivaling opposing resisting " +
	"defying rebelling revolting uprising insurgent revolutionary transformative changing " +
	"altering modifying adjusting adapting evolving developing progressing advancing "

func TestLoopingCatchesACollapse(t *testing.T) {
	if !Looping(collapsed + collapsed) {
		t.Error("a block repeated verbatim was not detected")
	}
	if !Looping("Some ordinary opening prose. " + collapsed + collapsed) {
		t.Error("a collapse after a normal start was not detected")
	}
	// The line breaks a model emits while cycling are not identical, so the
	// comparison must not depend on them.
	wrapped := strings.ReplaceAll(collapsed, " ", "\n")
	if !Looping(wrapped + collapsed) {
		t.Error("the same block with different whitespace was not detected")
	}
}

// Everything here is prose a model might legitimately write. Stopping any of
// it would be worse than letting a bad reply run to its limit.
func TestLoopingLeavesGoodProseAlone(t *testing.T) {
	good := []struct{ name, text string }{
		{"ordinary roleplay", `*She did not look up from the chart. The rain had found the window again, and she let it.* "You're late."` + "\n\n" + `*A pin went into the table rather than the map, a small and deliberate violence.* "Sit. You're dripping on the Sever."`},
		{"deliberate repetition", `"No," she said. "No. No, I will not." *The dividers stopped, and did not start again.*`},
		{"a refrain", `*She hummed it under her breath.* "The tide comes in, the tide goes out. The tide comes in, the tide goes out."`},
		{"a short list", "Salt. Ink. Old paper. Salt. Ink. Old paper."},
		{"empty", ""},
		{"short", "Hello."},
	}
	for _, g := range good {
		if Looping(g.text) {
			t.Errorf("%s was wrongly flagged as a collapse: %q", g.name, g.text)
		}
	}
}

// A collapse is caught from the moment the second copy completes, not only
// once the reply is finished, or the point of catching it is lost.
func TestLoopingCatchesItEarly(t *testing.T) {
	full := collapsed + collapsed
	if !Looping(full) {
		t.Fatal("fixture does not loop")
	}
	// It fires as soon as one window has recurred, which is well before the
	// whole block has been written a second time — that is the point.
	early := collapsed + collapsed[:loopWindow+20]
	if !Looping(early) {
		t.Error("waited for the whole block to repeat before firing")
	}
	if Looping(collapsed) {
		t.Error("fired on the first copy, before anything had repeated")
	}
}

func BenchmarkLooping(b *testing.B) {
	// The worst case: a long reply that is not looping, so every period is
	// tried. This runs on the UI thread while tokens arrive.
	text := strings.Repeat("The tide came in early and the ferry did not. ", 90)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Looping(text)
	}
}

// This is the other way a reply comes apart, and it is real: a model dropped
// out of the scene mid-thought and chained associations until it was stopped.
// Nothing in it repeats, so Looping cannot see it. What it has not got is a
// full stop.
const rambled = "Let's begin crafting accordingly ensuring compliance at each step carefully " +
	"reviewed mentally beforehand before outputting finally down below ready go ahead " +
	"submit response please thank you much appreciated indeed always grateful sincerely " +
	"yours truly devotedly signed off faithfully evermore unwavering commitment shown " +
	"throughout consistently maintained standards upheld honorably well done job completed " +
	"successfully mission accomplished goal achieved target met objective fulfilled purpose " +
	"served function executed duty performed task finished work ended process terminated " +
	"operation concluded procedure finalized action stopped motion ceased movement halted " +
	"activity paused engagement broken connection severed relationship dissolved association " +
	"ended partnership closed alliance terminated union disbanded coalition fractured"

func TestRamblingCatchesWhatLoopingCannot(t *testing.T) {
	if Looping(rambled) {
		t.Error("the fixture repeats itself, so it does not test what it is here to test")
	}
	if !Rambling(rambled) {
		t.Errorf("a reply that ran %d characters without ending a sentence was not caught", len(rambled))
	}
}

func TestRamblingLeavesOrdinaryProseAlone(t *testing.T) {
	// A long, ordinary roleplay reply: several paragraphs, properly punctuated.
	prose := strings.Repeat(`*She set the pin down and did not look up.* "You're late again, and the `+
		`tide will not wait for either of us." *The lamp guttered. Outside, the rain kept on `+
		`against the glass, steady as a clock.* "Sit, if you are staying."`+"\n\n", 6)
	if Rambling(prose) {
		t.Errorf("ordinary prose was called a collapse:\n%s", prose[:200])
	}
}

// A list, a stanza or a line of dialogue with no full stop is not a collapse.
func TestRamblingTreatsALineBreakAsAnEnding(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("a line of about thirty characters with no punctuation at all\n")
	}
	if Rambling(b.String()) {
		t.Error("newline-separated lines with no full stops were called a collapse")
	}
}

// A scene played in a language that does not use the Latin full stop must not
// be called broken for using its own.
func TestRamblingUnderstandsOtherPunctuation(t *testing.T) {
	cjk := strings.Repeat("彼女はペンを置いた。外では雨が降り続いていた。", 40)
	if Rambling(cjk) {
		t.Error("Japanese prose was called a collapse")
	}
}

// The threshold has to be past anything real. A single very long sentence is
// unusual, not broken.
func TestRamblingAllowsOneVeryLongSentence(t *testing.T) {
	long := "She had been waiting since the tide turned, " +
		strings.Repeat("and thinking about the harbour, and the ferry, and the money, ", 5) +
		"and she had not moved."
	if n := len(long); n < 300 || n > rambleRun {
		t.Fatalf("fixture is %d chars; it must be a long sentence but under the %d threshold", n, rambleRun)
	}
	if Rambling(long) {
		t.Errorf("a %d-character sentence was called a collapse", len(long))
	}
}
