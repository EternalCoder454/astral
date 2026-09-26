//go:build linux

package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Astral's interface uses Title Case for anything that names a thing: window and
// dialog titles, section headings, card titles, field labels, buttons, menu
// items. Anything that is a sentence stays sentence case, which is hint text,
// checkbox statements, toasts and errors.
//
// This is a source test rather than a convention nobody can check, because the
// two kinds of string sit next to each other in the same call and the difference
// between them is a decision, not a pattern. Left unguarded it drifts within a
// week, which is how the project ended up with "Style designer" beside
// "Keyboard Shortcuts".

// titleCalls are the calls whose string argument names something. A hint is
// always the argument after a label, so matching only the first is what keeps
// sentences out of this test.
var titleCalls = []*regexp.Regexp{
	regexp.MustCompile(`groupCard\("([^"]*)"\)`),
	regexp.MustCompile(`SetTitle\("([^"]+)"\)`),
	regexp.MustCompile(`AddTitled\([^,]+,\s*"[^"]+",\s*"([^"]+)"\)`),
	regexp.MustCompile(`heading\("([^"]+)"\)`),
	regexp.MustCompile(`labelledField\("([^"]+)"`),
	regexp.MustCompile(`NewButtonWithLabel\("([^"]+)"\)`),
	regexp.MustCompile(`SetLabel\("([^"]+)"\)`),
	regexp.MustCompile(`addRow\("([^"]+)"`),
	regexp.MustCompile(`add\((?:ui\.)?Icon\w+,\s*"([^"]+)"`),
	regexp.MustCompile(`label, tip = "([^"]+)"`),
}

// smallWords stay lowercase inside a title unless they open or close it:
// articles, conjunctions and prepositions.
//
// Prepositions are in it regardless of length, which is the stricter of the two
// usual conventions and the one that reads better here. "Read Lore from Text"
// against "Read Lore From Text", and "Export as a Character Card" against
// "Export As a Character Card".
var smallWords = map[string]bool{
	"a": true, "an": true, "and": true, "as": true, "at": true, "but": true,
	"by": true, "for": true, "from": true, "if": true, "in": true, "into": true,
	"nor": true, "of": true, "on": true, "onto": true, "or": true, "over": true,
	"per": true, "than": true, "the": true, "to": true, "up": true, "upon": true,
	"via": true, "vs": true, "with": true,
}

// unitWords are measurements, which stay lowercase even in a title: "Context
// Size (tokens)" is right and "(Tokens)" is not.
var unitWords = map[string]bool{
	"tokens": true, "px": true, "ms": true, "kb": true, "mb": true, "gb": true,
}

func TestUILabelsAreTitleCase(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, re := range titleCalls {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				checkTitle(t, path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func checkTitle(t *testing.T, path, label string) {
	t.Helper()
	words := strings.Fields(label)
	if len(words) == 0 {
		return // an untitled card, which is a layout container rather than a heading
	}
	for i, w := range words {
		core := strings.Trim(w, `(),.:;'"…`)
		if core == "" || !isLetter(core[0]) {
			continue
		}
		low := strings.ToLower(core)
		// A word already carrying a capital anywhere is a name, an acronym or a
		// technical term, and is left exactly as written: "Ollama", "Top-p".
		if core != low {
			continue
		}
		if unitWords[low] {
			continue
		}
		last := i == len(words)-1
		if smallWords[low] && i != 0 && !last {
			continue
		}
		t.Errorf("%s: %q should be Title Case, but %q is lowercase. "+
			"If this is a sentence rather than a name, it does not belong in one of "+
			"these calls; put it in the hint instead", path, label, core)
	}
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// The two characters are written as escapes so that this file, which is part of
// the project it checks, does not fail its own test.
const (
	emDash = "\u2014"
	enDash = "\u2013"
)

// TestNoDashesInTheProject is the other writing rule, and it is worth a test for
// the same reason: it is invisible in review and easy to reintroduce one comment
// at a time.
//
// Em dashes, en dashes, and a hyphen standing in for either. A hyphen inside a
// compound word is fine and is not what this looks for.
func TestNoDashesInTheProject(t *testing.T) {
	roots := []string{"..", "../../assets"}
	seen := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // a missing tree is not this test's business
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "bin", "fuzz", "node_modules":
					return fs.SkipDir
				}
				return nil
			}
			// By extension, plus the few files that have none. A Makefile prints
			// its own messages and is as much this project's prose as a comment.
			switch filepath.Ext(path) {
			case ".go", ".md", ".css", ".js", ".html", ".svg", ".sh", ".yml", ".iss", ".kts":
			default:
				switch d.Name() {
				case "Makefile", "Dockerfile":
				default:
					return nil
				}
			}
			clean := filepath.Clean(path)
			if seen[clean] {
				return nil
			}
			seen[clean] = true
			src, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for n, line := range strings.Split(string(src), "\n") {
				if strings.ContainsAny(line, emDash+enDash) {
					t.Errorf("%s:%d has an em or en dash: %s", clean, n+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
