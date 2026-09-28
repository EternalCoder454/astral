package serve

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// Serving the phone's own files.
//
// http.FileServer over an embedded file system sends no validator: embedded
// files have no modification time, and it makes no ETag. So the phone could
// never tell that what it had was still current, and fetched the page, its
// script, its styles and both fonts again on every launch, about 1.8 MB, most
// of it two uncompressed font files. This sends an ETag made from each file's
// contents, answers a phone that already has it with 304, compresses what
// compresses, and lets the fonts and icons be kept for a while without asking.

type staticFiles struct {
	fsys  fs.FS
	mu    sync.Mutex
	files map[string]*staticFile
}

type staticFile struct {
	etag, ctype string
	plain, gz   []byte
}

func newStatic(fsys fs.FS) *staticFiles {
	return &staticFiles{fsys: fsys, files: map[string]*staticFile{}}
}

func (sf *staticFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || strings.HasSuffix(r.URL.Path, "/") {
		name = path.Join(name, "index.html")
	}
	f, err := sf.load(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("ETag", f.etag)
	h.Set("Cache-Control", cacheFor(name))
	h.Set("Content-Type", f.ctype)
	h.Add("Vary", "Accept-Encoding")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, f.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := f.plain
	if f.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		body = f.gz
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}

// load reads a file once, and its compressed copy with it.
func (sf *staticFiles) load(name string) (*staticFile, error) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	if f, ok := sf.files[name]; ok {
		return f, nil
	}
	data, err := fs.ReadFile(sf.fsys, name)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	f := &staticFile{etag: `"` + hex.EncodeToString(sum[:8]) + `"`, ctype: contentType(name, data), plain: data}
	if compressible(f.ctype) {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		zw.Write(data)
		zw.Close()
		if buf.Len() < len(data)*9/10 {
			f.gz = buf.Bytes()
		}
	}
	sf.files[name] = f
	return f, nil
}

// cacheFor is how long a phone may keep a file without asking. The page, its
// script and its styles change with every release, so they are asked about
// each time, and a 304 costs nothing; fonts and icons almost never change.
func cacheFor(name string) string {
	switch {
	case strings.HasPrefix(name, "fonts/"), strings.HasSuffix(name, ".ttf"):
		return "public, max-age=604800"
	case strings.HasSuffix(name, ".svg"):
		return "public, max-age=86400"
	}
	return "no-cache"
}

func contentType(name string, data []byte) string {
	switch path.Ext(name) {
	case ".ttf":
		return "font/ttf"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return http.DetectContentType(data)
}

func compressible(ctype string) bool {
	return strings.HasPrefix(ctype, "text/") || strings.HasPrefix(ctype, "font/") ||
		strings.Contains(ctype, "javascript") || strings.Contains(ctype, "json") ||
		strings.Contains(ctype, "svg")
}
