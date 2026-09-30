//go:build linux || darwin

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// installUpdate fetches the branch into the managed clone, reinstalls from it,
// and restarts in place; a Flatpak downloads ver's bundle instead. Progress
// goes to onStatus, whose second argument says whether the work has finished,
// successfully or not.
func (a *App) installUpdate(branch, ver string, onStatus func(text string, done bool)) {
	if inFlatpak() {
		go func() {
			err := a.flatpakUpdate(ver, func(text string, done bool) {
				coreglib.IdleAdd(func() bool { onStatus(text, done); return false })
			})
			coreglib.IdleAdd(func() bool {
				if err != nil {
					onStatus("The update did not finish: "+err.Error(), true)
					return false
				}
				onStatus("Updated, restarting Astral…", true)
				if e := a.restartFlatpak(); e != nil {
					onStatus("Installed, but restart Astral yourself to finish: "+e.Error(), true)
				}
				return false
			})
		}()
		return
	}
	go func() {
		out, err := exec.Command("bash", "-lc", updateScript(branch)).CombinedOutput()
		coreglib.IdleAdd(func() bool {
			if err != nil {
				onStatus("The update did not finish:\n"+tailLines(string(out), 400), true)
				return false
			}
			onStatus("Updated, restarting Astral…", true)

			exe, e := installedBinary()
			if e != nil {
				onStatus("Installed, but restart Astral yourself to finish: "+e.Error(), true)
				return false
			}
			// Replace this process with the freshly installed binary. On
			// success this never returns.
			if e := syscall.Exec(exe, []string{exe}, os.Environ()); e != nil {
				onStatus("Installed, but restart Astral yourself to finish: "+e.Error(), true)
			}
			return false
		})
	}()
}

// updateScript fetches branch into the updater's own clone and installs it.
//
// A login shell keeps go, make and git on PATH even when Astral was launched
// from the application grid, which does not inherit a terminal's environment.
//
// The build itself is install.sh's, when the version fetched has one: it
// finds a Go new enough to build with, downloading one of its own when the
// distribution's is too old, which make alone cannot.
//
// Only the latest version is kept, and not the binary the build leaves in the
// clone once it is installed: the clone was 42 megabytes, of which the whole
// history was ten and a second copy of the installed app was twenty-six. A
// clone made before this is made shallow by its next update.
func updateScript(branch string) string {
	src := canonicalSourceDir()
	parent := filepath.Dir(src)
	return fmt.Sprintf(`set -e
if [ ! -d %[1]q/.git ]; then
  rm -rf %[1]q
  mkdir -p %[4]q
  git clone --depth 1 --branch %[3]q %[2]q %[1]q
fi
git -C %[1]q fetch --depth 1 --prune origin %[3]q
git -C %[1]q checkout -B %[3]q FETCH_HEAD
git -C %[1]q reset --hard FETCH_HEAD
if [ -f %[1]q/install.sh ]; then
  bash %[1]q/install.sh build %[1]q
else
  make -C %[1]q install
fi
rm -f %[1]q/bin/astral
git -C %[1]q for-each-ref --format='%%(refname)' refs/remotes refs/tags | while read -r ref; do git -C %[1]q update-ref -d "$ref"; done
git -C %[1]q reflog expire --expire=now --all || true
git -C %[1]q gc --prune=now --quiet || true`, src, repoURL, branch, parent)
}

// canonicalSourceDir is the clone updates are built from. Under the user's
// data directory rather than wherever the app happens to have been built, so
// an update never touches a checkout someone is working in.
func canonicalSourceDir() string {
	base, err := os.UserHomeDir()
	if err != nil {
		base = os.TempDir()
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		base = xdg
	} else {
		base = filepath.Join(base, ".local", "share")
	}
	return filepath.Join(base, "astral", "src")
}

// installedBinary is the path this process should be replaced with after an
// update, which is the one `make install` writes.
func installedBinary() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	exe := filepath.Join(home, ".local", "bin", "astral")
	if _, err := os.Stat(exe); err != nil {
		return "", err
	}
	return exe, nil
}

// tailLines returns the trailing n characters, for reporting a failed build
// without pasting the whole log into a dialog.
func tailLines(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
