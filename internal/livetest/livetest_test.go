package livetest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"astral/internal/gpu"
	"astral/internal/ollama"
)

var installed = []ollama.Model{
	{Name: "huihui_ai/qwen3.5-abliterated:4b", Size: 3300 << 20},
	{Name: "hf.co/llmfan46/Qwen3.6-35B-A3B-uncensored-heretic-GGUF:Q4_K_S", Size: 20900 << 20},
}

func TestNoModelIsChosenForYou(t *testing.T) {
	err := Check(nil, installed, "", false)
	if err == nil || !strings.Contains(err.Error(), "never choose") {
		t.Errorf("an unnamed model was allowed: %v", err)
	}
}

func TestALargeModelNeedsPermission(t *testing.T) {
	big := "hf.co/llmfan46/Qwen3.6-35B-A3B-uncensored-heretic-GGUF:Q4_K_S"
	if err := Check(nil, installed, big, false); err == nil {
		t.Error("the 21 GB model was allowed without ASTRAL_TEST_ALLOW_LARGE")
	}
}

func TestAModelThatIsNotInstalledIsRefused(t *testing.T) {
	if err := Check(nil, installed, "nothing:1b", false); err == nil {
		t.Error("a model that is not installed was allowed")
	}
}

// A large model allowed by name may load alone on the card, and never beside
// anything else.
func TestALargeModelMayLoadAlone(t *testing.T) {
	big := "hf.co/llmfan46/Qwen3.6-35B-A3B-uncensored-heretic-GGUF:Q4_K_S"
	card := func(used uint64) func() (gpu.Memory, bool) {
		return func() (gpu.Memory, bool) { return gpu.Memory{Total: 25753026560, Used: used}, true }
	}
	old := readVRAM
	defer func() { readVRAM = old }()

	// The real size of the file, and the card with the desktop on it.
	real := []ollama.Model{{Name: big, Size: 20859577355}}
	idle := fakeOllama(t, nil)
	readVRAM = card(2200 << 20)
	if err := Check(idle, real, big, true); err != nil {
		t.Errorf("alone on an idle card, it was refused: %v", err)
	}
	busy := fakeOllama(t, []string{"huihui_ai/qwen3.5-abliterated:4b"})
	if err := Check(busy, real, big, true); err == nil {
		t.Error("it was allowed beside another model")
	}
}

func fakeOllama(t *testing.T, loaded []string) *ollama.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var out []map[string]any
		for _, n := range loaded {
			out = append(out, map[string]any{"name": n, "size": 1, "size_vram": 1})
		}
		json.NewEncoder(w).Encode(map[string]any{"models": out})
	}))
	t.Cleanup(srv.Close)
	return ollama.NewClient(srv.URL)
}
