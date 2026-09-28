package scene

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"astral/internal/gpu"
	"astral/internal/ollama"
)

// The models on the machine this was written for, at their real sizes, and
// its card: 25.75 GB, with about 2.8 GB in use by the desktop.
var (
	goetia   = "hf.co/mradermacher/Goetia-26B-A4B-v1.3-Absolute-Heretic-ARA-GGUF:Q4_K_M"
	small    = "huihui_ai/qwen3.5-abliterated:4b"
	dense    = "huihui_ai/qwen3.6-abliterated:27b"
	moe      = "hf.co/llmfan46/Qwen3.6-35B-A3B-uncensored-heretic-GGUF:Q4_K_S"
	cardSize = uint64(25753026560)
	desktop  = uint64(2840000000)
)

// seeingServer is an Ollama that knows which models can see, keeps track of
// what is loaded, and records the most that were ever loaded at once.
type seeingServer struct {
	mu       sync.Mutex
	sizes    map[string]int64
	sees     map[string]bool
	loaded   map[string]bool
	peak     int
	chats    []string // the model each chat went to
	images   []int    // how many images each chat carried
	unloaded []string
	reply    string
}

func newSeeingServer() *seeingServer {
	return &seeingServer{
		sizes: map[string]int64{
			goetia: 16796020574, small: 3315333475, dense: 17420432805, moe: 20859577355,
		},
		sees:   map[string]bool{small: true, dense: true, moe: true},
		loaded: map[string]bool{},
		reply:  "A woman in her late twenties with a shaved undercut, dyed copper.",
	}
}

func (s *seeingServer) start(t *testing.T) *ollama.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var body struct {
			Model     string `json:"model"`
			KeepAlive any    `json:"keep_alive"`
			Messages  []struct {
				Images []string `json:"images"`
			} `json:"messages"`
		}
		if r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(&body)
		}
		switch r.URL.Path {
		case "/api/show":
			caps := []string{"completion"}
			if s.sees[body.Model] {
				caps = append(caps, "vision")
			}
			json.NewEncoder(w).Encode(map[string]any{"capabilities": caps})
		case "/api/tags":
			var out []map[string]any
			for n, size := range s.sizes {
				out = append(out, map[string]any{"name": n, "size": size})
			}
			json.NewEncoder(w).Encode(map[string]any{"models": out})
		case "/api/ps":
			var out []map[string]any
			for n := range s.loaded {
				out = append(out, map[string]any{"name": n, "size": s.sizes[n], "size_vram": s.sizes[n]})
			}
			json.NewEncoder(w).Encode(map[string]any{"models": out})
		case "/api/generate":
			if ka, ok := body.KeepAlive.(float64); ok && ka == 0 {
				delete(s.loaded, body.Model)
				s.unloaded = append(s.unloaded, body.Model)
			}
			w.Write([]byte(`{"done":true}`))
		case "/api/chat":
			s.loaded[body.Model] = true
			s.peak = max(s.peak, len(s.loaded))
			s.chats = append(s.chats, body.Model)
			n := 0
			for _, m := range body.Messages {
				n += len(m.Images)
			}
			s.images = append(s.images, n)
			json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]any{"role": "assistant", "content": s.reply},
				"done":    true,
			})
		}
	}))
	t.Cleanup(srv.Close)
	return ollama.NewClient(srv.URL)
}

// card is the fake card: the desktop's share in use, plus whatever the fake
// server has loaded.
func (s *seeingServer) card() func() (gpu.Memory, bool) {
	return func() (gpu.Memory, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		used := desktop
		for n := range s.loaded {
			used += uint64(s.sizes[n])
		}
		return gpu.Memory{Total: cardSize, Used: used}, true
	}
}

func withCard(t *testing.T, read func() (gpu.Memory, bool)) {
	t.Helper()
	old := readVRAM
	readVRAM = read
	t.Cleanup(func() { readVRAM = old })
}

// A chat whose own model can see is handed the picture as it is.
func TestTheChatsOwnModelLooksWhenItCan(t *testing.T) {
	s := newSeeingServer()
	client := s.start(t)
	withCard(t, s.card())
	seer, direct, err := Seer(context.Background(), client, "", dense)
	if err != nil || !direct || seer != dense {
		t.Errorf("got %q direct=%v err=%v, want the chat's own model", seer, direct, err)
	}
}

