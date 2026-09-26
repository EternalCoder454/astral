// Package transcript turns a scene into something you can keep.
//
// A roleplay is writing, and writing that only exists inside one application's
// database is writing you do not really have. This is the way out: Markdown,
// because it is readable as a plain file and still a document if you want one.
package transcript

import (
	"fmt"
	"strings"
	"time"

	"astral/internal/ollama"
	"astral/internal/store"
)

// Markdown renders a scene. who is the character's name and you is the
// persona's, since the transcript stores roles rather than names.
func Markdown(ch store.Chat, msgs []store.Message, who, you string) string {
	if who = strings.TrimSpace(who); who == "" {
		who = "Them"
	}
	if you = strings.TrimSpace(you); you == "" {
		you = "You"
	}

	var b strings.Builder
	title := strings.TrimSpace(ch.Title)
	if title == "" {
		title = "A scene"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)

	var meta []string
	if n := strings.TrimSpace(ch.CharacterName); n != "" {
		meta = append(meta, "with "+n)
	}
	if !ch.CreatedAt.IsZero() {
		meta = append(meta, ch.CreatedAt.Format("2 January 2006"))
	}
	if m := strings.TrimSpace(ch.Model); m != "" {
		meta = append(meta, m)
	}
	if len(meta) > 0 {
		fmt.Fprintf(&b, "*%s*\n\n", strings.Join(meta, " · "))
	}

	// The recap goes in, clearly marked. It is the only record of everything
	// that fell out of the context window, so a transcript without it is
	// missing the first half of a long scene.
	if recap := strings.TrimSpace(ch.Summary); recap != "" {
		b.WriteString("## What happened earlier\n\n")
		b.WriteString(recap)
		b.WriteString("\n\n")
	}

	b.WriteString("---\n\n")
	for _, m := range msgs {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		speaker := who
		if m.Role == ollama.RoleUser {
			speaker = you
		}
		fmt.Fprintf(&b, "**%s**\n\n%s\n\n", speaker, text)
	}
	return b.String()
}

// Filename is a safe name for a scene, without the extension.
//
// Anything a filesystem or a person would struggle with comes out as a dash,
// because a scene called "Vesper: the harbour / part 2" is a perfectly ordinary
// title and not a path.
func Filename(ch store.Chat) string {
	name := strings.TrimSpace(ch.Title)
	if name == "" {
		name = "astral-scene"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "astral-scene"
	}
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	if !ch.UpdatedAt.IsZero() {
		out += "-" + ch.UpdatedAt.Format("2006-01-02")
	} else {
		out += "-" + time.Now().Format("2006-01-02")
	}
	return out
}
