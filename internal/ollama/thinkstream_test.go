package ollama

import (
	"strings"
	"testing"
)

// feed runs a stream through in the given chunks and returns what a viewer
// would have seen, and what was folded away.
func feed(chunks ...string) (reply, thinking string) {
	var t ThinkStream
	var r, th strings.Builder
	for _, c := range chunks {
		a, b := t.Next(c)
		r.WriteString(a)
		th.WriteString(b)
	}
	a, b := t.Done()
	r.WriteString(a)
	th.WriteString(b)
	return r.String(), th.String()
}

func TestThinkStream(t *testing.T) {
	cases := []struct {
		name         string
		chunks       []string
		reply, think string
	}{
		{"no tags at all", []string{"She ", "did not ", "look up."}, "She did not look up.", ""},
		{"whole block in one chunk",
			[]string{"<think>I should be brief.</think>*She waited.*"},
			"*She waited.*", "I should be brief."},
		{"block split across chunks",
			[]string{"<thi", "nk>I sho", "uld be brief.</thi", "nk>*She waited.*"},
			"*She waited.*", "I should be brief."},
		{"one character at a time",
			splitEach("<think>hm</think>Fine."), "Fine.", "hm"},
		{"square brackets",
			[]string{"[THINK]", "reading the rules", "[/THINK]", `"Fine."`},
			`"Fine."`, "reading the rules"},
		{"leading whitespace before the tag",
			[]string{"\n\n", "<think>hm</think>", "Fine."}, "Fine.", "hm"},
		{"unclosed block is all deliberation",
			[]string{"<think>I should be brief and"}, "", "I should be brief and"},
		{"a tag mid-reply is left alone",
			[]string{`"What do you `, `<think>?" she asked.`},
			`"What do you <think>?" she asked.`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reply, think := feed(c.chunks...)
			if strings.TrimSpace(reply) != c.reply {
				t.Errorf("reply  = %q, want %q", reply, c.reply)
			}
			if strings.TrimSpace(think) != c.think {
				t.Errorf("thinking = %q, want %q", think, c.think)
			}
		})
	}
}

func splitEach(s string) []string {
	out := make([]string, 0, len(s))
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

// The point of the whole thing: nothing from inside the block is ever returned
// as reply, at any chunking. This is what stops a character sheet being read
// back aloud on screen.
func TestThinkStreamNeverLeaksAtAnyChunking(t *testing.T) {
	const full = "<think>The user wants me to roleplay Allison following specific " +
		"formatting rules strictly. Every sentence in quotes or asterisks." +
		"</think>*She looked up from the map.* \"You're late.\""
	const secret = "roleplay Allison"

	for size := 1; size <= len(full); size++ {
		var chunks []string
		for i := 0; i < len(full); i += size {
			chunks = append(chunks, full[i:min(i+size, len(full))])
		}
		reply, think := feed(chunks...)
		if strings.Contains(reply, secret) {
			t.Fatalf("chunk size %d leaked the deliberation into the reply:\n%q", size, reply)
		}
		if !strings.Contains(think, secret) {
			t.Fatalf("chunk size %d lost the deliberation: %q", size, think)
		}
		if want := `*She looked up from the map.* "You're late."`; strings.TrimSpace(reply) != want {
			t.Fatalf("chunk size %d gave reply %q, want %q", size, reply, want)
		}
	}
}

// Whatever arrives, the two halves together have to be what came in: this
// moves text between channels and must never drop or invent any.
func FuzzThinkStreamKeepsEverything(f *testing.F) {
	for _, seed := range []string{
		"", "plain text", "<think>a</think>b", "[THINK]a[/THINK]b", "<think>",
		"<thi", "</think>", "<think>a", "  <think>a</think>  b",
	} {
		f.Add(seed, 3)
	}
	f.Fuzz(func(t *testing.T, in string, size int) {
		if size < 1 || size > 64 {
			size = 1
		}
		var chunks []string
		for i := 0; i < len(in); i += size {
			chunks = append(chunks, in[i:min(i+size, len(in))])
		}
		reply, think := feed(chunks...)
		// Nothing is invented: every byte out came from the input.
		for _, part := range []string{reply, think} {
			if part != "" && !strings.Contains(in, strings.TrimSpace(part)) &&
				!strings.Contains(in, part) {
				t.Fatalf("output %q is not in the input %q", part, in)
			}
		}
		// And nothing is silently swallowed: what is not a tag comes back.
		if !startsWithThinkTag(in) && strings.TrimSpace(reply) != strings.TrimSpace(in) {
			t.Fatalf("no tag to act on but the reply changed:\n in: %q\nout: %q", in, reply)
		}
	})
}
