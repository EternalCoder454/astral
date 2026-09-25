package ui

import (
	"strings"
	"testing"
)

func TestMarkupGolden(t *testing.T) {
	cases := []struct {
		name string
		in   string
		mode Prose
		want string
	}{
		{"plain", "hello", Plain, "hello"},
		{"bold", "**hi**", Plain, "<b>hi</b>"},
		{"italic", "*hi*", Plain, "<i>hi</i>"},
		{"code", "`x`", Plain, "<tt>x</tt>"},
		{"heading", "## Title", Plain, "<b>Title</b>"},
		{"bullet", "- one", Plain, "• one"},
		{"indent kept", "    - one", Plain, "    • one"},
		{"escapes", "a < b & c", Plain, "a &lt; b &amp; c"},
		// Bold must win over italic, or "**x**" renders as "<i>" plus a stray star.
		{"bold before italic", "**x**", Plain, "<b>x</b>"},
		{"unclosed marker is literal", "a * b", Plain, "a * b"},
		{"fence dropped", "```\ncode\n```", Plain, "<tt>code</tt>"},
		{"no inline markup inside fence", "```\n**x**\n```", Plain, "<tt>**x**</tt>"},

		// Roleplay mode: everything internal is italic and stepped back;
		// speech and sounds are left completely plain.
		{"rp narration", "*he smiles*", Roleplay, `<span alpha="66%"><i>he smiles</i></span>`},
		{"rp underscore narration", "_he waits_", Roleplay, `<span alpha="66%"><i>he waits</i></span>`},
		{"rp speech is weighted", `"hello"`, Roleplay, `"<span weight="600">hello</span>"`},
		{"rp sound is untouched", "thud", Roleplay, "thud"},
		{"rp mixed line", `*She looked up.* "Fine."`, Roleplay,
			`<span alpha="66%"><i>She looked up.</i></span> "<span weight="600">Fine.</span>"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Markup(c.in, c.mode); got != c.want {
				t.Errorf("Markup(%q)\n got: %s\nwant: %s", c.in, got, c.want)
			}
		})
	}
}

// TestEscapeOrdering guards the one bug a naive implementation always has:
// replacing "&" after "<" re-escapes the ampersands the first pass introduced.
func TestEscapeOrdering(t *testing.T) {
	if got, want := Escape("<&>"), "&lt;&amp;&gt;"; got != want {
		t.Errorf("Escape(%q) = %q, want %q", "<&>", got, want)
	}
	if got, want := Escape("&lt;"), "&amp;lt;"; got != want {
		t.Errorf("Escape(%q) = %q, want %q", "&lt;", got, want)
	}
}

// allowedTags is every tag Markup is permitted to emit. Anything else in the
// output means model text reached the markup parser as markup.
var allowedTags = map[string]bool{
	"<b>": true, "</b>": true,
	"<i>": true, "</i>": true,
	"<tt>": true, "</tt>": true,
	"</span>":             true,
	`<span alpha="66%">`:  true,
	`<span weight="600">`: true,
}

// TestNoMarkupInjection is the property that actually matters: whatever a model
// writes, the only tags in the rendered output are ours. A model that emits
// <span foreground="red"> or a raw &#60; must come out as visible text, not as
// markup — otherwise a character card could style, or break, the transcript.
func TestNoMarkupInjection(t *testing.T) {
	hostile := []string{
		`<span foreground="red">red</span>`,
		`<b>not bold</b>`,
		"&lt;b&gt;escaped&lt;/b&gt;",
		"&#60;script&#62;",
		"<<<>>>",
		"*<i>mixed</i>*",
		"`<tt>`",
		"**<b>x</b>**",
		`"<span weight='900'>loud</span>"`,
		"<img src=x>",
		strings.Repeat("<b>", 50),
		"&&&<<<",
	}
	for _, mode := range []Prose{Plain, Roleplay} {
		for _, in := range hostile {
			out := Markup(in, mode)
			for _, tag := range extractTags(out) {
				if !allowedTags[tag] {
					t.Errorf("mode=%v input %q produced disallowed tag %q\nfull: %s",
						mode, in, tag, out)
				}
			}
		}
	}
}

// FuzzMarkupTagsAreOurs runs the same property over arbitrary input.
func FuzzMarkupTagsAreOurs(f *testing.F) {
	for _, seed := range []string{
		"hello", "**b**", "*i*", "`c`", "<b>", "&", "<", ">",
		`"speech"`, "*action*", "```\nfence\n```", "&amp;",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, mode := range []Prose{Plain, Roleplay} {
			out := Markup(in, mode)
			for _, tag := range extractTags(out) {
				if !allowedTags[tag] {
					t.Fatalf("mode=%v input %q produced disallowed tag %q", mode, in, tag)
				}
			}
		}
	})
}

// extractTags returns every "<...>" span in s.
func extractTags(s string) []string {
	var tags []string
	for {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			return tags
		}
		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			// An unclosed '<' is itself a failure: it means something was not
			// escaped, so report it rather than stopping quietly.
			return append(tags, s[i:])
		}
		tags = append(tags, s[i:i+j+1])
		s = s[i+j+1:]
	}
}

func TestSnippet(t *testing.T) {
	cases := []struct{ in, want string }{
		{"*he smiles* \"hello\"", `he smiles "hello"`},
		{"# Heading\n\nbody", "Heading body"},
		{"  lots   of    space  ", "lots of space"},
	}
	for _, c := range cases {
		if got := Snippet(c.in, 0); got != c.want {
			t.Errorf("Snippet(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := Snippet("abcdefghij", 5); got != "abcde…" {
		t.Errorf("Snippet truncation = %q", got)
	}
}

func TestAccentForIsStableAndInRange(t *testing.T) {
	for _, name := range []string{"Vesper", "Aria", "", "Wren", "日本語"} {
		a := AccentFor(name)
		if a < 0 || a >= AccentCount {
			t.Errorf("AccentFor(%q) = %d, out of range", name, a)
		}
		if b := AccentFor(name); a != b {
			t.Errorf("AccentFor(%q) not stable: %d then %d", name, a, b)
		}
	}
}

// TestQuoteRuleCannotRewriteOurOwnTags is a regression guard.
//
// The italic rule emits <span alpha="66%">, and that attribute value is
// wrapped in quote characters. Run the quote rule after it and the regexp
// matches the "66%" inside a tag this package just produced, rewriting the
// middle of it into markup Pango cannot parse. That shipped once. The fix is
// that quotes run first, and this holds the ordering in place.
func TestQuoteRuleCannotRewriteOurOwnTags(t *testing.T) {
	cases := []string{
		`*She said "hi" softly.*`,
		`"Quoted," *then narration,* "more speech."`,
		`*narration with "speech" inside and _underscores_ too*`,
	}
	for _, in := range cases {
		out := Markup(in, Roleplay)
		// The alpha value must survive intact; if the quote rule reached it,
		// a weight span appears inside the attribute.
		if strings.Contains(out, `alpha="<span`) || strings.Contains(out, `weight="<span`) {
			t.Errorf("a tag was rewritten from inside:\n in:  %s\n out: %s", in, out)
		}
		if !strings.Contains(out, `alpha="66%"`) {
			t.Errorf("the alpha attribute did not survive:\n in:  %s\n out: %s", in, out)
		}
		for _, tag := range extractTags(out) {
			if !allowedTags[tag] {
				t.Errorf("input %q produced disallowed tag %q\nfull: %s", in, tag, out)
			}
		}
	}
}

// Narration and speech must be visibly different, because that difference is
// the whole reading grammar of a transcript. An earlier version set narration
// at 92% alpha, which left it all but identical to speech.
func TestNarrationAndSpeechAreDistinguished(t *testing.T) {
	out := Markup(`*She looked up.* "Fine."`, Roleplay)
	if !strings.Contains(out, `<span alpha="66%"><i>She looked up.</i></span>`) {
		t.Errorf("narration is not set back:\n%s", out)
	}
	if !strings.Contains(out, `<span weight="600">Fine.</span>`) {
		t.Errorf("speech is not weighted:\n%s", out)
	}
}
