//go:build linux && !race

package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/theme"
)

// The stylesheets have no compiler: a misspelled property or an unimplemented
// keyframe loads without complaint and does nothing. GTK's parser is the only
// authority on what it accepts, and it says so on stderr.
//
// The build tag keeps this out of -race, which turns on checkptr, which aborts
// inside gotk4's weak-reference dependency the moment GTK is initialised. No
// test in this project can touch GTK under the race detector.

// noDisplayExit is how the child process reports that it could not start GTK,
// which is not a failing stylesheet.
const noDisplayExit = 3

// cssSchemeEnv names what the child process should parse: "theme:" and a theme's
// ID, or a literal stylesheet. Its presence is also what tells the helper test
// that it is the child.
const cssSchemeEnv = "ASTRAL_CSS_PARSE_SCHEME"

// themePrefix marks a scheme that is a theme's ID rather than a literal.
const themePrefix = "theme:"

// parseInChild parses a stylesheet in a subprocess and returns what GTK wrote
// to stderr.
//
// A subprocess because CSSProvider's parsing-error signal aborts when connected
// through gotk4, and because capturing this process's own stderr means
// redirecting fd 2, which swallows the crash report when anything goes wrong. A
// child has a stderr of its own by construction.
func parseInChild(t *testing.T, scheme string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestCSSParseHelper")
	cmd.Env = append(os.Environ(), cssSchemeEnv+"="+scheme)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ex, ok := err.(*exec.ExitError); ok && ex.ExitCode() == noDisplayExit {
		t.Skip("no display; GTK cannot parse a stylesheet without one")
	}
	return strings.TrimSpace(stderr.String())
}

// TestCSSParseHelper is the child half of parseInChild. It is a test only so
// that it sits inside a binary the parent can re-run.
func TestCSSParseHelper(t *testing.T) {
	scheme := os.Getenv(cssSchemeEnv)
	if scheme == "" {
		t.Skip("not the child process")
	}
	if !gtk.InitCheck() {
		os.Exit(noDisplayExit)
	}
	css := scheme // a literal stylesheet, for calibration
	if id, ok := strings.CutPrefix(scheme, themePrefix); ok {
		th, ok := theme.ByID(id)
		if !ok {
			t.Fatalf("no theme %q", id)
		}
		// What the app loads: the theme's colours, and the structural sheet with
		// the picker's generated circles appended to it.
		css = th.CSS() + "\n" + readAsset(t, filepath.Join("..", "..", "assets", "style.css")) +
			"\n" + theme.SwatchCSS()
	}
	gtk.NewCSSProvider().LoadFromString(css)
	os.Exit(0)
}

// TestStylesheetsParse checks every theme. The colours are prepended to the
// structural sheet because that is how the two are combined at runtime.
//
// It does not prove the colours resolve: GTK looks a @named colour up when it
// draws, not when it parses, so an undefined one is silently transparent.
// TestEveryColourIsDefinedInEveryTheme covers that.
func TestStylesheetsParse(t *testing.T) {
	for _, th := range theme.Themes {
		t.Run(th.ID, func(t *testing.T) {
			if got := parseInChild(t, themePrefix+th.ID); got != "" {
				t.Errorf("GTK rejected part of the stylesheet:\n%s", got)
			}
		})
	}
}

// TestCSSErrorDetectsAProblem is the calibration: a test that reads a child
// process's stderr is worth nothing if it misses what GTK writes there, and it
// would then pass on any stylesheet at all.
func TestCSSErrorDetectsAProblem(t *testing.T) {
	if got := parseInChild(t, `.x { no-such-property: 4px; }`); got == "" {
		t.Fatal("a stylesheet with an unknown property parsed without complaint, so this test cannot see errors at all")
	}
}
