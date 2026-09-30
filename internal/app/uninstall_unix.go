//go:build linux || darwin

package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// canUninstall says whether Astral can remove itself here: a build from source
// or a Flatpak, which is every way it is installed on Linux.
func canUninstall() bool { return true }

// programFiles is what `make install` and the install script put in place,
// and the clone and Go toolchain updates are built with. Not the library.
func programFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return programFilesUnder(home, canonicalSourceDir())
}

// programFilesUnder is programFiles for a given home directory and clone.
func programFilesUnder(home, src string) []string {
	local := filepath.Join(home, ".local")
	return []string{
		filepath.Join(local, "bin", "astral"),
		filepath.Join(local, "share", "applications", appID+".desktop"),
		filepath.Join(local, "share", "icons", "hicolor", "scalable", "apps", appID+".svg"),
		src,
		filepath.Join(filepath.Dir(src), "go"),
	}
}

// removeProgramFiles removes a build from source, and tells the desktop its
// entry has gone so it leaves the application menu straight away.
func removeProgramFiles() error {
	var errs []error
	for _, p := range programFiles() {
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, err)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		share := filepath.Join(home, ".local", "share")
		_ = exec.Command("update-desktop-database", filepath.Join(share, "applications")).Run()
		_ = exec.Command("gtk-update-icon-cache", "-f", "-t", filepath.Join(share, "icons", "hicolor")).Run()
	}
	return errors.Join(errs...)
}
