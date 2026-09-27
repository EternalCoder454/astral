package ui

import (
	"context"
	"path/filepath"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
)

// Getting a picture into a design chat.
//
// There was one way to do it: a button that opens a file chooser. That is the
// slowest of the three ways anyone actually has a picture to hand. A reference
// found in a browser is on the clipboard, a reference on disk is under the
// pointer in a file manager, and neither of those wants a dialog asking which
// folder it is in.
//
// So the composer takes a pasted image and the whole chat takes a dropped one,
// and because a drop with no feedback is indistinguishable from a drop into
// nothing, dragging a file over the chat says what will happen to it.

// imageExts is what a dropped or pasted file is allowed to be. It matches the
// file chooser's filter, and imageconv converts the rest to PNG on the way in.
var imageExts = []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp"}

// looksLikeImage reports whether a path is worth handing to the importer. It
// is a first pass on the name only: the importer decodes the bytes and is the
// thing that actually decides, which is what catches a .png that is not one.
func looksLikeImage(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, want := range imageExts {
		if ext == want {
			return true
		}
	}
	return false
}

// buildDropOverlay wraps the chat in the layer that shows a drop is possible.
//
// The indicator cannot be targeted, so it never intercepts the drop it is
// advertising: a drop landing on the notice that says where to drop is the
// classic way to build this wrong.
func (c *ChatView) buildDropOverlay(content gtk.Widgetter) *gtk.Overlay {
	card := gtk.NewBox(gtk.OrientationVertical, 8)
	card.AddCSSClass("drop-card")
	card.SetHAlign(gtk.AlignCenter)
	card.SetVAlign(gtk.AlignCenter)

	icon := gtk.NewImageFromIconName(IconFolder)
	icon.SetPixelSize(32)
	card.Append(icon)

	c.dropTitle = gtk.NewLabel("Drop an Image Here")
	c.dropTitle.AddCSSClass("drop-card-title")
	card.Append(c.dropTitle)

	c.dropHint = gtk.NewLabel("")
	c.dropHint.AddCSSClass("settings-hint")
	c.dropHint.SetWrap(true)
	c.dropHint.SetJustify(gtk.JustifyCenter)
	card.Append(c.dropHint)

	c.dropRevealer = gtk.NewRevealer()
	c.dropRevealer.SetChild(card)
	c.dropRevealer.SetTransitionType(gtk.RevealerTransitionTypeCrossfade)
	c.dropRevealer.SetTransitionDuration(90)
	c.dropRevealer.SetRevealChild(false)
	c.dropRevealer.SetCanTarget(false)
	c.dropRevealer.SetHAlign(gtk.AlignFill)
	c.dropRevealer.SetVAlign(gtk.AlignFill)
	c.dropRevealer.AddCSSClass("drop-veil")

	overlay := gtk.NewOverlay()
	overlay.SetChild(content)
	overlay.AddOverlay(c.dropRevealer)
	return overlay
}

// showDrop raises or lowers the drop indicator.
func (c *ChatView) showDrop(on bool, why string) {
	if c.dropRevealer == nil {
		return
	}
	if on && c.dropHint != nil {
		// The title says what will happen, so it cannot keep offering to take
		// an image while the line under it explains that nothing will be taken.
		if why == "" {
			c.dropTitle.SetText("Drop an Image Here")
		} else {
			c.dropTitle.SetText("Not Here")
		}
		c.dropHint.SetText(why)
		c.dropHint.SetVisible(why != "")
	}
	c.dropRevealer.SetRevealChild(on)
}

// acceptsImages reports whether an image would be used if one arrived, which
// is the same question the attach button answers by being visible or not.
func (c *ChatView) acceptsImages() bool { return c.canAttach }

// dropRefusal is why an image is not being taken, phrased for the person
// holding one over the window. Empty when it would be accepted.
//
// Worth saying rather than silently refusing: the reason is almost always the
// model, an image is only ever offered to a design chat, and "nothing happened
// when I dropped it" is indistinguishable from a bug.
func (c *ChatView) dropRefusal() string {
	if c.acceptsImages() {
		return ""
	}
	if c.Chat().Kind != store.KindDesigner {
		return "Images go to a design chat, where the model is describing somebody."
	}
	return "This model cannot see images. Choose one that can, and try again."
}

// installImageDrop lets an image file or an image itself be dropped anywhere
// on the chat.
//
// Three types are offered because the three sources disagree: a file manager
// sends a list of files, a single file arrives as one file, and a browser
// sends the decoded picture with no file behind it at all.
func (c *ChatView) installImageDrop(overlay *gtk.Overlay) {
	target := gtk.NewDropTarget(gio.GTypeFile, gdk.ActionCopy)
	target.SetGTypes([]coreglib.Type{gdk.GTypeFileList, gio.GTypeFile, gdk.GTypeTexture})

	target.ConnectEnter(func(x, y float64) gdk.DragAction {
		c.showDrop(true, c.dropRefusal())
		if !c.acceptsImages() {
			// Shown, so the reason is readable, but refused, so the pointer
			// says no and the file goes back where it came from.
			return 0
		}
		return gdk.ActionCopy
	})
	target.ConnectLeave(func() { c.showDrop(false, "") })
	target.ConnectDrop(func(value *coreglib.Value, x, y float64) bool {
		c.showDrop(false, "")
		if !c.acceptsImages() {
			return false
		}
		return c.takeDropped(value)
	})
	overlay.AddController(target)
}

