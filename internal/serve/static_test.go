package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The phone keeps what it has: every file carries an ETag, a phone that sends
// it back is told 304 with nothing to download, and the fonts arrive
// compressed.
func TestThePhoneCanKeepWhatItHas(t *testing.T) {
	s, _ := testServer(t)
	get := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		return w
	}
	for _, p := range []string{"/", "/app.js", "/style.css", "/fonts/AdwaitaSans-Regular.ttf", "/icons/astral-send-symbolic.svg"} {
		w := get(p, nil)
		tag := w.Header().Get("ETag")
		if w.Code != http.StatusOK || tag == "" {
			t.Errorf("%s = %d with ETag %q", p, w.Code, tag)
			continue
		}
		if again := get(p, map[string]string{"If-None-Match": tag}); again.Code != http.StatusNotModified || again.Body.Len() != 0 {
			t.Errorf("%s with its ETag = %d and %d bytes, want 304 and none", p, again.Code, again.Body.Len())
		}
	}
	plain := get("/fonts/AdwaitaSans-Regular.ttf", nil).Body.Len()
	gz := get("/fonts/AdwaitaSans-Regular.ttf", map[string]string{"Accept-Encoding": "gzip"})
	if gz.Header().Get("Content-Encoding") != "gzip" || gz.Body.Len() >= plain {
		t.Errorf("the font is %d bytes plain and %d as sent compressed (%q)", plain, gz.Body.Len(), gz.Header().Get("Content-Encoding"))
	}
}
