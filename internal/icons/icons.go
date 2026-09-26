// Package icons holds the symbolic icon set Astral bundles.
//
// It is a package of its own because there are two clients now. The window
// unpacks these into an icon theme for GTK; the server the phone talks to
// serves them to a browser. One copy, so a button cannot end up with different
// artwork depending on which screen you are looking at.
//
// They follow the symbolic conventions GTK expects: 16px nominal size, a
// single filled path, no strokes. That is also what makes them work as a CSS
// mask, which is how the phone recolours them.
package icons

import (
	"embed"
	"io/fs"
)

//go:embed svg/*.svg
var files embed.FS

// FS is the icon set, with the files at the top level.
func FS() fs.FS {
	sub, err := fs.Sub(files, "svg")
	if err != nil {
		// Only reachable if the embed directive above stops matching, which
		// is a build-time mistake rather than a runtime condition.
		panic("astral: the icon set is missing from this build: " + err.Error())
	}
	return sub
}

// Names is every file in the set, as "astral-thing-symbolic.svg".
func Names() []string {
	entries, err := fs.ReadDir(FS(), ".")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
