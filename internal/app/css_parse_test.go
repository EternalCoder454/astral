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

// The stylesheets are the one part of Astral with no compiler. A misspelled
// property, a keyframe GTK does not implement, a rule for a widget that no
// longer exists: all of them load without complaint and just do nothing, which
// is otherwise discovered by looking at the window and noticing that something
// is not happening.
//
// So they are parsed here. GTK's own parser is the only authority on what it
// accepts, and it says so on stderr.
//
// Not under the race detector, which is what the build tag is for. -race turns
// on checkptr, and checkptr aborts inside gotk4's weak-reference dependency the
// moment GTK is initialised: "pointer arithmetic result points to invalid
// allocation", in a library Astral only depends on. So no test in this project
// can touch GTK under -race, and the ones that do are tagged out of it rather
// than left to fail there for a reason that is nothing to do with Astral.

// noDisplayExit is how the child process reports that it could not start GTK,
// which is not a failing stylesheet.
const noDisplayExit = 3

// cssSchemeEnv names what the child process should parse. Its presence is also
// what tells the helper test that it is the child.
const cssSchemeEnv = "ASTRAL_CSS_PARSE_SCHEME"

// parseInChild parses a stylesheet in a subprocess and returns what GTK wrote to
// stderr.
//
// A subprocess rather than a CSSProvider in this process, for two reasons that
// each cost a debugging round. CSSProvider's parsing-error signal cannot be
// connected through gotk4 without aborting inside the first error's handler. And
// capturing this process's own stderr means redirecting file descriptor 2, which
// is global state in a test binary: under the race detector that crashed, and
// the crash report went into the pipe that was supposed to hold GTK's
// complaints, so the failure arrived as a silent non-zero exit.
//
// A child process has a stderr of its own by construction.
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
