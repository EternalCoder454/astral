package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

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
//
// A picture not made yet shows the letter tile at once and becomes the
// picture when it is ready, rather than holding up whatever is being drawn:
// the sidebar draws every chat's at startup, and a card's picture is often a
// megabyte or two of PNG, tens of milliseconds each to decode.
func NewCharacterAvatar(c chars.Character, size int) gtk.Widgetter {
	if c.AvatarPath == "" {
		return NewAvatar(c.Initial(), c.Accent, size)
	}
	if tex, ok := cachedTexture(c.AvatarPath, size); ok {
		if tex != nil {
			return imageFrame(tex, size)
		}
		return NewAvatar(c.Initial(), c.Accent, size)
	}
	// The letter in a box that can take the picture in its place.
	tile := gtk.NewBox(gtk.OrientationHorizontal, 0)
	tile.AddCSSClass("avatar")
	accent := accentClass(c.Accent)
	tile.AddCSSClass(accent)
	tile.SetSizeRequest(size, size)
	tile.SetHExpand(false)
	tile.SetVExpand(false)
	tile.SetHAlign(gtk.AlignStart)
	tile.SetVAlign(gtk.AlignCenter)
	letter := gtk.NewLabel(c.Initial())
	letter.SetHExpand(true)
	letter.SetHAlign(gtk.AlignCenter)
	tile.Append(letter)
	makeTexture(c.AvatarPath, size, func(tex *gdk.Texture) {
		if tex == nil {
			return
		}
		tile.Remove(letter)
		tile.RemoveCSSClass(accent)
		tile.AddCSSClass("avatar-image")
		tile.SetOverflow(gtk.OverflowHidden)
		tile.Append(avatarImage(tex, size))
	})
	return tile
}

// NewPersonaAvatar is one of your personas' pictures, or your initial on the
// clay disc when it has none.
func NewPersonaAvatar(p chars.Profile, size int) gtk.Widgetter {
	if pic := loadCropped(p.AvatarPath, size); pic != nil {
		return pic
	}
	return NewUserAvatar(firstLetter(p.DisplayName()), size)
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
	tex, ok := cachedTexture(path, size)
	if !ok {
		tex = storeTexture(path, size, thumbnail(path, size))
	}
	if tex == nil {
		return nil
	}
	return imageFrame(tex, size)
}

// avatarImage shows a texture at an avatar's size.
func avatarImage(tex *gdk.Texture, size int) *gtk.Image {
	// GtkImage, not GtkPicture. A picture's natural size is negotiable: it
	// takes whatever width the row has going spare, and a letter tile does
	// not, so a list mixing the two had its image rows indented and their
	// text pushed half an avatar to the right. An image with a pixel size
	// asks for exactly that many pixels and nothing else.
	img := gtk.NewImageFromPaintable(tex)
	img.SetPixelSize(size)
	img.SetHExpand(false)
	img.SetVExpand(false)
	return img
}

// imageFrame is an avatar picture with its rounding.
func imageFrame(tex *gdk.Texture, size int) gtk.Widgetter {
	// The rounding is on a wrapper: the image paints its own contents and
	// will happily paint over a border-radius set on itself.
	frame := gtk.NewBox(gtk.OrientationHorizontal, 0)
	frame.AddCSSClass("avatar")
	frame.AddCSSClass("avatar-image")
	frame.SetOverflow(gtk.OverflowHidden)
	frame.SetSizeRequest(size, size)
	frame.SetHExpand(false)
	frame.SetVExpand(false)
	frame.SetHAlign(gtk.AlignStart)
	frame.SetVAlign(gtk.AlignCenter)
	frame.Append(avatarImage(tex, size))
	return frame
}

// thumbs are the avatar images already cropped and scaled, by file and size.
//
// Every avatar used to be read, decoded and scaled again each time one was
// drawn: beside every message a character starts, and once for every chat in
// the sidebar. A texture can be shown by any number of widgets at once, so
// each is made once and shared, and kept on disk as a small PNG as well, so
// the next launch does not decode the original again. The file's size and
// time are part of the key, so a picture replaced under the same name is read
// again.
var thumbs = struct {
	sync.Mutex
	m       map[thumbKey]*gdk.Texture
	pending map[thumbKey][]func(*gdk.Texture)
}{m: map[thumbKey]*gdk.Texture{}, pending: map[thumbKey][]func(*gdk.Texture){}}

type thumbKey struct {
	path      string
	size      int
	mod, span int64
}

// thumbScale is how many image pixels an avatar has per pixel it is shown
// at, so it stays sharp on a display scaled to twice the size.
const thumbScale = 2

func keyFor(path string, size int) (thumbKey, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return thumbKey{}, false
	}
	return thumbKey{path, size, st.ModTime().UnixNano(), st.Size()}, true
}

// cachedTexture is the avatar texture when it is ready, from memory or from
// the thumbnail kept on disk. ok is false when it has still to be made; a nil
// texture with ok true is a file that is not an image, or has gone.
func cachedTexture(path string, size int) (*gdk.Texture, bool) {
	key, found := keyFor(path, size)
	if !found {
		return nil, true
	}
	thumbs.Lock()
	tex, ok := thumbs.m[key]
	thumbs.Unlock()
	if ok {
		return tex, true
	}
	if data, err := os.ReadFile(diskThumb(key)); err == nil {
		if tex, err := gdk.NewTextureFromBytes(glib.NewBytesWithGo(data)); err == nil {
			thumbs.Lock()
			thumbs.m[key] = tex
			thumbs.Unlock()
			return tex, true
		}
	}
	return nil, false
}

