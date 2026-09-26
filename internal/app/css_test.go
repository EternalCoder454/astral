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

// These two need no GTK, so unlike the parse tests next door they run
// everywhere, including under the race detector.

func readAsset(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
