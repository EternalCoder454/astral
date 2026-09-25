package ui

import (
	"regexp"
	"strings"
)

// Rendering model output as Pango markup means turning untrusted text into
// something a GtkLabel will parse as markup. The invariant that makes that
// safe is simple and absolute: **escape the literal text first, then insert
// tags**. Every path below does that, so the only angle brackets in the output
// are the handful of tags this file generates. markdown_test.go asserts exactly
// that property, which is what keeps a model unable to inject markup by
// writing <span> or &#60; in its reply.
var (
	mdCode   = regexp.MustCompile("`([^`]+)`")
	mdBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalic = regexp.MustCompile(`\*([^*]+)\*`)
	mdUnder  = regexp.MustCompile(`_([^_]+)_`)
	// Dialogue in straight or typographic quotes. It will not span a line, so
	// an unclosed quote affects one line rather than swallowing the rest of
	// the reply.
	mdQuote = regexp.MustCompile(`"([^"\n]*)"|“([^”\n]*)”`)
)

// A single Replacer rather than three sequential ReplaceAll passes: replacing
// "&" first and "<" second would re-escape the ampersands the second pass
// introduced. One walk cannot have that ordering bug.
var pangoEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// Escape makes s safe to place inside Pango markup.
func Escape(s string) string { return pangoEscaper.Replace(s) }

// Prose selects how a message body is rendered.
type Prose int

const (
	// Plain is ordinary Markdown: bold, italic, code, bullets, headings.
	Plain Prose = iota
	// Roleplay reads the genre's conventions: *asterisks* wrap everything
	// internal — narration, action, body language, thought — and "quotes" are
	// what was said out loud. Narration is set in a grey italic and speech is
	// weighted, so a long scene can be skimmed for what actually happened
	// rather than read start to finish.
	//
	// This is the one piece of styling that carries meaning rather than
	// decoration, which is why it is a mode and not a theme choice.
	Roleplay
)

// Markup converts Markdown (and, in Roleplay mode, roleplay prose) to Pango
// markup suitable for GtkLabel.SetMarkup.
func Markup(md string, mode Prose) string {
	lines := strings.Split(strings.TrimRight(md, "\n"), "\n")
	out := make([]string, 0, len(lines))
	inCode := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode // drop the fence itself, toggle the mode
			continue
		}
		if inCode {
			// Each line gets its own <tt>: a single tag spanning the block
			// would have to survive the newlines between them, and per-line
			// tagging renders identically without that fragility.
			out = append(out, "<tt>"+Escape(line)+"</tt>")
			continue
		}
		out = append(out, mdLine(line, mode))
	}
	return strings.Join(out, "\n")
}

// mdLine renders one line, preserving its indent.
func mdLine(line string, mode Prose) string {
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]

	bullet := ""
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		bullet = "• "
		trimmed = trimmed[2:]
	}
	heading := false
	for strings.HasPrefix(trimmed, "#") {
		heading = true
		trimmed = trimmed[1:]
	}
	if heading {
		trimmed = strings.TrimLeft(trimmed, " ")
	}

	content := mdInline(Escape(trimmed), mode) // escape, THEN tag
	if heading {
		content = "<b>" + content + "</b>"
	}
	return indent + bullet + content
}

// mdInline applies the inline rules to already-escaped text.
//
// Order matters twice over. Code goes first so its contents are not re-marked,
// then bold before italic — otherwise the single-asterisk rule eats half of a
// **pair**.
//
// Quotes must then run BEFORE italic. The italic rule emits an alpha span, and
// that attribute's value is itself wrapped in quote characters: a quote rule
// running afterwards matches the `"66%"` inside a tag this function just
// produced and rewrites the middle of it. That is not a hypothetical — it
// shipped once, and the fix is this ordering.
func mdInline(s string, mode Prose) string {
	s = mdCode.ReplaceAllString(s, "<tt>$1</tt>")
	s = mdBold.ReplaceAllString(s, "<b>$1</b>")
	if mode == Roleplay {
		// Speech carries the line: full strength and weighted.
		s = mdQuote.ReplaceAllStringFunc(s, quoteSpan)
		// Narration, action and thought step back behind it.
		//
		// How far back is the whole question. An earlier version put this at
		// 92%, which reads beautifully in isolation and is nearly useless in
		// practice: almost all of a roleplay reply is narration, so at that
		// level a message is one undifferentiated block and the eye has
		// nothing to catch on. 66% keeps it at 5.7:1 — comfortably legible —
		// while leaving speech 1.9x brighter, which is what makes a scene
		// skimmable.
		s = mdItalic.ReplaceAllString(s, `<span alpha="66%"><i>$1</i></span>`)
		s = mdUnder.ReplaceAllString(s, `<span alpha="66%"><i>$1</i></span>`)
		return s
	}
	s = mdItalic.ReplaceAllString(s, "<i>$1</i>")
	return s
}

// quoteSpan re-emits a matched quotation with its quote characters intact and
// the speech inside it weighted. The regexp has two alternatives (straight and
// typographic), so which one fired has to be found rather than assumed.
func quoteSpan(m string) string {
	if len(m) < 2 {
		return m
	}
	if strings.HasPrefix(m, "\u201c") {
		inner := strings.TrimSuffix(strings.TrimPrefix(m, "\u201c"), "\u201d")
		return "\u201c" + `<span weight="600">` + inner + "</span>\u201d"
	}
	open, close := m[:1], m[len(m)-1:]
	return open + `<span weight="600">` + m[1:len(m)-1] + `</span>` + close
}

// Snippet collapses a message to a single line for the sidebar, stripping the
// markup characters rather than rendering them — a preview row is scanned, not
// read, and asterisks in it are noise.
//
// It is a single pass that stops as soon as it has max runes. The obvious
// implementation — strip, then strings.Fields, then Join — walks the entire
// message and allocates a slice of every word in it, to produce sixty
// characters. On a long roleplay reply that is most of a kilobyte of garbage
// per sidebar row, per rebuild.
func Snippet(s string, max int) string {
	var b strings.Builder
	if max > 0 {
		b.Grow(max + 4)
	} else {
		b.Grow(len(s))
	}
	pendingSpace := false
	n := 0
	for _, r := range s {
		switch r {
		case '*', '_', '`', '#':
			continue
		case ' ', '\t', '\n', '\r':
			// Collapsed, and never leading: a run of whitespace becomes at
			// most one space, and only once something has been written.
			pendingSpace = b.Len() > 0
			continue
		}
		if pendingSpace {
			if max > 0 && n >= max {
				return b.String() + "…"
			}
			b.WriteByte(' ')
			n++
			pendingSpace = false
		}
		if max > 0 && n >= max {
			return b.String() + "…"
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
