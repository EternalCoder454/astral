package ui

import (
	"os"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
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

// loadCropped builds a square, centre-cropped, rounded image, or nil when the
// path is empty or unreadable.
func loadCropped(path string, size int) gtk.Widgetter {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	pic := gtk.NewPictureForFilename(path)
	// Cover fills the square and crops the overflow, which is what a face at
	// avatar size wants; Contain would letterbox it into a smaller face.
	pic.SetContentFit(gtk.ContentFitCover)
	pic.SetSizeRequest(size, size)
	pic.SetCanShrink(true)

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
