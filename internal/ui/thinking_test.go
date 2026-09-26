package ui

import (
	"strings"
	"testing"
)

func TestSplitThinking(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		think, reply string
	}{
		{"no tags", "She set the pin down.", "", "She set the pin down."},
		{"leading block", "<think>I should be brief.</think>*She set the pin down.*",
			"I should be brief.", "*She set the pin down.*"},
		{"whitespace and newlines", "\n<think>\nI should be brief.\n</think>\n\nShe waited.",
			"I should be brief.", "She waited."},
		{"case insensitive", "<THINK>hm</THINK>Fine.", "hm", "Fine."},
		{"thinking spelling", "<thinking>hm</thinking>Fine.", "hm", "Fine."},
		{"reasoning spelling", "<reasoning>hm</reasoning>Fine.", "hm", "Fine."},
		// The reply limit ran out mid-deliberation. Everything is thinking, and
		// the caller reports an empty reply rather than showing the scene a
		// monologue about its own instructions.
		{"unclosed takes the rest", "<think>I should be brief and also",
			"I should be brief and also", ""},
		// A tag in the middle of a reply is dialogue or a name, not a reasoning
		// block, and cutting the middle out of a reply is worse than leaving it.
		{"mid-reply tag is left alone", `"What do you <think>?" she asked.`,
			"", `"What do you <think>?" she asked.`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			think, reply := SplitThinking(c.in)
			if think != c.think || reply != c.reply {
				t.Errorf("SplitThinking(%q)\n think: %q, want %q\n reply: %q, want %q",
					c.in, think, c.think, reply, c.reply)
			}
		})
	}
}

// Whatever it is given, the two halves together must account for the input:
// this moves text between channels, it never invents or drops any.
func FuzzSplitThinkingKeepsEverything(f *testing.F) {
	for _, seed := range []string{
		"", "<think>", "</think>", "<think></think>", "<think>a</think>b",
		"plain", "<think>a", "<<think>>", "<thinking><think>a</think></thinking>",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		think, reply := SplitThinking(in)
		// Text with no leading tag must come back exactly as it went in. Text
		// that has one may come back with nothing at all, because the block can
		// be empty and the tag itself is consumed either way.
		if !startsWithThinkTag(in) && reply != in {
			t.Fatalf("no tag to act on but the reply changed:\n in: %q\nout: %q", in, reply)
		}
		// Nothing is invented: both halves come out of the input.
		for _, part := range []string{think, reply} {
			if part != "" && !strings.Contains(in, part) {
				t.Fatalf("output %q is not in the input %q", part, in)
			}
		}
	})
}

func startsWithThinkTag(s string) bool {
	low := strings.ToLower(strings.TrimLeft(s, " \t\r\n"))
	for _, tag := range thinkTags {
		if strings.HasPrefix(low, tag[0]) {
			return true
		}
	}
	return false
}

// Mistral-derived finetunes mark deliberation with square brackets. Cydonia
// does, and the UGI leaderboard lists a whole column of "[THINK] prefill"
// variants, so this is a family rather than one model.
func TestSplitThinkingKnowsSquareBrackets(t *testing.T) {
	cases := []struct{ in, think, reply string }{
		{"[THINK]She is late again.[/THINK]*Vesper looked up.*", "She is late again.", "*Vesper looked up.*"},
		{"[think]hm[/think]Fine.", "hm", "Fine."},
		{"\n[THINK]\nstill deciding\n[/THINK]\n\nShe waited.", "still deciding", "She waited."},
		// Unclosed takes the rest, the same as the angle-bracket spellings.
		{"[THINK]I should be brief and", "I should be brief and", ""},
		// And a stray bracket mid-reply is dialogue, not a block.
		{`"What do you [think]?" she asked.`, "", `"What do you [think]?" she asked.`},
	}
	for _, c := range cases {
		think, reply := SplitThinking(c.in)
		if think != c.think || reply != c.reply {
			t.Errorf("SplitThinking(%q)\n think: %q, want %q\n reply: %q, want %q",
				c.in, think, c.think, reply, c.reply)
		}
	}
}