// A chat on a model that cannot see gets the largest model that can and fits
// on the card by itself. On this card that is the 4B: the 27B at this context
// needs about 20 GB, and with the desktop's share and the reserve kept back
// there are about 19.7.
func TestAModelThatCannotSeeGetsOneThatFits(t *testing.T) {
	s := newSeeingServer()
	s.loaded[goetia] = true
	client := s.start(t)
	withCard(t, s.card())
	seer, direct, err := Seer(context.Background(), client, "", goetia)
	if err != nil || direct {
		t.Fatalf("direct=%v err=%v", direct, err)
	}
	if seer != small {
		t.Errorf("chose %s, want %s", seer, small)
	}

	// A somewhat bigger card takes the 27B, and still not the 35B.
	withCard(t, func() (gpu.Memory, bool) {
		return gpu.Memory{Total: 28_000_000_000, Used: desktop + uint64(s.sizes[goetia])}, true
	})
	if seer, _, _ := Seer(context.Background(), client, "", goetia); seer != dense {
		t.Errorf("on a 28 GB card chose %s, want %s", seer, dense)
	}

	// With nothing to go on, the smallest.
	withCard(t, func() (gpu.Memory, bool) { return gpu.Memory{}, false })
	if seer, _, _ := Seer(context.Background(), client, "", goetia); seer != small {
		t.Errorf("on an unknown card chose %s, want %s", seer, small)
	}
}

// A model chosen in Settings reads every picture, even for a chat whose model
// could see, and a choice that cannot see falls back rather than failing.
func TestTheChosenImageModel(t *testing.T) {
	s := newSeeingServer()
	client := s.start(t)
	withCard(t, s.card())
	if seer, direct, _ := Seer(context.Background(), client, dense, small); seer != dense || direct {
		t.Errorf("chose %s direct=%v, want the chosen %s", seer, direct, dense)
	}
	if seer, direct, _ := Seer(context.Background(), client, goetia, small); seer != small || !direct {
		t.Errorf("a chosen model that cannot see was used: %s direct=%v", seer, direct)
	}
}

func TestNobodyCanSee(t *testing.T) {
	s := newSeeingServer()
	s.sees = map[string]bool{}
	client := s.start(t)
	withCard(t, s.card())
	if _, _, err := Seer(context.Background(), client, "", goetia); err != ErrNobodyCanSee {
		t.Errorf("got %v, want ErrNobodyCanSee", err)
	}
	if CanTakePictures(context.Background(), client, "", goetia) {
		t.Error("pictures were offered with nothing to look at them")
	}
}

// The point of the whole arrangement: the model being talked to is set aside
// while the other looks, the one that looked is released straight after, and
// at no moment are two large models on the card together.
func TestDescribingNeverStacksModels(t *testing.T) {
	s := newSeeingServer()
	s.loaded[goetia] = true
	client := s.start(t)
	withCard(t, s.card())

	got, err := Describe(context.Background(), client, dense, goetia, "aGVsbG8=", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "copper") {
		t.Errorf("description %q", got)
	}
	if s.peak != 1 {
		t.Errorf("%d models were loaded at once", s.peak)
	}
	if len(s.loaded) != 0 {
		t.Errorf("left loaded afterwards: %v", s.loaded)
	}
	if len(s.chats) != 1 || s.chats[0] != dense || s.images[0] != 1 {
		t.Errorf("chats %v with images %v", s.chats, s.images)
	}
}

// When the looking model fits beside the chat's, nothing is moved: unloading
// a model that did not need to go costs the next reply a reload.
func TestDescribingMovesNothingThatFits(t *testing.T) {
	s := newSeeingServer()
	s.loaded[goetia] = true
	client := s.start(t)
	withCard(t, func() (gpu.Memory, bool) {
		return gpu.Memory{Total: 48 << 30, Used: desktop + uint64(s.sizes[goetia])}, true
	})
	if _, err := Describe(context.Background(), client, small, goetia, "aGVsbG8=", "what is she holding?", false); err != nil {
		t.Fatal(err)
	}
	for _, u := range s.unloaded {
		if u == goetia {
			t.Error("the chat's model was unloaded though there was room")
		}
	}
	if s.loaded[small] {
		t.Error("the model that looked was left loaded")
	}
}
