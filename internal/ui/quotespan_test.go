package ui

import (
	"strings"
	"testing"
)

// A quotation inside a span the author marked as narration is part of that
// narration, not speech.
//
// Read as speech it cuts the narration in two and leaves an unpaired asterisk at
// each end, so the markers show and the italics start in the wrong places. In a
// reply with several such paragraphs the result alternates, one right and the
// next wrong, which is how it was noticed.
func TestQuoteInsideMarkedNarration(t *testing.T) {
	for _, in := range []string{
		`*She said the word "late" as though it were an accusation.*`,
		`*He read the sign: "No sailing after dark." Then he laughed.*`,
		`**He shouted "stop" and meant it.**`,
		`_She mouthed "later" at him._`,
	} {
		for _, mode := range []Prose{Roleplay, RoleplayAsWritten} {
			got := Markup(in, mode)
			if strings.Contains(got, "*") || strings.Contains(got, "_") {
				t.Errorf("mode %v left a marker in %q:\n%s", mode, in, got)
			}
			if strings.Contains(got, `weight="600"`) {
				t.Errorf("mode %v styled a quote inside narration as speech:\n%s", mode, got)
			}
		}
	}
}

// And speech that genuinely sits outside the narration is still speech, which is
// the thing the fix must not break.
func TestQuoteOutsideNarrationIsStillSpeech(t *testing.T) {
	for _, in := range []string{
		`*She paused.* "You're late."`,
		`"You're late." *She did not look up.*`,
		`*She paused.* "You're late." *Then nothing.*`,
		`She paused. "You're late."`,
	} {
		got := Markup(in, Roleplay)
		if !strings.Contains(got, `weight="600"`) {
			t.Errorf("speech outside narration lost its styling in %q:\n%s", in, got)
		}
		if strings.Contains(got, "*") {
			t.Errorf("a marker survived in %q:\n%s", in, got)
		}
	}
}

// An unbalanced marker is left alone rather than swallowing the rest of the
// reply. A model cut off at the token limit ends mid-span, and the turn after it
// must still render.
func TestUnbalancedMarkerDoesNotSwallowTheReply(t *testing.T) {
	got := Markup(`*She said "hello."`, Roleplay)
	if !strings.Contains(got, `weight="600"`) {
		t.Errorf("an unclosed narration span ate the speech after it:\n%s", got)
	}
}
