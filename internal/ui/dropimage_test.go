package ui

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
)

func TestLooksLikeImage(t *testing.T) {
	yes := []string{
		"/home/zach/a.png", "/home/zach/a.PNG", "a.jpg", "a.JPG", "a.jpeg", "a.jfif",
		"a.webp", "a.gif", "a.bmp", "a.tif", "a.tiff", "/a b/c.d.png",
	}
	for _, p := range yes {
		if !looksLikeImage(p) {
			t.Errorf("looksLikeImage(%q) = false, want true", p)
		}
	}
	no := []string{
		"a.json", "a.txt", "a", "", "a.png.txt", "/home/zach/card.pngx",
		// A character card is a PNG and is imported elsewhere; what matters
		// here is only that the name test does not accept something that is
		// obviously not an image at all.
		"a.exe", "a.mp4", "a.gz",
	}
	for _, p := range no {
		if looksLikeImage(p) {
			t.Errorf("looksLikeImage(%q) = true, want false", p)
		}
	}
}

// What a phone saves is HEIC, and the formats beyond Go's own come from the
// system's gdk-pixbuf loaders. Whatever this machine can load has to be
// offered, or a picture that would import fine is refused by name.
func TestSystemFormatsAreOffered(t *testing.T) {
	for _, f := range gdkpixbuf.PixbufGetFormats() {
		if f.IsDisabled() {
			continue
		}
		for _, ext := range f.Extensions() {
			if ext == "svg.gz" {
				continue
			}
			if !looksLikeImage("photo." + ext) {
				t.Errorf("%s loads through gdk-pixbuf (%s) but is not offered", ext, f.Name())
			}
		}
	}
}
