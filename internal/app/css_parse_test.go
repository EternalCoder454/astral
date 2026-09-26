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

// cssSchemeEnv names what the child process should parse. Its presence is also
// what tells the helper test that it is the child.
const cssSchemeEnv = "ASTRAL_CSS_PARSE_SCHEME"

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
	if strings.HasSuffix(scheme, ".css") {
		assets := filepath.Join("..", "..", "assets")
		css = readAsset(t, filepath.Join(assets, scheme)) + "\n" +
			readAsset(t, filepath.Join(assets, "style.css"))
	}
	gtk.NewCSSProvider().LoadFromString(css)
	os.Exit(0)
}

// TestStylesheetsParse checks both schemes. The colour file is prepended to the
// structural one because that is how the two are combined at runtime.
//
// It does not prove the colours resolve: GTK looks a @named colour up when it
// draws, not when it parses, so an undefined one is silently transparent.
// TestEveryColourIsDefinedInBothSchemes covers that.
func TestStylesheetsParse(t *testing.T) {
	for _, scheme := range []string{"dark.css", "light.css"} {
		t.Run(scheme, func(t *testing.T) {
			if got := parseInChild(t, scheme); got != "" {
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
