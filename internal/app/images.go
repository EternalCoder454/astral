package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/imageconv"
	"astral/internal/store"
	"astral/internal/ui"
)

// maxImageBytes caps what will be read in as an image at all.
//
// Imports are brought down to imageconv.MaxSide whatever they arrive as, so
// this bounds the file, not what is kept. A 50 megapixel phone photograph is
// around 20 MB, and a screenshot of a 4K screen saved as PNG can be larger.
const maxImageBytes = 48 << 20 // 48 MiB

// imageFilters is the file dialog's filter, kept in one place so picking an
// avatar and picking a portrait offer the same thing.
//
// Suffixes rather than patterns: a pattern's case sensitivity depends on the
// platform, and on Linux "*.jpg" hides every photograph a camera named
// IMG_0001.JPG. The gdk-pixbuf formats are added as well, which matches files
// by what they contain.
func imageFilters() *gio.ListStore {
	images := gtk.NewFileFilter()
	images.SetName("Images")
	for _, ext := range ui.ImageSuffixes() {
		images.AddSuffix(ext)
	}
	images.AddPixbufFormats()
	all := gtk.NewFileFilter()
	all.SetName("All Files")
	all.AddPattern("*")

	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(images.Object)
	filters.Append(all.Object)
	return filters
}

// textSuffixes are the text files the attach button offers. Anything else can
// still be chosen under All Files, and is read as text if it is text.
var textSuffixes = []string{
	"md", "markdown", "txt", "text", "json", "yaml", "yml", "toml", "csv", "tsv",
	"xml", "html", "htm", "org", "rst", "adoc", "tex", "log", "ini", "conf", "srt", "vtt",
}

// pickAttachment opens a file chooser for the attach button: text files for
// the model to read, and pictures when the chat's model can see.
func (a *App) pickAttachment() {
	text := gtk.NewFileFilter()
	text.SetName("Text Files")
	text.AddMIMEType("text/*")
	for _, ext := range textSuffixes {
		text.AddSuffix(ext)
	}
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	if a.chat.CanAttachImages() {
		both := gtk.NewFileFilter()
		both.SetName("Text Files and Images")
		both.AddMIMEType("text/*")
		for _, ext := range textSuffixes {
			both.AddSuffix(ext)
		}
		for _, ext := range ui.ImageSuffixes() {
			both.AddSuffix(ext)
		}
		both.AddPixbufFormats()
		filters.Append(both.Object)
	}
	filters.Append(text.Object)
	if a.chat.CanAttachImages() {
		images := imageFilters()
		filters.Append(images.Item(0))
	}
	all := gtk.NewFileFilter()
	all.SetName("All Files")
	all.AddPattern("*")
	filters.Append(all.Object)

	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Attach a File")
	dialog.SetFilters(filters)
	dialog.Open(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := dialog.OpenFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		a.chat.AttachPicked(file)
	})
}

// pickImage opens a file chooser, copies the chosen image into Astral's own
// data directory, and hands back the new path.
//
// It is copied rather than referenced because a character outlives whatever
// folder you happened to pick the picture from. A path into Downloads is a
// broken image the next time that folder is tidied.
func (a *App) pickImage(title, prefix string, onPicked func(path string)) {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle(title)
	dialog.SetFilters(imageFilters())

	dialog.Open(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := dialog.OpenFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		if path := file.Path(); path != "" {
			a.importImageAsync(path, nil, prefix, onPicked)
			return
		}
		// Somewhere with no local path, such as a phone over USB.
		go func() {
			data, _, err := file.LoadContents(context.Background())
			coreglib.IdleAdd(func() bool {
				if err != nil {
					a.toast("Could not read that file: " + err.Error())
				} else {
					a.importImageAsync("", data, prefix, onPicked)
				}
				return false
			})
		}()
	})
}

// importImageAsync imports an image off the UI thread and hands the saved
// path to done on it. src is a file to read; when it is empty, data is the
// image itself.
//
// Off the thread because decoding, turning and shrinking a phone photograph
// takes a noticeable fraction of a second, and a HEIC goes through a sandboxed
// decoder process on top of that. The window should not stop while it does.
func (a *App) importImageAsync(src string, data []byte, prefix string, done func(path string)) {
	go func() {
		var path, note string
		var err error
		if src != "" {
			path, note, err = importImage(src, prefix)
		} else {
			path, note, err = importImageBytes(data, prefix)
		}
		coreglib.IdleAdd(func() bool {
			if err != nil {
				a.toast(err.Error())
				return false
			}
			if note != "" {
				a.toast(note)
			}
			done(path)
			return false
		})
	}()
}

// importImage copies src into the data directory under a unique name. note is
// something worth telling the person, such as that the format was converted.
func importImage(src, prefix string) (path, note string, err error) {
	if src == "" {
		return "", "", fmt.Errorf("no file chosen")
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", "", fmt.Errorf("could not read that file: %w", err)
	}
	if info.Size() > maxImageBytes {
		return "", "", fmt.Errorf("that image is %d MB; the limit is %d MB",
			info.Size()>>20, maxImageBytes>>20)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", "", fmt.Errorf("could not read that file: %w", err)
	}
	return importImageBytes(data, prefix)
}

