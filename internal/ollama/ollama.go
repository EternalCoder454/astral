// Package ollama is a thin client for a local Ollama server.
//
// It speaks /api/chat rather than /api/generate: a roleplay session is an
// unbounded multi-turn conversation, and only the chat endpoint takes a real
// messages array. Sending a transcript flattened into one prompt string works
// until the model has to tell whose turn is whose, which in roleplay is the
// whole job.
//
// Every call is blocking and bounded by the caller's context. The UI layer is
// responsible for running these on a goroutine and marshalling results back to
// the GTK main thread.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is where Ollama listens unless told otherwise.
	DefaultBaseURL = "http://localhost:11434"

	// requestTimeout bounds an entire streaming request including reading the
	// body, so it is generous: a long roleplay reply from a large model on a
	// modest machine is legitimately slow. Callers bound individual calls with
	// their own context, which is what a Stop button cancels.
	requestTimeout = 10 * time.Minute

	// probeTimeout bounds the readiness check. It is short because its whole
	// purpose is to answer "is anything there?" without making the UI wait.
	probeTimeout = 4 * time.Second
)

// Roles in a chat transcript.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn in a conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Images are base64-encoded, one per attachment, and only meaningful on a
	// user turn sent to a model with the vision capability. Ollama takes the
	// raw base64 without a data: prefix.
	Images []string `json:"images,omitempty"`
	// Thinking carries a reasoning model's scratchpad. It is sent back on
	// subsequent turns only when the model asked for it; for most models it is
	// empty and omitted.
	Thinking string `json:"thinking,omitempty"`
}

// Options are the sampling knobs Astral exposes. Zero values mean "leave it to
// the model", so an empty Options sends nothing and inherits the model's own
// defaults — which is what you want for a model you have never tuned.
type Options struct {
	Temperature   float64
	TopP          float64
	TopK          int
	RepeatPenalty float64
	// RepeatLastN is how many recent tokens the repetition penalty considers.
	//
	// Ollama's default is 64, which is the length of a sentence. A model that
	// collapses into a cycle repeats a block far longer than that, so by the
	// time the cycle comes round again its first copy has already left the
	// window and the penalty never sees it. The collapse that prompted this
	// ran about 225 tokens per cycle.
	RepeatLastN int
	NumCtx      int
	NumPredict  int
	Seed        int
	Stop        []string
}

// toMap renders only the options that were actually set. Ollama treats an
// explicit zero as a real value (temperature 0 is greedy decoding), so an
// unset field must be absent from the JSON rather than present as 0.
func (o Options) toMap() map[string]any {
	m := map[string]any{}
	if o.Temperature > 0 {
		m["temperature"] = o.Temperature
	}
	if o.TopP > 0 {
		m["top_p"] = o.TopP
	}
	if o.TopK > 0 {
		m["top_k"] = o.TopK
	}
	if o.RepeatPenalty > 0 {
		m["repeat_penalty"] = o.RepeatPenalty
	}
	if o.RepeatLastN > 0 {
		m["repeat_last_n"] = o.RepeatLastN
	}
	if o.NumCtx > 0 {
		m["num_ctx"] = o.NumCtx
	}
	if o.NumPredict > 0 {
		m["num_predict"] = o.NumPredict
	}
	if o.Seed != 0 {
		m["seed"] = o.Seed
	}
	if len(o.Stop) > 0 {
		m["stop"] = o.Stop
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// DefaultKeepAlive is how long Ollama is asked to hold the model in memory
// after a reply.
//
// This is the single biggest thing separating a fast session from a slow one.
// Ollama's own default is five minutes, and roleplay has long gaps — reading
// the reply, deciding what to do — so the model is routinely evicted between
// turns and the next message pays a full reload. On a 27B model that is tens
// of seconds of staring at nothing before the first token.
const DefaultKeepAlive = "30m"

// Client talks to a local Ollama server.
type Client struct {
	BaseURL string
	// KeepAlive is sent with every request; empty means Ollama's own default.
	KeepAlive string
	http      *http.Client
}

// NewClient returns a Client for the given base URL (empty means the default).
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		KeepAlive: DefaultKeepAlive,
		http:      &http.Client{Timeout: requestTimeout},
	}
}

