//go:build !race

// Not under -race: this starts GDK's content machinery, and the race
// detector's pointer checks abort inside gotk4 as soon as GTK is touched.

package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// A file manager's drop arrives as text/uri-list, which GTK turns into a
// GdkFileList: a boxed list, not an object. Asking it for an object gave nil,
// so every drop out of Files was refused whatever the file was. This builds the
// value the same way a drop does and checks the file comes out of it.
func TestAFileManagerDropIsRead(t *testing.T) {
	// GTK registers its converters from uri lists to files when it starts.
	// No window is opened.
	if !gtk.InitCheck() {
		t.Skip("no display to start GTK on")
	}
	dir := t.TempDir()
	photo := filepath.Join(dir, "Holiday Photo.JPG")
	if err := os.WriteFile(photo, []byte("not decoded here"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := gio.NewFileForPath(photo).URI() + "\r\n"

	for _, gtype := range []glib.Type{gdk.GTypeFileList, gio.GTypeFile} {
		stream := gio.NewMemoryInputStreamFromBytes(glib.NewBytes([]byte(uri)))
		done := false
		var files []*gio.File
		var err error
		gdk.ContentDeserializeAsync(context.Background(), stream, "text/uri-list", gtype, 0,
			func(res gio.AsyncResulter) {
				var value glib.Value
				value, err = gdk.ContentDeserializeFinish(res)
				if err == nil {
					files = clipboardFiles(&value)
				}
				done = true
			})
		deadline := time.Now().Add(5 * time.Second)
		for !done && time.Now().Before(deadline) {
			glib.MainContextDefault().Iteration(false)
			time.Sleep(time.Millisecond)
		}
		if !done {
			t.Fatalf("%v: the drop was never read", gtype)
		}
		if err != nil {
			t.Fatalf("%v: %v", gtype, err)
		}
		if len(files) != 1 || files[0].Path() != photo {
			t.Fatalf("%v: got %d files, want %s", gtype, len(files), photo)
		}
		if !looksLikeImage(files[0].Basename()) {
			t.Errorf("%q was not taken for an image", files[0].Basename())
		}
	}
}
