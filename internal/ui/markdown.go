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
	// RoleplayAsWritten is the same genre with the inference switched off: what
	// the author marked is narration, and what they did not is left alone.
	//
	// It is what your own messages are rendered with. Inferring narration from
	// everything outside quotation marks exists to cover for a model that
	// forgets its asterisks, and a person typing into the composer is not
	// something to cover for: they put the asterisks where they meant them, and
	// dimming the rest of what they wrote is the renderer overruling them.
	RoleplayAsWritten
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
// Order matters: code goes first so its contents are not re-marked, then bold
// before italic, or the single-asterisk rule eats half of a **pair**.
func mdInline(s string, mode Prose) string {
	switch mode {
	case Roleplay:
		return mdRoleplay(s, mdNarration)
	case RoleplayAsWritten:
		return mdRoleplay(s, mdAsWritten)
	}
	s = mdCode.ReplaceAllString(s, "<tt>$1</tt>")
	s = mdBold.ReplaceAllString(s, "<b>$1</b>")
	s = mdItalic.ReplaceAllString(s, "<i>$1</i>")
	return s
}

// mdRoleplay renders one line of roleplay prose.
//
// It splits on quotation marks rather than on asterisks, which is the change
// that makes the transcript stop depending on the model. In this genre
// everything outside quotes is narration by definition, whether or not the
// model remembered to wrap it in asterisks — and measured over twenty-turn
// scenes it often does not: around seventy per cent of replies came back
// carrying unmarked narration, however the prompt was worded, because the
// recap and the lorebook in the context are themselves flat unmarked prose.
//
// Three attempts to fix that in the prompt did not measurably work. So the
// renderer stopped asking. Speech is what sits inside quotes; everything else
// is narration and is styled as narration, and a reply that forgot its
// asterisks now reads exactly like one that remembered.
//
// It also retires a hazard rather than working around it. The previous version
// ran regexps over its own output, which meant the quote rule could match the
// `"66%"` inside an alpha tag the italic rule had just written and rewrite the
// middle of it. That shipped once. A single pass over the source, splitting
// before any tag exists, cannot do it at all.
// outside renders a run of text that sits between quotations: either as
// narration wholesale, or as written.
func mdRoleplay(s string, outside func(string) string) string {
	var b strings.Builder
	b.Grow(len(s) + 48)
	last := 0
	for _, loc := range mdQuote.FindAllStringIndex(s, -1) {
		b.WriteString(outside(s[last:loc[0]]))
		b.WriteString(quoteSpan(s[loc[0]:loc[1]]))
		last = loc[1]
	}
	b.WriteString(outside(s[last:]))
	return b.String()
}

// mdAsWritten styles only what the author marked, and leaves the rest as body
// text. Speech is still weighted, because the quotation marks are theirs too.
func mdAsWritten(s string) string {
	inner := mdCode.ReplaceAllString(s, "<tt>$1</tt>")
	inner = mdBold.ReplaceAllString(inner, "<b>$1</b>")
	// Marked narration gets the same treatment the model's gets, so a scene
	// reads as one conversation rather than two typefaces. Nothing else is
	// touched.
	inner = mdItalic.ReplaceAllString(inner, narrationOpen+"$1"+narrationClose)
	inner = mdUnder.ReplaceAllString(inner, narrationOpen+"$1"+narrationClose)
	return inner
}

// mdNarration styles a run of text that sits outside quotation marks.
//
// How far the colour steps back is the whole question. An earlier version put
// it at 92%, which reads beautifully in isolation and is nearly useless in
// practice: almost all of a roleplay reply is narration, so at that level a
// message is one undifferentiated block and the eye has nothing to catch on.
// 66% keeps it at 5.7:1, comfortably legible, while leaving speech 1.9x
// brighter, which is what makes a scene skimmable.
func mdNarration(s string) string {
	if strings.TrimSpace(s) == "" {
		return s // whitespace between two quoted lines needs no tag
	}
	// The spaces on either side stay outside the tag. Italicising the gap
	// between a piece of narration and the speech after it is invisible in
	// most fonts and wrong in the ones where it is not.
	lead := s[:len(s)-len(strings.TrimLeft(s, " \t"))]
	trail := s[len(strings.TrimRight(s, " \t")):]
	s = s[len(lead) : len(s)-len(trail)]

	inner := mdCode.ReplaceAllString(s, "<tt>$1</tt>")
	inner = mdBold.ReplaceAllString(inner, "<b>$1</b>")
	// The asterisks were the author saying "this is narration", and all of
	// this is narration, so the markers come out rather than nesting a second
	// identical span inside the first.
	inner = mdItalic.ReplaceAllString(inner, "$1")
	inner = mdUnder.ReplaceAllString(inner, "$1")
	return lead + narrationOpen + inner + narrationClose + trail
}

// The narration tags, named because two renderers emit them and a second copy
// of the alpha value is a second thing to keep in step with the stylesheet.
const (
	narrationOpen  = `<span alpha="66%"><i>`
	narrationClose = `</i></span>`
)

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
