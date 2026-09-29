package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Which models are held in video memory, and for how long.
//
// Ollama decides what stays loaded, but Astral decides what gets asked for, and
// on Linux asking for the wrong thing takes the desktop down: the AMD driver
// does not spill into system RAM, so a second large model loaded beside the
// first leaves the compositor no memory for its next frame and every window
// crashes at once. Switching the model in the picker used to leave the old one
// resident for the whole keep-alive, which is the pattern those crashes had.

// generateRequest is the smallest /api/generate body: a model and how long to
// keep it. With no prompt Ollama only loads or unloads; nothing is generated.
type generateRequest struct {
	Model     string `json:"model"`
	KeepAlive any    `json:"keep_alive,omitempty"`
	Stream    bool   `json:"stream"`
}

// Unload asks the server to release a model now rather than when its
// keep-alive runs out.
func (c *Client) Unload(ctx context.Context, model string) error {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	// Zero as a number: the string "0" is also accepted, but a number is what
	// the documentation shows and what every version understands.
	return c.generateEmpty(ctx, generateRequest{Model: model, KeepAlive: 0})
}

// Preload loads a model without generating anything, so the load happens while
// the person is still typing rather than after they press send.
func (c *Client) Preload(ctx context.Context, model string) error {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	req := generateRequest{Model: model}
	if c.KeepAlive != "" {
		req.KeepAlive = c.KeepAlive
	}
	return c.generateEmpty(ctx, req)
}

func (c *Client) generateEmpty(ctx context.Context, body generateRequest) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/generate", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Ollama at %s: %w: %w", c.BaseURL, ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("ollama returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// sceneModels remembers the model the last scene reply was written with, per
// client. Kept outside Client so the struct stays a plain value the rest of the
// code already copies and compares.
var sceneModels sync.Map // *Client -> *sceneModel

type sceneModel struct {
	mu   sync.Mutex
	name string
}

// UseForReplies records that replies are now written with model, and releases
// the one used before it when that was different.
//
// Synchronous on purpose, and called on the goroutine that is about to send the
// request. Released in the background, the old model would still be resident
// while the new one loaded, which is the exact peak this exists to avoid. The
// unload is a single small request and costs a fraction of a second.
//
// Housekeeping does not go through here: a separate housekeeping model is
// chosen only when it fits beside the scene's (see scene.FitHousekeeping), so
// it has no reason to evict anything.
func (c *Client) UseForReplies(ctx context.Context, model string) {
	if c == nil || strings.TrimSpace(model) == "" {
		return
	}
	v, _ := sceneModels.LoadOrStore(c, &sceneModel{})
	sm := v.(*sceneModel)
	sm.mu.Lock()
	previous := sm.name
	sm.name = model
	sm.mu.Unlock()
	if previous == "" || sameModel(previous, model) {
		return
	}
	// Only when it is actually resident: asking to unload a model that is not
	// loaded is harmless, but asking the server anything costs a round trip
	// and this runs before every reply.
	if loaded, err := c.Running(ctx); err == nil {
		if _, ok := FindLoaded(loaded, previous); !ok {
			return
		}
	}
	_ = c.Unload(ctx, previous)
}

func sameModel(a, b string) bool {
	return strings.TrimSuffix(a, ":latest") == strings.TrimSuffix(b, ":latest")
}

// Embed returns one vector per input, from an embedding model.
func (c *Client) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	return c.embed(ctx, model, inputs, nil)
}

// cpuEmbedKeepAlive is how long an embedding model made on the CPU stays in
// memory. Short, because it is no use to anything else, but long enough that
// the next turn of a scene being played finds it loaded: a cold load takes
// longer than the scene's memory is willing to wait for it.
const cpuEmbedKeepAlive = "2m"

// EmbedOnCPU is Embed with the model kept out of video memory.
//
// Astral keeps one model in video memory at a time, the scene's, and an
// embedding model loaded beside it would take room the scene needs. Asking for
// no layers on the GPU keeps this one in ordinary memory, where a small
// embedding model is quick enough for the few messages a turn adds.
func (c *Client) EmbedOnCPU(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	return c.embed(ctx, model, inputs, map[string]any{
		"options":    map[string]any{"num_gpu": 0},
		"keep_alive": cpuEmbedKeepAlive,
	})
}

// embed is the request both share; extra is added to the body as it is.
func (c *Client) embed(ctx context.Context, model string, inputs []string, extra map[string]any) ([][]float32, error) {
	fields := map[string]any{"model": model, "input": inputs, "truncate": true}
	for k, v := range extra {
		fields[k] = v
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Ollama at %s: %w: %w", c.BaseURL, ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("ollama returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("asked for %d embeddings and got %d", len(inputs), len(out.Embeddings))
	}
	return out.Embeddings, nil
}

// EmbeddingModels returns the installed models that make embeddings rather
// than text, found by what Ollama says each can do.
func (c *Client) EmbeddingModels(ctx context.Context) ([]string, error) {
	models, err := c.Models(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range models {
		caps, err := c.capabilities(ctx, m.Name)
		if err != nil {
			continue
		}
		for _, cp := range caps {
			if cp == "embedding" {
				out = append(out, m.Name)
				break
			}
		}
	}
	return out, nil
}

// capabilities asks /api/show what a model can do.
func (c *Client) capabilities(ctx context.Context, model string) ([]string, error) {
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %s", resp.Status)
	}
	var out struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Capabilities, nil
}
