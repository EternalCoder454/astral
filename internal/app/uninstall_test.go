//go:build linux

package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// What uninstalling a build from source removes is exactly what make install
// and the install script put down, and nothing of the library.
func TestUninstallRemovesOnlyTheProgram(t *testing.T) {
	home := "/home/someone"
	src := filepath.Join(home, ".local", "share", "astral", "src")
	got := programFilesUnder(home, src)
	want := []string{
		"/home/someone/.local/bin/astral",
		"/home/someone/.local/share/applications/io.github.astral.desktop",
		"/home/someone/.local/share/icons/hicolor/scalable/apps/io.github.astral.svg",
		"/home/someone/.local/share/astral/src",
		"/home/someone/.local/share/astral/go",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("program files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, p := range got {
		if p == filepath.Join(home, ".local", "share", "astral") {
			t.Errorf("the library directory is among the program files")
		}
	}
}

// A Flatpak installed for the user is updated and removed with --user, and
// one installed for everyone with --system.
func TestFlatpakScope(t *testing.T) {
	user := "[Application]\nname=io.github.astral\n\n[Instance]\ninstance-id=1\n" +
		"app-path=/home/someone/.local/share/flatpak/app/io.github.astral/x86_64/master/abc/files\n"
	system := "[Application]\nname=io.github.astral\n\n[Instance]\n" +
		"app-path=/var/lib/flatpak/app/io.github.astral/x86_64/master/abc/files\n"
	if got := scopeFromInfo(strings.NewReader(user)); got != "--user" {
		t.Errorf("user install: %s", got)
	}
	if got := scopeFromInfo(strings.NewReader(system)); got != "--system" {
		t.Errorf("system install: %s", got)
	}
	if got := scopeFromInfo(strings.NewReader("")); got != "--user" {
		t.Errorf("no information: %s", got)
	}
}

// Deleting the library only ever deletes folders that are Astral's own: an
// XDG variable holding a relative path, or the home directory itself, is
// refused.
func TestLibraryDirOK(t *testing.T) {
	for dir, want := range map[string]bool{
		"/home/someone/.local/share/astral": true,
		"/home/someone/.config/astral":      true,
		"astral":                            false,
		"relative/astral":                   false,
		"/home/someone":                     false,
		"/":                                 false,
		"/home/someone/.local/share":        false,
	} {
		if got := libraryDirOK(dir); got != want {
			t.Errorf("libraryDirOK(%q) = %v, want %v", dir, got, want)
		}
	}
}
