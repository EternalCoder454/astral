package app

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
)

// Astral ships every icon it uses rather than asking the desktop's theme for
// some of them. A toolbar drawn half from Material Symbols and half from
// whatever Adwaita, Papirus or Breeze happens to provide is a toolbar of
// mismatched weights and sizes; owning the set is the only way it looks like
// one set. Most came across from Atlas Notes and Atlas Monitor, so the three
// apps share a visual language.
//
// They follow the symbolic conventions GTK expects (16px nominal size, a
// single filled path, no strokes), so GTK recolours them with the rest of the
// interface and they match at any size.

//go:embed icons/*.svg
var iconFS embed.FS

// iconIndexTheme is the minimal theme description GTK needs to find the icons
// in the directory below.
const iconIndexTheme = `[Icon Theme]
Name=Astral
Comment=Symbolic icons bundled with Astral
Directories=scalable/actions

[scalable/actions]
Size=16
MinSize=8
MaxSize=512
Type=Scalable
Context=Actions
`

// iconCache memoizes icon-theme lookups. Each one is a query into GTK through
// cgo, and the window asks about a dozen while it is being built.
var iconCache = map[string]bool{}

// hasIcon reports whether the current icon theme can draw name.
func hasIcon(name string) bool {
	if known, ok := iconCache[name]; ok {
		return known
	}
	display := gdk.DisplayGetDefault()
	if display == nil {
		return false
	}
	known := gtk.IconThemeGetForDisplay(display).HasIcon(name)
	iconCache[name] = known
	return known
}

// iconDir is where the bundled icons are unpacked: the app's own data
// directory, in the layout an icon theme has.
func iconDir() string { return filepath.Join(store.DataDir(), "icons") }

// installIcons unpacks the bundled icons and adds them to the icon theme, so
// the rest of the app can ask for them by name.
func installIcons() {
	dir := iconDir()
	if err := unpackIcons(dir); err != nil {
		log.Printf("astral: bundled icons: %v", err)
		return // buttons fall back to stock names
	}
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	gtk.IconThemeGetForDisplay(display).AddSearchPath(dir)
}

// unpackIcons writes the icons to disk when what is there is not what is
// embedded. The stamp keeps a normal launch down to one read and one compare.
//
// It is a digest of the icons themselves rather than the app's version. Keyed
// on the version, an icon that changed without a release going out would never
// reach disk — which is every icon change during development.
func unpackIcons(dir string) error {
	actions := filepath.Join(dir, "hicolor", "scalable", "actions")
	stampPath := filepath.Join(dir, ".stamp")

	entries, err := fs.ReadDir(iconFS, "icons")
	if err != nil {
		return err
	}
	want, err := iconStamp(entries)
	if err != nil {
		return err
	}
	if current, err := os.ReadFile(stampPath); err == nil && string(current) == want {
		return nil
	}

	// Start from an empty directory so a renamed icon does not leave its old
	// name behind, still resolving, for the rest of the install's life.
	if err := os.RemoveAll(actions); err != nil {
		return err
	}
	if err := os.MkdirAll(actions, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		data, err := iconFS.ReadFile("icons/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(actions, e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "hicolor", "index.theme"), []byte(iconIndexTheme), 0o644); err != nil {
		return err
	}
	return os.WriteFile(stampPath, []byte(want), 0o644)
}

// iconStamp digests every bundled icon's name and contents.
func iconStamp(entries []fs.DirEntry) (string, error) {
	h := sha256.New()
	for _, e := range entries {
		data, err := iconFS.ReadFile("icons/" + e.Name())
		if err != nil {
			return "", err
		}
		h.Write([]byte(e.Name()))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// The names themselves live in internal/ui, beside the widgets that ask for
// them; this file only gets them onto the icon theme.
