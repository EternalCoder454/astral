//go:build !race

// Not under -race: gdk-pixbuf is reached through gotk4, and the race
// detector's pointer checks abort inside gotk4's bindings.

package app

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"
)

// Formats Go cannot decode go through gdk-pixbuf and come out as something
// both GTK and a vision model can read. SVG stands in for the rest (HEIC,
// AVIF, JPEG XL) because it can be written by hand in a test, and it takes the
// same path.
func TestFormatsGoCannotReadAreStillImported(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="120" height="80">` +
		`<rect width="120" height="80" fill="#c0392b"/></svg>`)

	for _, tc := range []struct {
		name string
		data []byte
		w, h int
	}{
		{"svg", svg, 120, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, ext, was, err := normalizeImage(tc.data)
			if err != nil {
				t.Skipf("this system has no %s loader: %v", tc.name, err)
			}
			if ext != ".jpg" && ext != ".png" {
				t.Errorf("saved as %q", ext)
			}
			if was == "" {
				t.Error("a conversion from an unusual format was not reported")
			}
			img, _, err := image.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("the result is not readable: %v", err)
			}
			if b := img.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
				t.Errorf("%dx%d, want %dx%d", b.Dx(), b.Dy(), tc.w, tc.h)
			}
			r, _, _, _ := color.RGBAModel.Convert(img.At(tc.w/2, tc.h/2)).RGBA()
			if r < 0x9000 {
				t.Errorf("the picture did not survive: centre red is %#x", r)
			}
		})
	}
}

// Bytes that nothing can decode are refused with the message that says so,
// not with whatever gdk-pixbuf said about them.
func TestNotAnImageIsRefusedPlainly(t *testing.T) {
	_, _, _, err := normalizeImage([]byte("this is a text file, not a picture"))
	if err == nil || !strings.Contains(err.Error(), "not an image Astral can read") {
		t.Errorf("got %v", err)
	}
}