// Stats summarizes a generation's throughput, for display in the UI.
type Stats struct {
	Tokens       int           // tokens generated
	PromptTokens int           // tokens in the prompt
	Elapsed      time.Duration // total wall-clock time
	TokPerSec    float64       // generation speed
	// PromptTokPerSec is how fast the prompt itself was read. It is the number
	// behind the wait before the first word appears, and it is the one the
	// prompt's shape can actually move: a cached prefix is not re-read, so the
	// tokens counted here are only the ones that changed since last turn.
	PromptTokPerSec float64
	PromptElapsed   time.Duration
	// DoneReason is why generation stopped. "length" means the reply hit the
	// token limit rather than finishing, which is worth telling the user:
	// otherwise a reply that stops mid-sentence looks like the model's fault.
	DoneReason string
}

// Truncated reports whether the reply was cut off at the token limit.
func (s Stats) Truncated() bool { return s.DoneReason == "length" }

// Summary renders the stats like "141.7 tok/s · 363 tokens", or "" when the
// server reported no token counts.
func (s Stats) Summary() string {
	if s.Tokens == 0 {
		return ""
	}
	return fmt.Sprintf("%.1f tok/s · %d tokens", s.TokPerSec, s.Tokens)
}

// Delta is one streamed increment: content, reasoning, or both.
type Delta struct {
	Content  string
	Thinking string
}

type chatRequest struct {
	Model     string          `json:"model"`
	Messages  []Message       `json:"messages"`
	Stream    bool            `json:"stream"`
	Think     *bool           `json:"think,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	Options   map[string]any  `json:"options,omitempty"`
}

type chatResponse struct {
	Message struct {
		Role     string `json:"role"`
		Content  string `json:"content"`
		Thinking string `json:"thinking"`
	} `json:"message"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	Error              string `json:"error,omitempty"`
	EvalCount          int    `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
	PromptEvalCount    int    `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	TotalDuration      int64  `json:"total_duration"`
}

func statsFrom(cr chatResponse) Stats {
	st := Stats{
		Tokens:       cr.EvalCount,
		PromptTokens: cr.PromptEvalCount,
		Elapsed:      time.Duration(cr.TotalDuration),
		DoneReason:   cr.DoneReason,
	}
	if cr.EvalDuration > 0 {
		st.TokPerSec = float64(cr.EvalCount) / (float64(cr.EvalDuration) / 1e9)
	}
	st.PromptElapsed = time.Duration(cr.PromptEvalDuration)
	if cr.PromptEvalDuration > 0 {
		st.PromptTokPerSec = float64(cr.PromptEvalCount) / (float64(cr.PromptEvalDuration) / 1e9)
	}
	return st
}

// Chat streams a multi-turn completion. onDelta (if non-nil) is invoked on this
// goroutine with each chunk as it arrives — the caller is responsible for
// getting those onto the UI thread. It returns the assembled reply and the
// throughput stats from the final chunk.
//
// think must be passed explicitly: left unspecified, a reasoning model thinks
// by default and, under a token limit, can return a completed thought and an
// empty reply. A server that rejects the parameter gets one retry without it.
func (c *Client) Chat(ctx context.Context, model string, msgs []Message, opts Options, think *bool, onDelta func(Delta)) (Message, Stats, error) {
	if model == "" {
		return Message{}, Stats{}, fmt.Errorf("no model selected")
	}
	if len(msgs) == 0 {
		return Message{}, Stats{}, fmt.Errorf("no messages to send")
	}
	msg, stats, err := c.chat(ctx, model, msgs, opts, think, nil, onDelta)
	if err != nil && think != nil && rejectsThinking(err) && ctx.Err() == nil {
		return c.chat(ctx, model, msgs, opts, nil, nil, onDelta)
	}
	return msg, stats, err
}

// Structured asks the model to answer as JSON matching schema, and returns the
// raw JSON. Ollama constrains decoding to the schema, so the result parses —
// which is the difference between this and asking nicely in a prompt and
// hoping. Not streamed: there is nothing useful to show of a half-built object.
//
// Reasoning is turned off for the same reason it is elsewhere: a model that
// deliberates before emitting constrained JSON spends its budget on text that
// is thrown away.
func (c *Client) Structured(ctx context.Context, model string, msgs []Message, opts Options, schema json.RawMessage) ([]byte, Stats, error) {
	noThink := false
	msg, stats, err := c.chat(ctx, model, msgs, opts, &noThink, schema, nil)
	if err != nil && rejectsThinking(err) && ctx.Err() == nil {
		msg, stats, err = c.chat(ctx, model, msgs, opts, nil, schema, nil)
	}
	if err != nil {
		return nil, stats, err
	}
	out := strings.TrimSpace(msg.Content)
	if out == "" {
		return nil, stats, fmt.Errorf("the model returned nothing")
	}
	return []byte(out), stats, nil
}

