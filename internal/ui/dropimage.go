package ui

import (
	"context"
	"fmt"

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
	card.SetVExpand(true)

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

	// The veil is the revealer's child, not the revealer.
	//
	// A GtkRevealer with reveal-child off hides what is inside it and stays
	// visible itself, so a background painted on the revealer is painted all
	// the time. With the revealer filling the overlay, that dimmed the whole
	// chat permanently and made the app look like it was waiting for a file
	// nobody was dragging.
	veil := gtk.NewBox(gtk.OrientationVertical, 0)
	veil.AddCSSClass("drop-veil")
	veil.SetHAlign(gtk.AlignFill)
	veil.SetVAlign(gtk.AlignFill)
	veil.Append(card)

	c.dropRevealer = gtk.NewRevealer()
	c.dropRevealer.SetChild(veil)
	c.dropRevealer.SetTransitionType(gtk.RevealerTransitionTypeCrossfade)
	c.dropRevealer.SetTransitionDuration(90)
	c.dropRevealer.SetRevealChild(false)
	c.dropRevealer.SetCanTarget(false)
	c.dropRevealer.SetHAlign(gtk.AlignFill)
	c.dropRevealer.SetVAlign(gtk.AlignFill)
	// Hidden outright as well, so nothing in this layer can draw over the chat
	// while no drag is happening. Belt as well as braces, because the bug this
	// replaces was exactly a layer that was only supposed to be invisible.
	c.dropRevealer.SetVisible(false)

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
	c.dropRevealer.SetVisible(on)
	c.dropRevealer.SetRevealChild(on)
}

// acceptsImages reports whether an image would be used if one arrived, which
// is the same question the attach button answers by being visible or not.
func (c *ChatView) acceptsImages() bool { return c.canAttach }

