// Package imageconv normalizes the images a user brings in.
//
// WebP is common and breaks twice over: Ollama's vision path expects PNG or
// JPEG, and displaying one needs a gdk-pixbuf loader many systems lack. So
// anything neither half understands is decoded once, at import, and
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

	"golang.org/x/image/draw"

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

// Thumbnail returns a square, centre-cropped PNG of the given pixel size.
//
// This is done here rather than by asking GTK to scale a GtkPicture, because a
// picture's *natural* size is the size of the image in it. A size request on
// the widget is only a minimum, so a 64x96 portrait used as an avatar was
// allocated 64x96 and appeared beside messages roughly three times the size
// intended. Producing a texture that is already the right shape removes the
// negotiation entirely, and means each row holds a 28px image rather than a
// full-resolution one.
func Thumbnail(data []byte, size int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("thumbnail size must be positive")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("that file is not an image Astral can read")
	}

	// Centre crop to a square first, so scaling cannot distort the aspect.
	b := src.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	crop := image.Rect(
		b.Min.X+(b.Dx()-side)/2,
		b.Min.Y+(b.Dy()-side)/2,
		b.Min.X+(b.Dx()-side)/2+side,
		b.Min.Y+(b.Dy()-side)/2+side,
	)

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Over, nil)

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
