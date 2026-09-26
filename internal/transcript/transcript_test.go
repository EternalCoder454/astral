package transcript

import (
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
	"astral/internal/store"
)

func TestMarkdown(t *testing.T) {
	ch := store.Chat{
		Title: "The tide came in early", CharacterName: "Vesper Quill",
		Model: "qwen3.6:27b", Summary: "Vesper admitted she has never left the city.",
		CreatedAt: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC),
	}
	msgs := []store.Message{
		{Role: ollama.RoleAssistant, Content: `*She does not look up.* "You're late."`},
		{Role: ollama.RoleUser, Content: `"The ferry was held."`},
		{Role: ollama.RoleAssistant, Content: "   "}, // empty turns are skipped
	}
	got := Markdown(ch, msgs, "Vesper Quill", "Wren")

	for _, want := range []string{
		"# The tide came in early",
		"with Vesper Quill",
		"26 September 2026",
		"## What happened earlier",
		"never left the city",
		"**Vesper Quill**",
		"**Wren**",
		`"The ferry was held."`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the transcript is missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "**Vesper Quill**") != 1 {
		t.Errorf("a blank turn was written out:\n%s", got)
	}
}

// A scene with nothing in it is still a file, not a crash.
func TestMarkdownWithNothing(t *testing.T) {
	got := Markdown(store.Chat{}, nil, "", "")
	if !strings.Contains(got, "# A scene") {
		t.Errorf("no title: %q", got)
	}
}

func TestFilename(t *testing.T) {
	day := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	cases := []struct{ title, want string }{
		{"The tide came in early", "The-tide-came-in-early-2026-09-26"},
		{"Vesper: the harbour / part 2", "Vesper-the-harbour-part-2-2026-09-26"},
		{"   ", "astral-scene-2026-09-26"},
		{"???", "astral-scene-2026-09-26"},
		{"日本語", "astral-scene-2026-09-26"},
	}
	for _, c := range cases {
		got := Filename(store.Chat{Title: c.title, UpdatedAt: day})
		if got != c.want {
			t.Errorf("Filename(%q) = %q, want %q", c.title, got, c.want)
		}
		// Whatever the title, the result has to be usable as a filename.
		if strings.ContainsAny(got, `/\:*?"<>| `) {
			t.Errorf("Filename(%q) = %q, which is not a safe name", c.title, got)
		}
	}
}

// A very long title must not become a very long filename.
func TestFilenameIsBounded(t *testing.T) {
	got := Filename(store.Chat{Title: strings.Repeat("harbour ", 40)})
	if len(got) > 80 {
		t.Errorf("filename is %d characters: %q", len(got), got)
	}
	if strings.HasSuffix(strings.TrimSuffix(got, time.Now().Format("2006-01-02")), "--") {
		t.Errorf("filename has a trailing dash before the date: %q", got)
	}
}
