// Package imageconv normalizes the images a user brings in.
//
// Two different readers have to be able to use what comes out: GTK, to show
// it, and a vision model, to look at it. Ollama's vision path expects PNG or
// JPEG, so anything else is decoded once, at import, and re-encoded.
//
// What the model is handed matters as much as whether it can read it. A phone
// photograph is stored sideways with a note saying which way up it goes, and
// a model that ignores the note is asked to describe a face lying on its side,
// which is the single easiest way to get a worse description of a person. And
// a 48 megapixel original is minutes of image tokens for detail the model
// scales away anyway. So photographs are turned upright and brought down to a
// size vision models actually read at.
package imageconv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	// Decoders. The blank import registers GIF with image.Decode; PNG and
	// JPEG are registered by the encoders imported above, and x/image covers
	// the rest. Anything none of these know (HEIC, AVIF, JPEG XL, icons,
	// SVG) is the caller's to decode some other way; see ErrUnknownFormat.
	_ "image/gif"

	"golang.org/x/image/draw"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// MaxSide is the longest edge an imported image is kept at.
//
// Vision models resize whatever they are given to a fixed budget of patches,
// so pixels beyond that budget are decoded, sent and thrown away. 1536 keeps a
// face in a full-length photograph large enough to read an expression from,
// and a portrait crop far larger than that.
const MaxSide = 1536

// MaxPixels is the largest image that will be decoded at all. A 108 megapixel
// phone photograph is under it; a decompression bomb claiming a million pixels
// square is not, and is refused before anything is allocated for it.
const MaxPixels = 150_000_000

// ErrUnknownFormat is returned for bytes none of the decoders here recognise.
// It is not the same as "not an image": the caller may have a decoder for a
// format this package does not carry, and is expected to try it.
var ErrUnknownFormat = errors.New("that file is not an image Astral can read")

// Normalize returns image bytes that both GTK and a vision model can read,
// along with the file extension they should be saved under.
//
// A PNG or JPEG that is already upright and no larger than MaxSide comes back
// untouched: re-encoding a JPEG would throw away quality for nothing. Anything
// else is decoded, turned the right way up, brought down to MaxSide, and
// encoded again.
//
// Decoding also validates: a truncated download or a file that is not an image
// at all fails here, at import, rather than as a broken preview later.
func Normalize(data []byte) (out []byte, ext string, err error) {
	if len(data) == 0 {
		return nil, "", fmt.Errorf("that file is empty")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", ErrUnknownFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxPixels {
		return nil, "", fmt.Errorf("that image is %dx%d, which is too large to read", cfg.Width, cfg.Height)
	}

	orientation := 1
	if format == "jpeg" {
		orientation = jpegOrientation(data)
	}
	if orientation == 1 && cfg.Width <= MaxSide && cfg.Height <= MaxSide {
		switch format {
		case "png":
			return data, ".png", nil
		case "jpeg":
			return data, ".jpg", nil
		}
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("that %s image could not be read: %w", format, err)
	}
	// Shrunk before it is turned, so the turn moves the small copy.
	img = Upright(Fit(img, MaxSide), orientation)

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

// Fit scales an image down so neither side is longer than side, keeping its
// shape. An image already that small is returned as it is.
//
// Bilinear rather than Catmull-Rom: x/image widens either kernel to cover the
// whole source footprint when shrinking, so neither aliases, and bilinear does
// it in a quarter of the work, which on a 50 megapixel photograph is the
// difference between an import you notice and one you do not.
func Fit(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		return img
	}
	if w >= h {
		h = max(1, h*side/w)
		w = side
	} else {
		w = max(1, w*side/h)
		h = side
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// Upright applies an EXIF orientation, 1 to 8, so the picture is the way up
// the camera meant it. 1, or anything out of range, is no change.
func Upright(img image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src, ok := img.(*image.RGBA)
	if !ok {
		src = image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	}

	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w // the four that turn a quarter swap the sides
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			// Where in the stored image the pixel shown at (x, y) comes from.
			var sx, sy int
			switch orientation {
			case 2: // mirrored
				sx, sy = w-1-x, y
			case 3: // upside down
				sx, sy = w-1-x, h-1-y
			case 4: // mirrored and upside down
				sx, sy = x, h-1-y
			case 5: // mirrored, then a quarter turn
				sx, sy = y, x
			case 6: // a quarter turn clockwise, how most phones store a portrait
				sx, sy = y, h-1-x
			case 7: // mirrored, then a quarter turn the other way
				sx, sy = w-1-y, h-1-x
			case 8: // a quarter turn anticlockwise
				sx, sy = w-1-y, x
			}
			si := src.PixOffset(src.Rect.Min.X+sx, src.Rect.Min.Y+sy)
			di := dst.PixOffset(x, y)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// jpegOrientation reads the EXIF orientation out of a JPEG's APP1 segment, and
// answers 1 (as stored) when there is none or it cannot be read.
//
// Go's decoder reads the pixels and nothing else, so this walks the segments
// itself. Metadata always comes before the image data, so the walk stops at
// the start of the scan rather than reading the whole file.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		switch {
		case marker == 0xFF: // padding between segments
			i++
			continue
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8): // no length
			i += 2
			continue
		case marker == 0xDA || marker == 0xD9: // the image itself, or the end
			return 1
		}
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			if o := exifOrientation(seg[6:]); o != 0 {
				return o
			}
		}
		i += 2 + size
	}
	return 1
}

// exifOrientation finds tag 0x0112 in the first directory of an EXIF block,
// which is a small TIFF file of its own. Zero when it is not there.
func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(t[2:]) != 42 {
		return 0
	}
	ifd := int(order.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 0
	}
	n := int(order.Uint16(t[ifd:]))
	for k := 0; k < n; k++ {
		e := ifd + 2 + 12*k
		if e+12 > len(t) {
			return 0
		}
		if order.Uint16(t[e:]) != 0x0112 {
			continue
		}
		// A SHORT, count one, so the value sits in the first two bytes of
		// the entry's value field.
		if order.Uint16(t[e+2:]) != 3 {
			return 0
		}
		if v := int(order.Uint16(t[e+8:])); v >= 1 && v <= 8 {
			return v
		}
		return 0
	}
	return 0
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
	// Every concrete type in the standard library can answer this from its
	// own pixel buffer, which is far quicker than asking pixel by pixel.
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return !o.Opaque()
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
