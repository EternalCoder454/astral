package scene

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"astral/internal/ollama"
)

// fakeServer answers /api/ps, /api/tags and /api/generate, and counts loads.
type fakeServer struct {
	mu     sync.Mutex
	loaded []string
	sizes  map[string]int64
	loads  int
}

func (f *fakeServer) start(t *testing.T) *ollama.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/api/ps":
			var out struct {
				Models []map[string]any `json:"models"`
			}
			for _, n := range f.loaded {
				out.Models = append(out.Models, map[string]any{"name": n, "size": 1, "size_vram": 1})
			}
			json.NewEncoder(w).Encode(out)
		case "/api/tags":
			var out struct {
				Models []map[string]any `json:"models"`
			}
			for n, s := range f.sizes {
				out.Models = append(out.Models, map[string]any{"name": n, "size": s})
			}
			json.NewEncoder(w).Encode(out)
		case "/api/generate":
			f.loads++
			w.Write([]byte(`{"done":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return ollama.NewClient(srv.URL)
}

func resetPreload() {
	preloadMu.Lock()
	preloadLast = map[string]time.Time{}
	preloadMu.Unlock()
}

func TestAModelAlreadyLoadedIsNotLoadedAgain(t *testing.T) {
	resetPreload()
	f := &fakeServer{loaded: []string{"small:4b"}, sizes: map[string]int64{"small:4b": 3 << 30}}
	PreloadForTyping(f.start(t), "small:4b")
	if f.loads != 0 {
		t.Errorf("a resident model was loaded again")
	}
}

func TestPreloadingIsNotRepeatedForEveryKeystroke(t *testing.T) {
	resetPreload()
	f := &fakeServer{sizes: map[string]int64{"tiny:1b": 1}}
	c := f.start(t)
	for i := 0; i < 5; i++ {
		PreloadForTyping(c, "tiny:1b")
	}
	// At most once, and not at all on a machine whose free memory cannot be
	// read and so is treated as having room.
	if f.loads > 1 {
		t.Errorf("preloaded %d times for one message", f.loads)
	}
}

func TestAModelThatIsNotInstalledIsNotPreloaded(t *testing.T) {
	resetPreload()
	f := &fakeServer{sizes: map[string]int64{}}
	PreloadForTyping(f.start(t), "missing:7b")
	if f.loads != 0 {
		t.Error("a model with no known size was preloaded")
	}
}
