//go:build linux

package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Astral as a Flatpak.
//
// Building from source needs a libadwaita as new as the one Astral is written
// against, which most distributions do not ship yet: Linux Mint, Ubuntu's
// long-term releases and Debian among them. Those get Astral as a Flatpak,
// which brings its own GNOME libraries. A Flatpak cannot rebuild itself, so it
// updates by downloading the new bundle from the release and handing it to
// Flatpak on the host, which the sandbox is allowed to ask through
// flatpak-spawn (the manifest grants org.freedesktop.Flatpak for exactly this).

// flatpakInfo is the file Flatpak puts at the root of every sandbox.
const flatpakInfo = "/.flatpak-info"

// inFlatpak reports whether this is the Flatpak build.
func inFlatpak() bool {
	if os.Getenv("FLATPAK_ID") != "" {
		return true
	}
	_, err := os.Stat(flatpakInfo)
	return err == nil
}

// flatpakScope is the flatpak option naming the installation Astral was
// installed into: --user for the user's own, which is what the install script
// makes, and --system otherwise, which asks for an administrator's password
// on the host. Flatpak on the host is asked; the sandbox's own record of
// where the app lives is the fallback, for when it cannot be.
func flatpakScope() string {
	if _, err := hostFlatpak("info", "--user", appID); err == nil {
		return "--user"
	} else if _, ok := err.(*exec.ExitError); ok {
		return "--system"
	}
	f, err := os.Open(flatpakInfo)
	if err != nil {
		return "--user"
	}
	defer f.Close()
	return scopeFromInfo(f)
}

// scopeFromInfo reads the scope from a .flatpak-info file: the [Instance]
// section's app-path says where the app's files are, and a user installation
// keeps them under the home directory's .local/share/flatpak.
func scopeFromInfo(r io.Reader) string {
	sc := bufio.NewScanner(r)
	section := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Instance]" {
			continue
		}
		if path, ok := strings.CutPrefix(line, "app-path="); ok {
			if strings.Contains(path, "/.local/share/flatpak/") {
				return "--user"
			}
			return "--system"
		}
	}
	return "--user"
}

// flatpakBundleURL is where a release's Flatpak bundle is published.
// ASTRAL_UPDATE_BUNDLE_URL stands in for it when testing, with %s for the
// version, as ASTRAL_UPDATE_NOTES_URL does for the notes.
func flatpakBundleURL(ver string) string {
	if u := os.Getenv("ASTRAL_UPDATE_BUNDLE_URL"); u != "" {
		return strings.ReplaceAll(u, "%s", ver)
	}
	return fmt.Sprintf("%s/releases/download/v%s/astral-%s-x86_64.flatpak", projectURL, ver, ver)
}

// hostFlatpak runs flatpak on the host, outside the sandbox, and returns what
// it printed.
func hostFlatpak(args ...string) ([]byte, error) {
	return exec.Command("flatpak-spawn", append([]string{"--host", "flatpak"}, args...)...).CombinedOutput()
}

// flatpakUpdate downloads ver's bundle and installs it over this one. It runs
// on a background goroutine and reports through onStatus like installUpdate.
func (a *App) flatpakUpdate(ver string, onStatus func(text string, done bool)) error {
	if runtime.GOARCH != "amd64" {
		return fmt.Errorf("the Flatpak is built for x86_64 only, so download Astral from %s", projectURL)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	// The sandbox's cache directory is under the home directory on the host
	// too, at the same path, so flatpak on the host can read the file there.
	bundle := filepath.Join(cache, "astral-update.flatpak")
	defer os.Remove(bundle)
	if err := download(flatpakBundleURL(ver), bundle); err == errNotFound {
		return fmt.Errorf("the Flatpak of Astral %s is not published yet, so try again in a little while", ver)
	} else if err != nil {
		return err
	}
	onStatus("Installing the new version…", false)
	if out, err := hostFlatpak("install", flatpakScope(), "--noninteractive", "-y", "--bundle", bundle); err != nil {
		return fmt.Errorf("%v\n%s", err, tailLines(string(out), 400))
	}
	return nil
}

// errNotFound is a bundle that is not on the release yet: the notes go out as
// a release is tagged, and its builds follow a while later.
var errNotFound = errors.New("not found")

// download fetches url into path.
func download(url, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading the update: %s", res.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// restartFlatpak starts Astral again on the host once this instance has gone,
// then lets this one quit. It waits for the instance to leave rather than for
// a fixed time: Astral is single instance, and one started while this is
// still shutting down would hand itself to the old one and close.
func (a *App) restartFlatpak() error {
	script := `for i in $(seq 1 100); do flatpak ps --columns=application | grep -qx ` + appID +
		` || break; sleep 0.2; done; exec flatpak run ` + appID
	if err := exec.Command("flatpak-spawn", "--host", "sh", "-c", script).Start(); err != nil {
		return err
	}
	a.adw.Quit()
	return nil
}

// uninstallFlatpak removes the Flatpak, and its data with it when asked.
func uninstallFlatpak(deleteLibrary bool) error {
	args := []string{"uninstall", flatpakScope(), "--noninteractive", "-y"}
	if deleteLibrary {
		args = append(args, "--delete-data")
	}
	if out, err := hostFlatpak(append(args, appID)...); err != nil {
		return fmt.Errorf("%v\n%s", err, tailLines(string(out), 400))
	}
	return nil
}
