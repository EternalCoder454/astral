package ui

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
)

// Which files count as pictures.
//
// Two decoders stand behind an import. Go's own reads PNG, JPEG, GIF, BMP, TIFF
// and WebP. gdk-pixbuf reads whatever this system has loaders for, which on a
// current desktop adds HEIC and AVIF (what phones save), JPEG XL, icons, SVG
// and a handful of rarer ones. So the list is asked of the system rather than
// written down: a machine with fewer loaders does not offer formats it would
// then fail to open, and one with more gets them without a release.

// goImageSuffixes are the ones Go decodes itself, so they are offered even on
// a system with no gdk-pixbuf loaders at all.
var goImageSuffixes = []string{
	"png", "jpg", "jpeg", "jpe", "jfif", "gif", "bmp", "dib", "tif", "tiff", "webp",
}

var (
	suffixOnce sync.Once
	suffixes   []string
	suffixSet  map[string]bool
)

// ImageSuffixes lists the file extensions an import can read, lower case and
// without the dot.
func ImageSuffixes() []string {
	suffixOnce.Do(func() {
		suffixSet = map[string]bool{}
		add := func(s string) {
			s = strings.ToLower(strings.TrimPrefix(s, "."))
			// "svg.gz" is listed as one, but a name ends in ".gz" as far as
			// anything matching on the last extension is concerned.
			if s == "" || strings.Contains(s, ".") || suffixSet[s] {
				return
			}
			suffixSet[s] = true
			suffixes = append(suffixes, s)
		}
		for _, s := range goImageSuffixes {
			add(s)
		}
		for _, f := range gdkpixbuf.PixbufGetFormats() {
			if f.IsDisabled() {
				continue
			}
			for _, e := range f.Extensions() {
				add(e)
			}
		}
		sort.Strings(suffixes)
	})
	return suffixes
}

// goImageTypes are the MIME types of the formats Go decodes itself.
var goImageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/bmp", "image/tiff", "image/webp"}

// ImageMIMETypes lists the MIME types an import can read, for a file chooser
// to match pictures by what they contain as well as by their names.
func ImageMIMETypes() []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range goImageTypes {
		add(t)
	}
	for _, f := range gdkpixbuf.PixbufGetFormats() {
		if f.IsDisabled() {
			continue
		}
		for _, t := range f.MIMETypes() {
			add(t)
		}
	}
	return out
}

// looksLikeImage reports whether a name is one an import can read. It is a
// first pass on the name only: the importer decodes the bytes and is the thing
// that actually decides, which is what catches a .png that is not one.
func looksLikeImage(path string) bool {
	ImageSuffixes()
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	return ext != "" && suffixSet[ext]
}
