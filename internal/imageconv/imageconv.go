// Package imageconv normalizes the images a user brings in.
//
// Astral accepts whatever picture someone has to hand, which in practice means
// WebP as often as anything else. Two things then go wrong. Ollama's vision
// path expects PNG or JPEG, so a WebP reaches the model as bytes it cannot
// read. And displaying one needs a gdk-pixbuf WebP loader, which is a separate
// package that plenty of systems do not have installed.
//
// Rather than discover either of those at the point of use, anything that is
// not already a format both halves understand is decoded once, at import, and
// re-encoded as PNG.
package imageconv

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	// Decoders. The blank import registers GIF with image.Decode; PNG and
	// JPEG are registered by the encoders imported above, and x/image covers
	// the rest.
	_ "image/gif"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// Normalize returns image bytes that both GTK and a vision model can read,
// along with the file extension they should be saved under.
//
// PNG and JPEG are passed through untouched. Re-encoding a JPEG would throw
// away quality for nothing, and re-encoding a PNG would spend time to produce
// the same picture. Everything else is converted.
//
// Decoding also validates: a truncated download or a file that is not an image
// at all fails here, at import, rather than as a broken preview later.
func Normalize(data []byte) (out []byte, ext string, err error) {
	if len(data) == 0 {
		return nil, "", fmt.Errorf("that file is empty")
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("that file is not an image Astral can read")
	}

	switch format {
	case "png":
		return data, ".png", nil
	case "jpeg":
		return data, ".jpg", nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("that %s image could not be read: %w", format, err)
	}

	// Which format to convert *to* matters more than it looks. A lossy WebP
	// photograph re-encoded as PNG can grow by an order of magnitude, and the
	// result is not only sitting on disk: it is base64'd into every request
	// the image goes out with, so the model pays for it too.
	//
	// So transparency decides. An image with an alpha channel in use has to be
	// PNG or it loses it. Everything else is a photograph as far as this is
	// concerned, and becomes a JPEG.
	var buf bytes.Buffer
	if hasTransparency(img) {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", fmt.Errorf("could not convert that %s image: %w", format, err)
		}
		return buf.Bytes(), ".png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, "", fmt.Errorf("could not convert that %s image: %w", format, err)
	}
	return buf.Bytes(), ".jpg", nil
}

// hasTransparency reports whether any pixel is not fully opaque.
//
// The colour model alone is not enough: most decoders hand back an RGBA image
// whether or not the source used its alpha channel, so asking the type would
// answer "yes" for every photograph. This reads the pixels, which at import
// time is a cost worth paying once.
func hasTransparency(img image.Image) bool {
	if _, ok := img.(*image.YCbCr); ok {
		return false // JPEG's colour model has no alpha at all
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xffff {
				return true
			}
		}
	}
	return false
}

// Format reports what an image claims to be, for messages that name it.
func Format(data []byte) string {
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	return format
}
