package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestEmbedOnCPUKeepsTheModelOffTheGPU(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 2}}})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	if _, err := c.Embed(context.Background(), "m", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	vecs, err := c.EmbedOnCPU(context.Background(), "m", []string{"x"})
	if err != nil || len(vecs) != 1 || len(vecs[0]) != 2 {
		t.Fatalf("vecs = %v, %v", vecs, err)
	}
	if len(bodies) != 2 {
		t.Fatalf("%d requests", len(bodies))
	}
	if _, ok := bodies[0]["options"]; ok {
		t.Errorf("the ordinary Embed now sends options: %v", bodies[0])
	}
	opts, _ := bodies[1]["options"].(map[string]any)
	if gpu, ok := opts["num_gpu"].(float64); !ok || gpu != 0 {
		t.Errorf("no num_gpu 0 in %v", bodies[1])
	}
	if ka, _ := bodies[1]["keep_alive"].(string); ka == "" {
		t.Errorf("no keep_alive in %v", bodies[1])
	}
	if bodies[1]["truncate"] != true || bodies[1]["model"] != "m" {
		t.Errorf("the shared fields went missing: %v", bodies[1])
	}
}