// takeDropped turns whatever was dropped into an attachment.
func (c *ChatView) takeDropped(value *coreglib.Value) bool {
	if value == nil {
		return false
	}
	obj := value.Object()
	if obj == nil {
		return false
	}
	switch v := obj.Cast().(type) {
	case gdk.Texturer:
		return c.attachTexture(v)
	case gio.Filer:
		return c.attachFile(v.Path())
	}
	return false
}

// attachFile queues an image that already exists on disk.
func (c *ChatView) attachFile(path string) bool {
	if path == "" || c.OnImageFile == nil {
		return false
	}
	if !looksLikeImage(path) {
		c.fail("That is not an image Astral can read.")
		return false
	}
	c.OnImageFile(path)
	return true
}

// attachTexture queues an image that arrived as pixels rather than a file.
//
// Encoded to PNG here because that is the only form the rest of the path
// understands, and because the sender is usually a browser or a screenshot
// tool, which hands over a decoded picture with no file behind it.
func (c *ChatView) attachTexture(tex gdk.Texturer) bool {
	if c.OnImageBytes == nil {
		return false
	}
	bytes := gdk.BaseTexture(tex).SaveToPNGBytes()
	if bytes == nil {
		c.fail("That image could not be read.")
		return false
	}
	c.OnImageBytes(bytes.Data())
	return true
}

// pasteImageIntoComposer makes Ctrl+V in the message box accept a picture.
//
// Text still pastes as text. The clipboard is asked what it is holding before
// the key is claimed, so this only takes over when there is an image and no
// text, and an ordinary paste is untouched.
func (c *ChatView) pasteImageIntoComposer() {
	key := gtk.NewEventControllerKey()
	key.SetPropagationPhase(gtk.PhaseCapture)
	key.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if state&gdk.ControlMask == 0 {
			return false
		}
		if keyval != gdk.KEY_v && keyval != gdk.KEY_V {
			return false
		}
		clip := gtk.BaseWidget(c.composer).Clipboard()
		if clip == nil {
			return false
		}
		formats := clip.Formats()
		if formats == nil {
			return false
		}
		// Two shapes, because the two ways of copying a picture disagree.
		// Copying one out of a browser or a screenshot tool puts the decoded
		// image on the clipboard; copying the file itself in a file manager
		// puts a path, and nothing else. Handling only the first is what made
		// pasting a .png look broken, since that is the copy people make.
		hasImage := formats.ContainGType(gdk.GTypeTexture)
		hasFile := formats.ContainGType(gio.GTypeFile) || formats.ContainGType(gdk.GTypeFileList)
		if !hasImage && !hasFile {
			return false // ordinary text paste, left alone
		}
		if hasFile && !hasImage && !clipboardHoldsAnImageFile(formats) {
			return false // some other file, or plain text that came with a path
		}
		if !c.acceptsImages() {
			c.fail(c.dropRefusal())
			return true
		}
		if hasImage {
			clip.ReadTextureAsync(context.Background(), func(res gio.AsyncResulter) {
				tex, err := clip.ReadTextureFinish(res)
				if err != nil || tex == nil {
					c.fail("That image could not be pasted.")
					return
				}
				c.attachTexture(tex)
			})
			return true
		}
		clip.ReadValueAsync(context.Background(), gio.GTypeFile, 0, func(res gio.AsyncResulter) {
			value, err := clip.ReadValueFinish(res)
			if err != nil || value == nil {
				c.fail("That image could not be pasted.")
				return
			}
			c.takeDropped(value)
		})
		return true
	})
	c.composer.AddController(key)
}

// DevShowDrop raises the drop indicator so a dev run can capture it. There is
// no way to synthesise a drag from inside the process, and an indicator nobody
// has looked at is an indicator nobody knows the size of.
func (c *ChatView) DevShowDrop(refused bool) {
	c.canAttach = !refused
	c.showDrop(true, c.dropRefusal())
}

// clipboardHoldsAnImageFile reports whether a clipboard carrying a file
// carries a picture, by the media types offered alongside it.
//
// Asked before the key is claimed, so copying a .txt in a file manager and
// pasting it into the message box still pastes its name as text, which is what
// it did before any of this existed.
func clipboardHoldsAnImageFile(formats *gdk.ContentFormats) bool {
	for _, mime := range imageMIMEs {
		if formats.ContainMIMEType(mime) {
			return true
		}
	}
	return false
}

// imageMIMEs mirrors imageExts. A clipboard names what it is holding by media
// type rather than by extension, and the two lists have to agree.
var imageMIMEs = []string{
	"image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp",
}
