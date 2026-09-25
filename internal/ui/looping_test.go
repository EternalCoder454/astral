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
