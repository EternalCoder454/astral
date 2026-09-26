//go:build linux

package app

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The stylesheets are the one part of Astral with no compiler. A misspelled
// property, a colour name that exists in one scheme and not the other, a
// keyframe GTK does not implement: all of them load without complaint and just
// do nothing, which is discovered by looking at the window and noticing that
// something is not happening.
//
// So they are parsed here. GTK's own parser is the only authority on what it
// accepts, and it says so on stderr.

// cssError loads css through GTK and returns whatever GTK complained about.
//
// It reads stderr rather than connecting to CSSProvider's parsing-error
// signal, which would be the obvious way round. Connecting that signal through
// gotk4 aborts the process inside the first error's handler, so a test built on
// it fails by killing the test binary.
func cssError(t *testing.T, css string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	saved, err := syscall.Dup(syscall.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Dup2(int(w.Fd()), syscall.Stderr); err != nil {
		t.Fatal(err)
	}

	gtk.NewCSSProvider().LoadFromString(css)

	if err := syscall.Dup2(saved, syscall.Stderr); err != nil {
		t.Fatal(err)
	}
	syscall.Close(saved)
	w.Close()
	out, _ := io.ReadAll(r)
	return strings.TrimSpace(string(out))
}

// TestStylesheetsParse checks both schemes. The colour file is prepended to the
// structural one because that is how the two are combined at runtime.
//
// It does not prove the colours resolve: GTK looks a @named colour up when it
// draws, not when it parses, so an undefined one is silently transparent.
// TestEveryColourIsDefinedInBothSchemes covers that.
func TestStylesheetsParse(t *testing.T) {
	if !gtk.InitCheck() {
		t.Skip("no display; GTK cannot parse a stylesheet without one")
	}
	assets := filepath.Join("..", "..", "assets")
	structure := readAsset(t, filepath.Join(assets, "style.css"))
	for _, scheme := range []string{"dark.css", "light.css"} {
		t.Run(scheme, func(t *testing.T) {
			colors := readAsset(t, filepath.Join(assets, scheme))
			if got := cssError(t, colors+"\n"+structure); got != "" {
				t.Errorf("GTK rejected part of the stylesheet:\n%s", got)
			}
		})
	}
}

// TestCSSErrorDetectsAProblem is the calibration: a test that reads stderr is
// worth nothing if the capture silently misses what GTK writes, and it would
// then pass on any stylesheet at all.
func TestCSSErrorDetectsAProblem(t *testing.T) {
	if !gtk.InitCheck() {
		t.Skip("no display; GTK cannot parse a stylesheet without one")
	}
	if got := cssError(t, `.x { no-such-property: 4px; }`); got == "" {
		t.Fatal("a stylesheet with an unknown property parsed without complaint, so this test cannot see errors at all")
	}
}

// TestEveryColourIsDefinedInBothSchemes is the check the parser will not do.
//
// The colours live in a second stylesheet so the scheme can be swapped while
// the window is open, and the cost of that is two files that have to agree.
// A colour added to one and forgotten in the other draws as transparent,
// silently, in one theme only.
func TestEveryColourIsDefinedInBothSchemes(t *testing.T) {
	assets := filepath.Join("..", "..", "assets")
	used := colorsUsed(readAsset(t, filepath.Join(assets, "style.css")))
	if len(used) < 5 {
		t.Fatalf("only found %d colour references in style.css, so the pattern is wrong", len(used))
	}
	for _, scheme := range []string{"dark.css", "light.css"} {
		defined := colorsDefined(readAsset(t, filepath.Join(assets, scheme)))
		for _, name := range used {
			if !defined[name] {
				t.Errorf("style.css uses @%s, which %s does not define", name, scheme)
			}
		}
	}
}

var (
	colorUse = regexp.MustCompile(`@([a-zA-Z_][a-zA-Z0-9_-]*)`)
	colorDef = regexp.MustCompile(`@define-color\s+([a-zA-Z_][a-zA-Z0-9_-]*)`)
)

// cssAtRules are the @ keywords that are not colour references.
var cssAtRules = map[string]bool{
	"define-color": true, "keyframes": true, "import": true, "media": true,
	"supports": true, "font-face": true, "charset": true, "namespace": true,
}

func colorsUsed(css string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range colorUse.FindAllStringSubmatch(css, -1) {
		if cssAtRules[m[1]] || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}

func colorsDefined(css string) map[string]bool {
	out := map[string]bool{}
	for _, m := range colorDef.FindAllStringSubmatch(css, -1) {
		out[m[1]] = true
	}
	return out
}

func readAsset(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestNoUnusedCSSClasses is the other half of the dead-code problem. Go has
// staticcheck; a stylesheet has nothing, so a rule for a widget that was removed
// or renamed stays in the file forever, and the next person reading it has to
// work out whether it matters. Three rules for an Ollama status dot lived here
// long after the dot became a chip in the header.
//
// It only checks the direction that can be decided: every class the stylesheet
// styles must be a class some Go file applies. The other direction is not a
// fault, because libadwaita defines plenty of classes worth using unstyled.
func TestNoUnusedCSSClasses(t *testing.T) {
	css := readAsset(t, filepath.Join("..", "..", "assets", "style.css"))
	applied := classesAppliedInGo(t, filepath.Join("..", ".."))
	for _, class := range classesStyled(css) {
		if !applied[class] {
			t.Errorf(".%s is styled but nothing applies it", class)
		}
	}
}

// classesStyled returns the classes named in selectors. Comments are stripped
// first: a class name in prose is not a rule, and the file explains itself at
// length.
var (
	cssComment  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssSelector = regexp.MustCompile(`([^{}]*)\{`)
	cssClass    = regexp.MustCompile(`\.([a-zA-Z][a-zA-Z0-9_-]*)`)
)

func classesStyled(css string) []string {
	seen := map[string]bool{}
	var out []string
	for _, sel := range cssSelector.FindAllStringSubmatch(cssComment.ReplaceAllString(css, ""), -1) {
		for _, m := range cssClass.FindAllStringSubmatch(sel[1], -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

// classesAppliedInGo collects every short lower-case string literal in the Go
// sources. It is deliberately loose: the question asked of it is only whether a
// class name appears at all, and a name that appears for some other reason costs
// nothing but a missed report.
func classesAppliedInGo(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	lit := regexp.MustCompile(`"([a-zA-Z][a-zA-Z0-9_-]*)"`)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range lit.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 50 {
		t.Fatalf("only found %d string literals in the sources, so the walk is wrong", len(out))
	}
	return out
}