// dropRefusal is why an image is not being taken, phrased for the person
// holding one over the window. Empty when it would be accepted.
//
// Worth saying rather than silently refusing: "nothing happened when I dropped
// it" is indistinguishable from a bug.
func (c *ChatView) dropRefusal() string {
	if c.acceptsImages() {
		return ""
	}
	switch c.Chat().Kind {
	case store.KindDesigner, store.KindAssistant:
	default:
		return "Images go to a design chat or a plain chat. A scene has no way to show one."
	}
	return "None of your models can see images. Pull one that can, such as gemma3 or qwen2.5vl, then choose Check Again in Settings."
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
//
// A file manager's drop arrives as a GdkFileList, which is a boxed list and not
// an object. Asking the value for an object, which is what this used to do for
// everything, answers nil for a list, so every drop out of Files was quietly
// refused whatever the file was.
func (c *ChatView) takeDropped(value *coreglib.Value) bool {
	if value == nil {
		return false
	}
	if value.Type() == gdk.GTypeFileList || value.Type() == gio.GTypeFile {
		return c.attachFiles(clipboardFiles(value))
	}
	obj := value.Object()
	if obj == nil {
		return false
	}
	if tex, ok := obj.Cast().(gdk.Texturer); ok {
		return c.attachTexture(tex)
	}
	return false
}

// attachFiles takes the picture out of a list of dropped files.
//
// One goes with each message, so from several the first picture is taken and
// the rest are named as left behind rather than silently dropped. A single file
// is always tried, whatever it is called: an image saved without an extension
// is still an image, and the importer is the one that can tell.
func (c *ChatView) attachFiles(files []*gio.File) bool {
	if len(files) == 0 {
		return false
	}
	if len(files) == 1 {
		return c.attachFile(files[0])
	}
	var images []*gio.File
	for _, f := range files {
		if looksLikeImage(f.Basename()) {
			images = append(images, f)
		}
	}
	if len(images) == 0 {
		c.fail("None of those are images Astral can read.")
		return false
	}
	if len(images) > 1 {
		c.fail(fmt.Sprintf("Attached the first of %d images. One goes with each message.", len(images)))
	}
	return c.attachFile(images[0])
}

// attachFile queues a file as an image.
//
// A file with no local path is on a network share or a phone plugged in over
// USB, and is read through GIO instead, off this thread because reading it
// means waiting on the network or the cable.
func (c *ChatView) attachFile(file *gio.File) bool {
	if file == nil || c.OnImageFile == nil || c.OnImageBytes == nil {
		return false
	}
	if path := file.Path(); path != "" {
		c.OnImageFile(path)
		return true
	}
	go func() {
		data, _, err := file.LoadContents(context.Background())
		coreglib.IdleAdd(func() bool {
			if err != nil {
				c.fail("Could not read that file: " + err.Error())
			} else {
				c.OnImageBytes(data)
			}
			return false
		})
	}()
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
// the key is claimed, so this only takes over when there is an image or a file
// and no text, and an ordinary paste is untouched.
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
		// puts a list of files, and nothing that says they are pictures.
		hasImage := formats.ContainGType(gdk.GTypeTexture)
		hasList := formats.ContainGType(gdk.GTypeFileList)
		hasFile := hasList || formats.ContainGType(gio.GTypeFile)
		if !hasImage && !hasFile {
			return false // ordinary text paste, left alone
		}
		if hasImage {
			if !c.acceptsImages() {
				c.fail(c.dropRefusal())
				return true
			}
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
		// A file. Whether it is a picture is only known once the list is
		// read, which is asynchronous, so the key is claimed now and, when
		// the file turns out not to be a picture, the text that Ctrl+V would
		// have pasted is pasted. Copying a .txt in Files and pasting it here
		// still gives its name, as it always did.
		gtype := gio.GTypeFile
		if hasList {
			gtype = gdk.GTypeFileList
		}
		clip.ReadValueAsync(context.Background(), gtype, 0, func(res gio.AsyncResulter) {
			value, err := clip.ReadValueFinish(res)
			if err != nil || value == nil {
				c.pasteText(clip)
				return
			}
			files := clipboardFiles(value)
			var image *gio.File
			for _, f := range files {
				if looksLikeImage(f.Basename()) {
					image = f
					break
				}
			}
			switch {
			case image == nil:
				c.pasteText(clip)
			case !c.acceptsImages():
				c.fail(c.dropRefusal())
			default:
				c.attachFile(image)
			}
		})
		return true
	})
	c.composer.AddController(key)
}

// clipboardFiles reads the files out of a dropped or pasted value, which holds
// a list or a single file depending on which type was asked for.
//
// The single file is wrapped directly rather than cast: what GIO hands over is
// a private subclass (a local file, a network one) that has no Go type of its
// own to cast to.
func clipboardFiles(value *coreglib.Value) []*gio.File {
	switch value.Type() {
	case gdk.GTypeFileList:
		if list, ok := value.GoValue().(*gdk.FileList); ok && list != nil {
			return list.Files()
		}
	case gio.GTypeFile:
		if obj := value.Object(); obj != nil {
			return []*gio.File{{Object: obj}}
		}
	}
	return nil
}

// pasteText does what Ctrl+V would have done, for a paste that was claimed in
// case it was a picture and turned out not to be.
func (c *ChatView) pasteText(clip *gdk.Clipboard) {
	clip.ReadTextAsync(context.Background(), func(res gio.AsyncResulter) {
		text, err := clip.ReadTextFinish(res)
		if err != nil || text == "" {
			return
		}
		buf := c.composer.Buffer()
		buf.DeleteSelection(true, true)
		buf.InsertAtCursor(text)
	})
}

// DevShowDrop raises the drop indicator so a dev run can capture it. There is
// no way to synthesise a drag from inside the process, and an indicator nobody
// has looked at is an indicator nobody knows the size of.
func (c *ChatView) DevShowDrop(refused bool) {
	c.canAttach = !refused
	c.showDrop(true, c.dropRefusal())
}
