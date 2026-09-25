package imageconv

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	_ "image/jpeg"
	_ "image/png"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// WebP is the format this package exists for: it is what people actually have,
// and it is the one neither Ollama's vision path nor a stock gdk-pixbuf can
// necessarily read.
func TestWebPIsConverted(t *testing.T) {
	for _, name := range []string{"lossy.webp", "lossless.webp"} {
		t.Run(name, func(t *testing.T) {
			in := read(t, name)
			if got := Format(in); got != "webp" {
				t.Fatalf("fixture is %q, not webp", got)
			}

			out, ext, err := Normalize(in)
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			// No transparency in these fixtures, so they become JPEG: a lossy
			// WebP photograph re-encoded as PNG can grow tenfold, and the
			// result is base64'd into every request it goes out with.
			if ext != ".jpg" {
				t.Errorf("ext = %q, want .jpg", ext)
			}
			if got := Format(out); got != "jpeg" {
				t.Errorf("output is %q, want jpeg", got)
			}

			// The picture has to survive the conversion, not just the bytes.
			before, _, err := image.DecodeConfig(bytes.NewReader(in))
			if err != nil {
				t.Fatal(err)
			}
			after, _, err := image.DecodeConfig(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			if before.Width != after.Width || before.Height != after.Height {
				t.Errorf("size changed: %dx%d became %dx%d",
					before.Width, before.Height, after.Width, after.Height)
			}
		})
	}
}

// PNG and JPEG are already readable everywhere. Re-encoding a JPEG would throw
// away quality for nothing, so they must come back untouched.
func TestPNGAndJPEGPassThroughUnchanged(t *testing.T) {
	for _, tc := range []struct{ file, ext string }{
		{"sample.png", ".png"},
		{"sample.jpg", ".jpg"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			in := read(t, tc.file)
			out, ext, err := Normalize(in)
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if ext != tc.ext {
				t.Errorf("ext = %q, want %q", ext, tc.ext)
			}
			if !bytes.Equal(in, out) {
				t.Errorf("%s was re-encoded when it did not need to be", tc.file)
			}
		})
	}
}

func TestOtherFormatsAreConverted(t *testing.T) {
	in := read(t, "sample.bmp")
	if got := Format(in); got != "bmp" {
		t.Fatalf("fixture is %q, not bmp", got)
	}
	out, ext, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if Format(out) != "jpeg" || ext != ".jpg" {
		t.Errorf("opaque bmp became %q (%s), want jpeg", Format(out), ext)
	}
}

// An image that actually uses its alpha channel has to stay PNG, or the
// transparency is flattened onto whatever JPEG decides the background is.
func TestTransparentImagesStayPNG(t *testing.T) {
	in := read(t, "alpha.webp")
	if got := Format(in); got != "webp" {
		t.Fatalf("fixture is %q, not webp", got)
	}
	out, ext, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if ext != ".png" || Format(out) != "png" {
		t.Errorf("transparent webp became %q (%s), want png", Format(out), ext)
	}
}

// A photograph must not be misread as transparent. Most decoders hand back an
// RGBA image whether or not the source used alpha, so asking the type instead
// of the pixels would send every photo down the PNG path.
func TestOpaqueImagesAreNotTreatedAsTransparent(t *testing.T) {
	for _, name := range []string{"sample.png", "lossy.webp", "sample.bmp"} {
		img, _, err := image.Decode(bytes.NewReader(read(t, name)))
		if err != nil {
			t.Fatal(err)
		}
		if hasTransparency(img) {
			t.Errorf("%s was judged transparent", name)
		}
	}
	img, _, err := image.Decode(bytes.NewReader(read(t, "alpha.png")))
	if err != nil {
		t.Fatal(err)
	}
	if !hasTransparency(img) {
		t.Error("alpha.png was judged opaque")
	}
}

// Decoding doubles as validation: a file that is not an image should fail at
// import, with a message a person can act on, rather than becoming a broken
// preview and an image the model silently cannot read.
func TestJunkIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"text", []byte("this is not an image, it is a sentence")},
		{"truncated png", read(t, "sample.png")[:20]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Normalize(tc.data); err == nil {
				t.Error("accepted something that is not a readable image")
			}
		})
	}
}

// An avatar has to come out the size asked for and square, whatever shape went
// in. A tall portrait used as an avatar was allocated at its own size and
// appeared beside messages several times too large, because a widget size
// request is only a minimum.
func TestThumbnailIsAlwaysSquareAndTheSizeAsked(t *testing.T) {
	for _, name := range []string{"sample.png", "sample.jpg", "lossy.webp", "sample.bmp"} {
		for _, size := range []int{28, 64} {
			out, err := Thumbnail(read(t, name), size)
			if err != nil {
				t.Fatalf("Thumbnail(%s, %d): %v", name, size, err)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Width != size || cfg.Height != size {
				t.Errorf("%s at %d came out %dx%d", name, size, cfg.Width, cfg.Height)
			}
		}
	}
}

// A non-square source must be centre cropped rather than squashed.
func TestThumbnailCropsRatherThanDistorts(t *testing.T) {
	tall := image.NewRGBA(image.Rect(0, 0, 40, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 40; x++ {
			// A band of white across the vertical middle, black elsewhere.
			c := uint8(0)
			if y > 50 && y < 70 {
				c = 255
			}
			tall.Set(x, y, color.RGBA{c, c, c, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, tall); err != nil {
		t.Fatal(err)
	}
	out, err := Thumbnail(buf.Bytes(), 40)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 40 {
		t.Fatalf("thumbnail is %v", b)
	}
	// The centre band should have survived the crop and be near the middle.
	mid, _, _, _ := img.At(20, 20).RGBA()
	if mid < 0x8000 {
		t.Errorf("the middle of the image was cropped away; centre pixel = %d", mid>>8)
	}
}

func TestThumbnailRejectsJunk(t *testing.T) {
	if _, err := Thumbnail([]byte("not an image"), 28); err == nil {
		t.Error("junk was accepted")
	}
	if _, err := Thumbnail(read(t, "sample.png"), 0); err == nil {
		t.Error("a zero size was accepted")
	}
}
