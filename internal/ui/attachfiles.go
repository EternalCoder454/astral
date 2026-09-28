package ui

import (
	"context"
	"fmt"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/chars"
	"astral/internal/store"
)

// Text files for the designers and plain chats.
//
// Notes in a .md file, a character sheet exported from somewhere else, a
// world bible: dropped on the chat, pasted, or picked with the attach button,
// each goes with the next message for the model to read, and stays in the
// conversation after it. A long paste becomes one too, so a page of notes
// does not fill the message box.

// maxFiles is how many text files wait for one message.
const maxFiles = 5

// longPaste is the length at which pasted text goes as a file rather than
// into the message box.
const longPaste = 2500

// attachedText is a text file waiting to go with the next message.
type attachedText struct {
	name, text string
	cut        bool
}

// acceptsFiles reports whether this chat takes text files: every kind but a
// scene, whose character reads only what is said in it.
func (c *ChatView) acceptsFiles() bool {
	k := c.Chat().Kind
	return k != "" && k != store.KindRoleplay
}

// attachText reads a file and queues it as text, or, when it is not text,
// tries it as a picture. Off this thread, because the file may be on a
// network share or a phone.
func (c *ChatView) attachText(file *gio.File) {
	name := file.Basename()
	go func() {
		data, _, err := file.LoadContents(context.Background())
		coreglib.IdleAdd(func() bool {
			if err != nil {
				c.fail("Could not read " + name + ": " + err.Error())
				return false
			}
			text, cut, terr := chars.ReadAttachable(data)
			switch {
			case terr == nil && c.acceptsFiles():
				c.AttachText(name, text, cut)
			case terr == nil:
				c.fail(c.dropRefusal())
			case c.acceptsImages() && c.OnImageBytes != nil:
				c.OnImageBytes(data)
			default:
				c.fail(fmt.Sprintf("Could not attach %s, since only text files can be read: %v", name, terr))
			}
			return false
		})
	}()
}

// AttachText queues text to go with the next message as a file called name.
func (c *ChatView) AttachText(name, text string, cut bool) {
	if len(c.files) >= maxFiles {
		c.fail(fmt.Sprintf("%d files at most go with one message.", maxFiles))
		return
	}
	c.files = append(c.files, attachedText{name: name, text: text, cut: cut})
	if cut {
		c.fail(fmt.Sprintf("%s is long, so only its first %d characters go with the message.",
			name, chars.MaxAttachedChars))
	}
	c.showFiles()
}

// clearFiles drops every queued file.
func (c *ChatView) clearFiles() {
	if len(c.files) == 0 {
		return
	}
	c.files = nil
	c.showFiles()
}

// takeFiles is the queued files as the blocks the message carries, and
// empties the queue.
func (c *ChatView) takeFiles() string {
	if len(c.files) == 0 {
		return ""
	}
	blocks := make([]string, len(c.files))
	for i, f := range c.files {
		blocks[i] = chars.AttachedFile(f.name, f.text)
	}
	c.clearFiles()
	return strings.Join(blocks, "\n\n")
}

// showFiles draws a chip above the message box for each queued file.
func (c *ChatView) showFiles() {
	if c.fileChips == nil {
		return
	}
	for child := c.fileChips.FirstChild(); child != nil; child = c.fileChips.FirstChild() {
		c.fileChips.Remove(child)
	}
	for i, f := range c.files {
		i := i
		chip := gtk.NewBox(gtk.OrientationHorizontal, 6)
		chip.AddCSSClass("attach-chip")
		chip.Append(gtk.NewImageFromIconName(IconKnowledge))
		label := gtk.NewLabel(f.name)
		label.SetEllipsize(pango.EllipsizeMiddle)
		label.SetMaxWidthChars(24)
		label.SetTooltipText(fmt.Sprintf("%s, %d words", f.name, len(strings.Fields(f.text))))
		chip.Append(label)
		remove := gtk.NewButtonFromIconName(IconClose)
		remove.AddCSSClass("message-action")
		remove.SetTooltipText("Take " + f.name + " off this message")
		remove.ConnectClicked(func() {
			if i < len(c.files) {
				c.files = append(c.files[:i], c.files[i+1:]...)
				c.showFiles()
			}
		})
		chip.Append(remove)
		c.fileChips.Append(chip)
	}
	c.fileChips.SetVisible(len(c.files) > 0)
}

// pasteLongText pastes text as usual, unless it is long enough to be a
// document, when it goes with the message as a file instead.
func (c *ChatView) pasteLongText(clip *gdk.Clipboard) {
	clip.ReadTextAsync(context.Background(), func(res gio.AsyncResulter) {
		text, err := clip.ReadTextFinish(res)
		if err != nil || text == "" {
			return
		}
		if len([]rune(text)) < longPaste {
			buf := c.composer.Buffer()
			buf.DeleteSelection(true, true)
			buf.InsertAtCursor(text)
			return
		}
		body, cut, err := chars.ReadAttachable([]byte(text))
		if err != nil {
			return
		}
		c.AttachText(c.pastedName(), body, cut)
	})
}

// pastedName names a pasted file, numbered when there is more than one.
func (c *ChatView) pastedName() string {
	n := 1
	for _, f := range c.files {
		if strings.HasPrefix(f.name, "Pasted text") {
			n++
		}
	}
	if n == 1 {
		return "Pasted text.txt"
	}
	return fmt.Sprintf("Pasted text %d.txt", n)
}

// AttachPicked takes a file chosen with the attach button: a picture goes the
// way pictures go, anything else is read as text.
func (c *ChatView) AttachPicked(file *gio.File) {
	c.attachFiles([]*gio.File{file})
}
