package livetest

import (
	"strings"
	"testing"

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
