package ui

import (
	"os"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/imageconv"
)

// Characters can carry two images, and they are cropped for different jobs.
// The avatar is a face at 28 pixels beside every message, so it is filled and
// centre-cropped. The portrait is the whole figure shown beside a scene, so it
// is fitted and never cropped.
//
// Both fall back rather than fail: a character whose image file has been moved
// or deleted still shows a tinted letter tile and still plays.

// NewCharacterAvatar returns the character's avatar, or a tinted letter tile
// when it has no image or the file has gone.
func NewCharacterAvatar(c chars.Character, size int) gtk.Widgetter {
	if pic := loadCropped(c.AvatarPath, size); pic != nil {
		return pic
	}
	return NewAvatar(c.Initial(), c.Accent, size)
}

// loadCropped builds a square, rounded avatar image, or nil when the path is
// empty or unreadable.
//
// The image is cropped and scaled in Go rather than by GTK. A GtkPicture's
// natural size is the size of the image inside it, and a size request on a
// widget is only a minimum, so a tall portrait used as an avatar was allocated
// at its own size and appeared beside messages several times too large.
func loadCropped(path string, size int) gtk.Widgetter {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	thumb, err := imageconv.Thumbnail(data, size)
	if err != nil {
		return nil
	}
	tex, err := gdk.NewTextureFromBytes(glib.NewBytesWithGo(thumb))
	if err != nil {
		return nil
	}

	pic := gtk.NewPictureForPaintable(tex)
	pic.SetCanShrink(true)
	pic.SetSizeRequest(size, size)

	// The rounding is on a wrapper: a GtkPicture draws its own contents and
	// will happily paint over a border-radius set on itself.
	frame := gtk.NewBox(gtk.OrientationHorizontal, 0)
	frame.AddCSSClass("avatar")
	frame.AddCSSClass("avatar-image")
	frame.SetOverflow(gtk.OverflowHidden)
	frame.SetSizeRequest(size, size)
	frame.SetHAlign(gtk.AlignCenter)
	frame.SetVAlign(gtk.AlignCenter)
	frame.Append(pic)
	return frame
}

// NewPortrait builds the large image shown beside a scene, or nil when the
// character has none.
func NewPortrait(path string) *gtk.Picture {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	pic := gtk.NewPictureForFilename(path)
	// Contain, not Cover: a portrait is being looked at, so cropping the top
	// of someone's head to fill a panel is the wrong trade.
	pic.SetContentFit(gtk.ContentFitContain)
	pic.SetCanShrink(true)
	pic.SetVExpand(true)
	pic.SetHExpand(true)
	pic.AddCSSClass("portrait-image")
	return pic
}

// NewImageThumb is a small rounded preview of an image file, or nil when the
// file is missing.
func NewImageThumb(path string, size int) gtk.Widgetter { return loadCropped(path, size) }
