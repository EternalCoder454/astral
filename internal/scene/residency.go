package scene

import (
	"context"
	"log"
	"strings"

	"astral/internal/gpu"
	"astral/internal/ollama"
)

// contextHeadroom is the share added to a model's size on disk to cover what
// loading it costs beyond its weights: the context cache and the runner's own
// buffers. Measured on the 4B at 8k context it came to about a tenth; fifteen
// per cent leaves room for a longer window without pretending to precision.
const contextHeadroom = 1.15

// FitHousekeeping returns the model housekeeping should use: the one chosen for
// it when that will fit on the card beside what is already there, and otherwise
// the model the scene is played with, which is loaded already and costs nothing
// more.
//
// A separate housekeeping model exists so recaps and lore do not queue behind
// the scene, and that only works while both fit. On a card where they do not,
// loading the second one either pushes most of it onto the CPU, which makes it
// slower than simply using the scene's model, or, on Linux with an AMD card,
// leaves the desktop no memory at all and crashes it.
//
// Run it off the UI thread: it asks the server what is loaded.
func FitHousekeeping(ctx context.Context, client *ollama.Client, chosen, sceneModel string) string {
	chosen = strings.TrimSpace(chosen)
	if chosen == "" || strings.TrimSuffix(chosen, ":latest") == strings.TrimSuffix(sceneModel, ":latest") {
		return sceneModel
	}
	if client == nil {
		return chosen
	}
	// Already resident: using it costs no memory at all.
	if loaded, err := client.Running(ctx); err == nil {
		if _, ok := ollama.FindLoaded(loaded, chosen); ok {
			return chosen
		}
	}
	size := int64(0)
	if models, err := client.Models(ctx); err == nil {
		for _, m := range models {
			if strings.TrimSuffix(m.Name, ":latest") == strings.TrimSuffix(chosen, ":latest") {
				size = m.Size
				break
			}
		}
	}
	if size <= 0 {
		// Not installed, or the server did not say. Nothing to measure, so the
		// choice stands and the server reports whatever goes wrong.
		return chosen
	}
	if gpu.Fits(uint64(float64(size) * contextHeadroom)) {
		return chosen
	}
	if sceneModel == "" {
		return chosen
	}
	log.Printf("astral: %s would not fit beside what is loaded; housekeeping uses %s instead", chosen, sceneModel)
	return sceneModel
}
