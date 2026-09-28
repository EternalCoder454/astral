package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeOllama records what was asked of it and says which models are loaded.
type fakeOllama struct {
	mu       sync.Mutex
	loaded   []string
	requests []map[string]any
}

func (f *fakeOllama) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/api/ps":
			var out psResponse
			for _, n := range f.loaded {
				out.Models = append(out.Models, struct {
					Name      string `json:"name"`
					Model     string `json:"model"`
					Size      int64  `json:"size"`
					SizeVRAM  int64  `json:"size_vram"`
					ExpiresAt string `json:"expires_at"`
				}{Name: n, Size: 1, SizeVRAM: 1})
			}
			json.NewEncoder(w).Encode(out)
		case "/api/generate":
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			json.Unmarshal(b, &body)
			f.requests = append(f.requests, body)
			if ka, ok := body["keep_alive"].(float64); ok && ka == 0 {
				for i, n := range f.loaded {
					if n == body["model"] {
						f.loaded = append(f.loaded[:i], f.loaded[i+1:]...)
						break
					}
				}
			}
			w.Write([]byte(`{"done":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeOllama) unloads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		if ka, ok := r["keep_alive"].(float64); ok && ka == 0 {
			out = append(out, r["model"].(string))
		}
	}
	return out
}

func TestSwitchingModelsReleasesTheOldOne(t *testing.T) {
	f := &fakeOllama{loaded: []string{"big:27b"}}
	c := NewClient(f.server(t).URL)
	ctx := context.Background()

	c.UseForReplies(ctx, "big:27b")
	if got := f.unloads(); len(got) != 0 {
		t.Fatalf("the first model unloaded something: %v", got)
	}
	c.UseForReplies(ctx, "big:27b")
	if got := f.unloads(); len(got) != 0 {
		t.Fatalf("staying on one model unloaded it: %v", got)
	}
	c.UseForReplies(ctx, "other:35b")
	if got := f.unloads(); len(got) != 1 || got[0] != "big:27b" {
		t.Fatalf("switching did not release the old model: %v", got)
	}
}

func TestSwitchingDoesNotUnloadWhatIsNotLoaded(t *testing.T) {
	// The previous model already left memory on its own; there is nothing to
	// ask for, and the request would only cost a round trip before the reply.
	f := &fakeOllama{}
	c := NewClient(f.server(t).URL)
	ctx := context.Background()
	c.UseForReplies(ctx, "a:4b")
	c.UseForReplies(ctx, "b:4b")
	if got := f.unloads(); len(got) != 0 {
		t.Errorf("unloaded a model that was not loaded: %v", got)
	}
}

func TestLatestSuffixIsTheSameModel(t *testing.T) {
	f := &fakeOllama{loaded: []string{"qwen:latest"}}
	c := NewClient(f.server(t).URL)
	ctx := context.Background()
	c.UseForReplies(ctx, "qwen:latest")
	c.UseForReplies(ctx, "qwen")
	if got := f.unloads(); len(got) != 0 {
		t.Errorf("a model was unloaded for switching to itself: %v", got)
	}
}

func TestTwoClientsTrackSeparately(t *testing.T) {
	f := &fakeOllama{loaded: []string{"a:4b", "b:4b"}}
	url := f.server(t).URL
	one, two := NewClient(url), NewClient(url)
	ctx := context.Background()
	one.UseForReplies(ctx, "a:4b")
	two.UseForReplies(ctx, "b:4b")
	if got := f.unloads(); len(got) != 0 {
		t.Errorf("one client's model was released by another's first reply: %v", got)
	}
}

func TestPreloadSendsTheKeepAlive(t *testing.T) {
	f := &fakeOllama{}
	c := NewClient(f.server(t).URL)
	c.KeepAlive = "10m"
	if err := c.Preload(context.Background(), "a:4b"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 1 || f.requests[0]["keep_alive"] != "10m" || f.requests[0]["model"] != "a:4b" {
		t.Errorf("preload sent %v", f.requests)
	}
	// And with no keep-alive set, none is sent, so the server's own applies.
	c.KeepAlive = ""
	f.requests = nil
	f.mu.Unlock()
	c.Preload(context.Background(), "a:4b")
	f.mu.Lock()
	if _, sent := f.requests[0]["keep_alive"]; sent {
		t.Errorf("an empty keep-alive was sent as %v, overriding the server", f.requests[0]["keep_alive"])
	}
}
