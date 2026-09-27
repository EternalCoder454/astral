package ui

import "testing"

func TestLooksLikeImage(t *testing.T) {
	yes := []string{
		"/home/zach/a.png", "/home/zach/a.PNG", "a.jpg", "a.jpeg",
		"a.webp", "a.gif", "a.bmp", "/a b/c.d.png",
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
		"a.exe", "a.mp4",
	}
	for _, p := range no {
		if looksLikeImage(p) {
			t.Errorf("looksLikeImage(%q) = true, want false", p)
		}
	}
}

// The extension list and the media type list describe the same set from two
// directions: a dropped file is judged by its name, a pasted one by what the
// clipboard says it is. Adding a format to one and not the other means an
// image that can be dropped but not pasted, or the reverse.
func TestImageExtensionsAndMediaTypesAgree(t *testing.T) {
	byExt := map[string]bool{}
	for _, e := range imageExts {
		switch e {
		case ".jpg", ".jpeg":
			byExt["jpeg"] = true
		default:
			byExt[e[1:]] = true
		}
	}
	byMIME := map[string]bool{}
	for _, m := range imageMIMEs {
		byMIME[m[len("image/"):]] = true
	}
	for k := range byExt {
		if !byMIME[k] {
			t.Errorf("%q can be dropped but not pasted: no image/%s media type", k, k)
		}
	}
	for k := range byMIME {
		if !byExt[k] {
			t.Errorf("image/%s can be pasted but not dropped: no matching extension", k)
		}
	}
}
