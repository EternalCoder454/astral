package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// contextLengths remembers each model's longest context, which does not change
// while it is installed and costs a request to ask.
var contextLengths sync.Map

// ContextLength is the longest context a model was trained for, from what
// Ollama reports about it: "<architecture>.context_length" in its model info.
func (c *Client) ContextLength(ctx context.Context, model string) (int, error) {
	if n, ok := contextLengths.Load(c.BaseURL + "|" + model); ok {
		return n.(int), nil
	}
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ollama returned %s", resp.Status)
	}
	var out struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	for k, v := range out.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			if f, ok := v.(float64); ok && f > 0 {
				n := int(f)
				contextLengths.Store(c.BaseURL+"|"+model, n)
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("%s does not say how long its context is", model)
}
