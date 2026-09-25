package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
	"astral/internal/ui"
)

// maxImageBytes caps what will be copied in as a character image. Vision
// models are slow enough on a large photograph that a cap is a kindness, and
// nothing in the interface displays one larger than a few hundred pixels.
const maxImageBytes = 12 << 20 // 12 MiB

// imageFilters is the file dialog's filter, kept in one place so picking an
// avatar and picking a portrait offer the same thing.
func imageFilters() *gio.ListStore {
	images := gtk.NewFileFilter()
	images.SetName("Images")
	for _, p := range []string{"*.png", "*.jpg", "*.jpeg", "*.webp", "*.gif", "*.bmp"} {
		images.AddPattern(p)
	}
	all := gtk.NewFileFilter()
	all.SetName("All files")
	all.AddPattern("*")

	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(images.Object)
	filters.Append(all.Object)
	return filters
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
		path, err := a.importImage(file.Path(), prefix)
		if err != nil {
			a.toast(err.Error())
			return
		}
		onPicked(path)
	})
}

// importImage copies src into the data directory under a unique name.
func (a *App) importImage(src, prefix string) (string, error) {
	if src == "" {
		return "", fmt.Errorf("no file chosen")
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("could not read that file: %w", err)
	}
	if info.Size() > maxImageBytes {
		return "", fmt.Errorf("that image is %d MB; the limit is %d MB",
			info.Size()>>20, maxImageBytes>>20)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("could not read that file: %w", err)
	}
	if err := os.MkdirAll(store.AvatarDir(), 0o755); err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		ext = ".png"
	}
	// Stamped, so replacing an image does not fight the old one for the same
	// filename while GTK still has the decoded texture cached against it.
	name := fmt.Sprintf("%s-%d%s", safeFileName(prefix), time.Now().UnixNano(), ext)
	dst := filepath.Join(store.AvatarDir(), name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", fmt.Errorf("could not save the image: %w", err)
	}
	return dst, nil
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
