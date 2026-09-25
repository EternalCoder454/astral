package chars

import "testing"

// The bug this file exists to prevent: the parts of a prompt adding up to more
// than the window they share. Every budget is checked against the window it
// came from, at several sizes, because the failure was silent at 8k and would
// have been silent at 4k too.
func TestPlanFitsTheWindow(t *testing.T) {
	for _, numCtx := range []int{4096, 8192, 16384, 32768, 131072} {
		for _, fixed := range []int{1500, 4000, 9000} {
			b := Plan(numCtx, 0, fixed)
			used := fixed + b.History + b.Lore + b.Recap
			// Everything sent, plus the reply, plus the safety margin, must
			// fit inside the window.
			tokens := int(float64(used)/charsPerToken) + DefaultReplyTokens + safetyTokens
			if tokens > numCtx {
				t.Errorf("numCtx=%d fixed=%d: prompt plans %d chars (~%d tokens), over the window by %d",
					numCtx, fixed, used, tokens, tokens-numCtx)
			}
		}
	}
}

// Compaction must fire before the transcript is trimmed, or turns are dropped
// before the recap has read them and the scene loses them for good.
func TestCompactionRunsBeforeTrimming(t *testing.T) {
	for _, numCtx := range []int{4096, 8192, 32768} {
		b := Plan(numCtx, 0, 4000)
		if b.History > 0 && b.Compact >= b.History {
			t.Errorf("numCtx=%d: compaction at %d chars, trimming at %d: turns would be dropped unread",
				numCtx, b.Compact, b.History)
		}
		if b.Keep >= b.Compact {
			t.Errorf("numCtx=%d: keeps %d of a %d-char threshold, so a pass reclaims nothing",
				numCtx, b.Keep, b.Compact)
		}
	}
}

// A reply limit the user raised has to come out of the transcript, not out of
// thin air.
func TestReplyLimitComesOutOfTheBudget(t *testing.T) {
	small := Plan(8192, 256, 4000)
	large := Plan(8192, 4096, 4000)
	if large.History >= small.History {
		t.Errorf("history with a 4096-token reply limit (%d) should be smaller than with 256 (%d)",
			large.History, small.History)
	}
}

// A window too small for the fixed parts must degrade to an empty transcript
// rather than to a negative one.
func TestTinyWindowDegrades(t *testing.T) {
	b := Plan(1024, 0, 9000)
	if b.History < 0 || b.Lore < 0 || b.Recap < 0 {
		t.Errorf("negative budget: %+v", b)
	}
}
