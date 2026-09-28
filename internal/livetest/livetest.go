// Package livetest decides which model a live test may load, and refuses when
// the answer is none.
//
// Live tests talk to a real Ollama server and load a real model. On a machine
// with one GPU that is also drawing the desktop, loading the wrong model is not
// a slow test: the AMD driver on Linux does not spill video memory into system
// RAM, so a model that does not fit leaves the compositor with nothing and the
// whole desktop freezes. That happened twice, and the second time it was a
// plain "go test" of one package, whose helper picked the first installed
// model when nobody named one. The first installed model was 21 GB.
//
// So every live test goes through Model, and Model is strict:
//
//   - It never picks a model. ASTRAL_TEST_MODEL has to name one, or the test
//     is skipped. A test that runs because nobody said not to is how the
//     second freeze happened.
//   - It refuses anything larger than SmallModel unless ASTRAL_TEST_ALLOW_LARGE
//     is set, because a large model that fits when the card is empty does not
//     fit once something else is on it.
//   - It refuses anything that would not fit in the video memory free right
//     now, with the desktop's reserve kept back.
//
// Skipping, not failing: a test that could not safely run has not found a bug.
package livetest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/gpu"
	"astral/internal/ollama"
)

// SmallModel is the largest model, on disk, a live test loads without being
// told it may. Eight gigabytes admits the 4B and 8B models the tests are
// written for, and nothing that could crowd a 24 GB card by itself.
const SmallModel = 8 << 30

// headroom is what a model costs beyond its weights once loaded: the context
// cache and the runner's buffers.
const headroom = 1.15

// Client returns a client for the configured server, or skips when there is
// none. It also skips in -short mode, so "go test -short" never reaches a
// model at all.
func Client(t testing.TB) (*ollama.Client, []ollama.Model) {
	t.Helper()
	if testing.Short() {
		t.Skip("live test skipped in -short mode")
	}
	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	installed, err := client.Probe(ctx)
	if err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}
	if len(installed) == 0 {
		t.Skip("Ollama is running but has no models installed")
	}
	return client, installed
}

// Model returns the model named by ASTRAL_TEST_MODEL when it is safe to load,
// and skips the test otherwise. See the package comment.
func Model(t testing.TB, client *ollama.Client, installed []ollama.Model) string {
	t.Helper()
	model := strings.TrimSpace(os.Getenv("ASTRAL_TEST_MODEL"))
	if err := Check(client, installed, model, os.Getenv("ASTRAL_TEST_ALLOW_LARGE") != ""); err != nil {
		t.Skip(err.Error())
	}
	return model
}

// Check is Model's decision, without the test around it, so the rules can be
// tested themselves and so a harness that is not a test can ask the same
// question.
func Check(client *ollama.Client, installed []ollama.Model, model string, allowLarge bool) error {
	if model == "" {
		return fmt.Errorf("live tests need ASTRAL_TEST_MODEL; they never choose a model themselves")
	}
	size := int64(-1)
	for _, m := range installed {
		if strings.TrimSuffix(m.Name, ":latest") == strings.TrimSuffix(model, ":latest") {
			size = m.Size
			break
		}
	}
	if size < 0 {
		return fmt.Errorf("%s is not installed", model)
	}
	if size > SmallModel && !allowLarge {
		return fmt.Errorf("%s is %.1f GB, over the %d GB a live test loads without ASTRAL_TEST_ALLOW_LARGE",
			model, float64(size)/(1<<30), SmallModel>>30)
	}
	// Already resident: running against it costs no more memory.
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		loaded, err := client.Running(ctx)
		cancel()
		if err == nil {
			if _, ok := ollama.FindLoaded(loaded, model); ok {
				return nil
			}
		}
	}
	if !gpu.Fits(uint64(float64(size) * headroom)) {
		return fmt.Errorf("%s (%.1f GB) does not fit in free video memory beside what is already "+
			"loaded, with room kept for the desktop; not loading it", model, float64(size)/(1<<30))
	}
	return nil
}