// ErrUnreachable means the model server did not answer at all, as opposed to
// answering with a refusal. Callers tell the two apart with errors.Is: the
// first is worth telling someone how to fix, and matching the message text
// instead breaks the moment the wording changes.
var ErrUnreachable = errors.New("cannot reach the model server")

// rejectsThinking reports whether an error is the server refusing the think
// parameter, rather than a failure worth surfacing.
func rejectsThinking(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "think") || strings.Contains(s, "thinking")
}

func (c *Client) chat(ctx context.Context, model string, msgs []Message, opts Options, think *bool, format json.RawMessage, onDelta func(Delta)) (Message, Stats, error) {
	// A structured request is not streamed: Ollama then returns one JSON
	// object rather than NDJSON, which the reader below handles either way
	// because a single object is just a one-line stream that is already done.
	body, err := json.Marshal(chatRequest{
		Model:     model,
		Messages:  msgs,
		Stream:    len(format) == 0,
		Think:     think,
		Format:    format,
		KeepAlive: c.KeepAlive,
		Options:   opts.toMap(),
	})
	if err != nil {
		return Message{}, Stats{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Message{}, Stats{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Message{}, Stats{}, fmt.Errorf("cannot reach Ollama at %s: %w: %w", c.BaseURL, ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Message{}, Stats{}, fmt.Errorf("ollama returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	sc := bufio.NewScanner(resp.Body)
	// A single NDJSON line holds one delta, but a model that emits a long
	// stretch without a newline (or a thinking block flushed at once) can make
	// one arbitrarily large, so the scanner gets room well past the default 64K.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var content, thinking strings.Builder
	var stats Stats
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var cr chatResponse
		if err := json.Unmarshal(line, &cr); err != nil {
			continue // a partial or non-JSON line: the next one usually parses
		}
		if cr.Error != "" {
			return Message{}, Stats{}, fmt.Errorf("ollama: %s", cr.Error)
		}
		d := Delta{Content: cr.Message.Content, Thinking: cr.Message.Thinking}
		if d.Content != "" {
			content.WriteString(d.Content)
		}
		if d.Thinking != "" {
			thinking.WriteString(d.Thinking)
		}
		if onDelta != nil && (d.Content != "" || d.Thinking != "") {
			onDelta(d)
		}
		if cr.Done {
			stats = statsFrom(cr)
			break
		}
	}
	if err := sc.Err(); err != nil {
		// A cancelled context surfaces here as a read error. Report the
		// cancellation itself, so the caller can tell "you pressed Stop" from
		// "the connection broke".
		if ctx.Err() != nil {
			return Message{Role: RoleAssistant, Content: content.String(), Thinking: thinking.String()}, stats, ctx.Err()
		}
		return Message{Role: RoleAssistant, Content: content.String(), Thinking: thinking.String()}, stats, err
	}
	msg := Message{
		Role:     RoleAssistant,
		Content:  strings.TrimSpace(content.String()),
		Thinking: strings.TrimSpace(thinking.String()),
	}
	return msg, stats, nil
}

type tagsResponse struct {
	Models []struct {
		Name       string `json:"name"`
		Model      string `json:"model"`
		Size       int64  `json:"size"`
		ModifiedAt string `json:"modified_at"`
		Details    struct {
			ParameterSize     string `json:"parameter_size"`
			QuantizationLevel string `json:"quantization_level"`
			Family            string `json:"family"`
		} `json:"details"`
	} `json:"models"`
}

// Model is an installed model as the picker shows it.
type Model struct {
	Name     string
	Size     int64
	Params   string
	Quant    string
	Family   string
	Modified time.Time
}

// Label renders the model for a dropdown: "qwen3:8b · 8.2B · Q4_K_M".
func (m Model) Label() string {
	parts := []string{m.Name}
	if m.Params != "" {
		parts = append(parts, m.Params)
	}
	if m.Quant != "" {
		parts = append(parts, m.Quant)
	}
	return strings.Join(parts, " · ")
}

// Models returns the models installed on the server. A nil error means the
// server is reachable — the slice may still be empty if nothing is pulled,
// which lets the UI tell "Ollama down" from "no models installed".
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Ollama at %s: %w: %w", c.BaseURL, ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %s", resp.Status)
	}
	var tr tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(tr.Models))
	for _, m := range tr.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == "" {
			continue
		}
		mod, _ := time.Parse(time.RFC3339, m.ModifiedAt)
		out = append(out, Model{
			Name:     name,
			Size:     m.Size,
			Params:   m.Details.ParameterSize,
			Quant:    m.Details.QuantizationLevel,
			Family:   m.Details.Family,
			Modified: mod,
		})
	}
	return out, nil
}