// thumbnail reads and scales path, off the main thread or on it. Nil when it
// is not an image.
func thumbnail(path string, size int) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	thumb, err := imageconv.Thumbnail(data, size*thumbScale)
	if err != nil {
		return nil
	}
	return thumb
}

// storeTexture makes the texture from a thumbnail and keeps it, in memory
// and on disk. On the main thread.
func storeTexture(path string, size int, thumb []byte) *gdk.Texture {
	key, found := keyFor(path, size)
	if !found {
		return nil
	}
	var tex *gdk.Texture
	if thumb != nil {
		tex, _ = gdk.NewTextureFromBytes(glib.NewBytesWithGo(thumb))
		if tex != nil {
			if dir := filepath.Dir(diskThumb(key)); os.MkdirAll(dir, 0o755) == nil {
				_ = os.WriteFile(diskThumb(key), thumb, 0o644)
			}
		}
	}
	thumbs.Lock()
	for k := range thumbs.m {
		if k.path == path && k.size == size && k != key {
			delete(thumbs.m, k) // the same picture before it was replaced
		}
	}
	thumbs.m[key] = tex
	thumbs.Unlock()
	return tex
}

// makeTexture makes path's avatar texture off the main thread and hands it
// to done on the main thread; nil when it is not an image. Asked for twice
// before it is ready, it is made once.
func makeTexture(path string, size int, done func(*gdk.Texture)) {
	key, found := keyFor(path, size)
	if !found {
		return
	}
	thumbs.Lock()
	waiting := thumbs.pending[key]
	thumbs.pending[key] = append(waiting, done)
	thumbs.Unlock()
	if len(waiting) > 0 {
		return
	}
	go func() {
		thumb := thumbnail(path, size)
		glib.IdleAdd(func() bool {
			tex := storeTexture(path, size, thumb)
			thumbs.Lock()
			all := thumbs.pending[key]
			delete(thumbs.pending, key)
			thumbs.Unlock()
			for _, f := range all {
				f(tex)
			}
			return false
		})
	}()
}

// diskThumb is where the thumbnail for key is kept.
func diskThumb(k thumbKey) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%d", k.path, k.size*thumbScale, k.mod, k.span, thumbVersion)))
	return filepath.Join(dir, "astral", "thumbs", hex.EncodeToString(sum[:12])+".png")
}

// thumbVersion changes when the way a thumbnail is made does, so ones made
// the old way are not used.
const thumbVersion = 1

// NewPortrait builds the large image shown beside a scene, or nil when the
// character has none.
//
// The picture is read off the main thread and shown when it arrives. Read on
// it, a card's portrait held up every chat opened with that character by 11
// to 20ms, measured on a 1024 by 1536 PNG, about a sixth of what opening the
// chat costs. The last two read are kept, for going back and forth.
func NewPortrait(path string) *gtk.Picture {
	if path == "" {
		return nil
	}
	key, found := keyFor(path, 0)
	if !found {
		return nil
	}
	pic := gtk.NewPicture()
	// Contain, not Cover: a portrait is being looked at, so cropping the top
	// of someone's head to fill a panel is the wrong trade.
	pic.SetContentFit(gtk.ContentFitContain)
	pic.SetCanShrink(true)
	pic.SetVExpand(true)
	pic.SetHExpand(true)
	pic.AddCSSClass("portrait-image")
	if tex := portraits.get(key); tex != nil {
		pic.SetPaintable(tex)
		return pic
	}
	go func() {
		// GDK documents loading a texture from a file as safe on any thread,
		// for exactly this.
		tex, err := gdk.NewTextureFromFilename(path)
		glib.IdleAdd(func() bool {
			if err == nil && tex != nil {
				portraits.put(key, tex)
				pic.SetPaintable(tex)
			}
			return false
		})
	}()
	return pic
}

// portraits are the last portraits read, newest first.
var portraits portraitCache

type portraitCache struct {
	sync.Mutex
	keys []thumbKey
	texs []*gdk.Texture
}

func (c *portraitCache) get(k thumbKey) *gdk.Texture {
	c.Lock()
	defer c.Unlock()
	for i, have := range c.keys {
		if have == k {
			return c.texs[i]
		}
	}
	return nil
}

func (c *portraitCache) put(k thumbKey, tex *gdk.Texture) {
	c.Lock()
	defer c.Unlock()
	for i, have := range c.keys {
		if have == k {
			c.keys = append(c.keys[:i], c.keys[i+1:]...)
			c.texs = append(c.texs[:i], c.texs[i+1:]...)
			break
		}
	}
	c.keys = append([]thumbKey{k}, c.keys...)
	c.texs = append([]*gdk.Texture{tex}, c.texs...)
	if len(c.keys) > 2 {
		c.keys, c.texs = c.keys[:2], c.texs[:2]
	}
}

// NewImageThumb is a small rounded preview of an image file, or nil when the
// file is missing.
func NewImageThumb(path string, size int) gtk.Widgetter { return loadCropped(path, size) }