// importImageBytes is the same for an image that never was a file: one pasted
// from the clipboard, or dropped out of a browser, which arrives as pixels.
func importImageBytes(data []byte, prefix string) (path, note string, err error) {
	if len(data) == 0 {
		return "", "", fmt.Errorf("that image was empty")
	}
	if len(data) > maxImageBytes {
		return "", "", fmt.Errorf("that image is %d MB; the limit is %d MB",
			len(data)>>20, maxImageBytes>>20)
	}

	// Everything becomes PNG or JPEG here, upright and no larger than a
	// vision model reads at. Ollama's vision path expects one of those two,
	// and showing anything else needs a loader that may not be installed, so
	// converting once at import is cheaper than finding out at either point
	// of use. See imageconv for why upright and smaller matter.
	//
	// This also validates: a truncated download fails now, with something
	// worth reading, rather than becoming a broken preview later.
	data, ext, was, err := normalizeImage(data)
	if err != nil {
		return "", "", err
	}
	if was != "" {
		note = fmt.Sprintf("Converted that %s image.", strings.ToUpper(was))
	}

	if err := os.MkdirAll(store.AvatarDir(), 0o755); err != nil {
		return "", "", err
	}
	// Stamped, so replacing an image does not fight the old one for the same
	// filename while GTK still has the decoded texture cached against it.
	name := fmt.Sprintf("%s-%d%s", safeFileName(prefix), time.Now().UnixNano(), ext)
	dst := filepath.Join(store.AvatarDir(), name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", "", fmt.Errorf("could not save the image: %w", err)
	}
	return dst, note, nil
}

// normalizeImage is imageconv.Normalize with a second decoder behind it. was
// names the original format when it is one people would be surprised to see
// converted, and is empty for PNG and JPEG.
//
// Go reads the common formats itself. The ones phones and the web have moved
// to since (HEIC, AVIF, JPEG XL) and a few older ones (icons, SVG, TGA) it
// does not, so those go through gdk-pixbuf, which uses whatever loaders this
// system has installed, and come back as PNG for imageconv to finish.
func normalizeImage(data []byte) (out []byte, ext, was string, err error) {
	if f := imageconv.Format(data); f != "" && f != "png" && f != "jpeg" {
		was = f
	}
	out, ext, err = imageconv.Normalize(data)
	if !errors.Is(err, imageconv.ErrUnknownFormat) {
		return out, ext, was, err
	}
	png, format, perr := decodeWithPixbuf(data)
	if perr != nil {
		return nil, "", "", err // "not an image Astral can read" says it better
	}
	out, ext, err = imageconv.Normalize(png)
	return out, ext, format, err
}

// decodeWithPixbuf decodes an image through gdk-pixbuf and returns it as PNG,
// upright and no larger than imageconv.MaxSide, with the name of the format it
// was.
func decodeWithPixbuf(data []byte) (png []byte, format string, err error) {
	loader := gdkpixbuf.NewPixbufLoader()
	if err := loader.Write(data); err != nil {
		_ = loader.Close()
		return nil, "", err
	}
	if err := loader.Close(); err != nil {
		return nil, "", err
	}
	pb := loader.Pixbuf()
	if pb == nil {
		return nil, "", fmt.Errorf("nothing was decoded")
	}
	if f := loader.Format(); f != nil {
		format = f.Name()
	}
	// A HEIC from a phone carries its orientation the same way a JPEG does.
	if turned := pb.ApplyEmbeddedOrientation(); turned != nil {
		pb = turned
	}
	// Shrunk here rather than left to imageconv, so a 48 megapixel HEIC is
	// not encoded to a PNG of the same size only to be decoded again.
	if w, h := pb.Width(), pb.Height(); w > imageconv.MaxSide || h > imageconv.MaxSide {
		if w >= h {
			h, w = max(1, h*imageconv.MaxSide/w), imageconv.MaxSide
		} else {
			w, h = max(1, w*imageconv.MaxSide/h), imageconv.MaxSide
		}
		if scaled := pb.ScaleSimple(w, h, gdkpixbuf.InterpBilinear); scaled != nil {
			pb = scaled
		}
	}
	png, err = pb.SaveToBufferv("png", nil, nil)
	return png, format, err
}

// imageField is a labelled image picker: a preview, a button to choose, and a
// button to remove. get and set read and write the path on the character being
// edited.
func (a *App) imageField(label, hint, prefix string, get func() string, set func(string)) *gtk.Box {
	preview := gtk.NewBox(gtk.OrientationHorizontal, 0)
	preview.SetSizeRequest(64, 64)
	preview.SetVAlign(gtk.AlignCenter)

	refresh := func() {
		for {
			child := preview.FirstChild()
			if child == nil {
				break
			}
			preview.Remove(child)
		}
		if path := get(); path != "" {
			if img := ui.NewImageThumb(path, 64); img != nil {
				preview.Append(img)
				return
			}
		}
		empty := gtk.NewLabel("None")
		empty.AddCSSClass("settings-hint")
		preview.Append(empty)
	}
	refresh()

	choose := gtk.NewButtonWithLabel("Choose…")
	choose.ConnectClicked(func() {
		a.pickImage(label, prefix, func(path string) {
			set(path)
			refresh()
		})
	})
	clear := gtk.NewButtonWithLabel("Remove")
	clear.ConnectClicked(func() {
		set("")
		refresh()
	})

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	buttons.SetVAlign(gtk.AlignCenter)
	buttons.Append(choose)
	buttons.Append(clear)

	row := gtk.NewBox(gtk.OrientationHorizontal, 12)
	row.Append(preview)
	row.Append(buttons)

	return labelledField(label, hint, row)
}