type showResponse struct {
	Capabilities []string `json:"capabilities"`
	Details      struct {
		Family   string   `json:"family"`
		Families []string `json:"families"`
	} `json:"details"`
}

// CanSee reports whether a model accepts images.
//
// Asking beforehand is worth a round trip: a text-only model handed an image
// either ignores it silently, which looks like the feature is broken, or
// rejects the whole request, which loses the message with it.
func (c *Client) CanSee(ctx context.Context, model string) (bool, error) {
	if model == "" {
		return false, fmt.Errorf("no model selected")
	}
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("cannot reach Ollama at %s: %w: %w", c.BaseURL, ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("ollama returned %s", resp.Status)
	}
	var sr showResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return false, err
	}
	for _, cap := range sr.Capabilities {
		if strings.EqualFold(cap, "vision") {
			return true, nil
		}
	}
	// Older Ollama builds predate the capabilities list and only name the
	// families. A vision model carries a projector family alongside its own.
	for _, f := range append(sr.Details.Families, sr.Details.Family) {
		switch strings.ToLower(f) {
		case "clip", "mllama", "qwen2vl", "gemma3", "llava":
			return true, nil
		}
	}
	return false, nil
}

// Probe reports whether the server is reachable and which models it has. It is
// the readiness check the UI polls with, so it uses its own short timeout
// rather than the caller's.
func (c *Client) Probe(ctx context.Context) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return c.Models(ctx)
}

// HasModel reports whether name is among models, tolerating the ":latest"
// suffix that Ollama adds to a bare tag but users rarely type.
func HasModel(models []Model, name string) bool {
	if name == "" {
		return false
	}
	want := strings.TrimSuffix(name, ":latest")
	for _, m := range models {
		if strings.TrimSuffix(m.Name, ":latest") == want {
			return true
		}
	}
	return false
}

type psResponse struct {
	Models []struct {
		Name      string `json:"name"`
		Model     string `json:"model"`
		Size      int64  `json:"size"`
		SizeVRAM  int64  `json:"size_vram"`
		ExpiresAt string `json:"expires_at"`
	} `json:"models"`
}

// Loaded is a model currently held in memory by the server.
type Loaded struct {
	Name string
	// Size is what the model occupies in total, SizeVRAM how much of that is
	// on the GPU. They are equal for a model that fits.
	Size     int64
	SizeVRAM int64
}

// OnGPU reports the share of the model that is on the GPU, 0 to 1.
func (l Loaded) OnGPU() float64 {
	if l.Size <= 0 {
		return 0
	}
	return float64(l.SizeVRAM) / float64(l.Size)
}

// Spilled reports whether a meaningful part of the model was pushed out of
// video memory onto the CPU.
//
// This is what turns a second, smaller model from a saving into a cost. Ollama
// keeps both resident rather than swapping, which is the point of a separate
// housekeeping model, but only while both fit: measured on a 24GB card, a 27B
// at 32k pushed a 4B to 18% CPU. The threshold is generous because a few
// percent costs little and the reported numbers are approximate.
func (l Loaded) Spilled() bool { return l.OnGPU() < 0.95 }

// Running returns the models the server currently holds in memory.
func (c *Client) Running(ctx context.Context) ([]Loaded, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Ollama at %s: %w: %w", c.BaseURL, ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %s", resp.Status)
	}
	var pr psResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	out := make([]Loaded, 0, len(pr.Models))
	for _, m := range pr.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == "" {
			continue
		}
		out = append(out, Loaded{Name: name, Size: m.Size, SizeVRAM: m.SizeVRAM})
	}
	return out, nil
}

// FindLoaded returns the named model among those loaded, tolerating the
// ":latest" suffix the way HasModel does.
func FindLoaded(loaded []Loaded, name string) (Loaded, bool) {
	want := strings.TrimSuffix(name, ":latest")
	for _, l := range loaded {
		if strings.TrimSuffix(l.Name, ":latest") == want {
			return l, true
		}
	}
	return Loaded{}, false
}
