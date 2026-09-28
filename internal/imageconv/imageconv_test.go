package imageconv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// withOrientation puts an EXIF block carrying one orientation tag at the front
// of a JPEG, the way a phone does. Built by hand because no encoder in the
// standard library writes EXIF, and the test should not depend on one that
// happens to be installed.
func withOrientation(jpg []byte, o uint16, order binary.ByteOrder) []byte {
	tiff := make([]byte, 8+2+12+4)
	if order == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8) // the first directory follows the header
	order.PutUint16(tiff[8:], 1) // with one entry in it
	e := tiff[10:]
	order.PutUint16(e[0:], 0x0112) // orientation
	order.PutUint16(e[2:], 3)      // SHORT
	order.PutUint32(e[4:], 1)      // one of them
	order.PutUint16(e[8:], o)

	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)

	out := append([]byte{}, jpg[:2]...) // SOI
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// halves is an opaque image, red on the left and blue on the right.
func halves(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{220, 30, 30, 255}
			if x >= w/2 {
				c = color.RGBA{30, 30, 220, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func isRed(c color.Color) bool {
	r, _, b, _ := c.RGBA()
	return r > 0x9000 && b < 0x6000
}

// A portrait taken on a phone is stored lying on its side with a note saying
// which way up it goes. Handed over as stored, a vision model is describing a
// face turned ninety degrees, which is the easiest way there is to get a worse
// description of a person.
func TestPhonePortraitIsTurnedUpright(t *testing.T) {
	var src bytes.Buffer
	if err := jpeg.Encode(&src, halves(64, 32), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		in := withOrientation(src.Bytes(), 6, order)
		if got := jpegOrientation(in); got != 6 {
			t.Fatalf("%v: read orientation %d, want 6", order, got)
		}
		out, ext, err := Normalize(in)
		if err != nil {
			t.Fatal(err)
		}
		if ext != ".jpg" {
			t.Errorf("ext %q", ext)
		}
		img, _, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 64 {
			t.Fatalf("%v: %dx%d, want 32x64", order, b.Dx(), b.Dy())
		}
		// A quarter turn clockwise brings the left half to the top.
		if !isRed(img.At(16, 12)) || isRed(img.At(16, 52)) {
			t.Errorf("%v: turned the wrong way", order)
		}
	}
}

// Every orientation, against pictures of the answer drawn by hand rather than
// the formula the code uses.
func TestUprightAllOrientations(t *testing.T) {
	// A B C
	// D E F
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	names := "ABCDEF"
	for i := range names {
		src.Pix[i*4] = names[i]
		src.Pix[i*4+3] = 255
	}
	want := map[int][]string{
		1: {"ABC", "DEF"},
		2: {"CBA", "FED"},
		3: {"FED", "CBA"},
		4: {"DEF", "ABC"},
		5: {"AD", "BE", "CF"},
		6: {"DA", "EB", "FC"},
		7: {"FC", "EB", "DA"},
		8: {"CF", "BE", "AD"},
	}
	for o, rows := range want {
		got := Upright(src, o).(*image.RGBA)
		var lines []string
		for y := 0; y < got.Rect.Dy(); y++ {
			line := ""
			for x := 0; x < got.Rect.Dx(); x++ {
				line += string(got.Pix[got.PixOffset(x, y)])
			}
			lines = append(lines, line)
		}
		if fmt.Sprint(lines) != fmt.Sprint(rows) {
			t.Errorf("orientation %d: got %v, want %v", o, lines, rows)
		}
	}
}

// Anything larger than a vision model reads at comes down to MaxSide, keeping
// its shape. A photograph stays a JPEG.
func TestLargeImagesAreBroughtDown(t *testing.T) {
	var src bytes.Buffer
	if err := png.Encode(&src, halves(4000, 1000)); err != nil {
		t.Fatal(err)
	}
	out, ext, err := Normalize(src.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if ext != ".jpg" {
		t.Errorf("an opaque picture became %q", ext)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != MaxSide || cfg.Height != MaxSide/4 {
		t.Errorf("%dx%d, want %dx%d", cfg.Width, cfg.Height, MaxSide, MaxSide/4)
	}
}

// A file claiming to be enormous is refused before anything is allocated for
// it, from its header alone.
func TestAnEnormousImageIsRefused(t *testing.T) {
	var src bytes.Buffer
	if err := png.Encode(&src, halves(4, 4)); err != nil {
		t.Fatal(err)
	}
	b := src.Bytes()
	// IHDR is the first chunk: 8 bytes of signature, 8 of length and type,
	// then width and height, then the rest, then a checksum over type+data.
	binary.BigEndian.PutUint32(b[16:], 40000)
	binary.BigEndian.PutUint32(b[20:], 40000)
	binary.BigEndian.PutUint32(b[29:], crc32.ChecksumIEEE(b[12:29]))
	if _, _, err := Normalize(b); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("a 1.6 gigapixel image was not refused: %v", err)
	}
}

// Bytes none of these decoders know are reported as such, so the caller can
// try a decoder of its own before giving up.
func TestUnknownBytesSayWhy(t *testing.T) {
	_, _, err := Normalize([]byte("\x00\x00\x00\x1cftypheic this is not decodable here"))
	if !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("got %v, want ErrUnknownFormat", err)
	}
}
