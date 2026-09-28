package scene

import (
	"context"
	"strings"
	"testing"

	"astral/internal/ollama"
	"astral/internal/store"
)

func TestFitContextGrowsForALongFile(t *testing.T) {
	opts := ollama.Options{NumCtx: 8192, NumPredict: 1024}
	short := []ollama.Message{{Role: ollama.RoleUser, Content: "Hello."}}
	if got := FitContext(context.TODO(), nil, "m", store.KindDesigner, opts, short); got.NumCtx != 8192 {
		t.Errorf("a short chat was widened to %d", got.NumCtx)
	}
	long := []ollama.Message{{Role: ollama.RoleUser, Content: strings.Repeat("word ", 30000)}} // 150,000 characters
	got := FitContext(context.TODO(), nil, "m", store.KindDesigner, opts, long)
	if got.NumCtx != 65536 {
		t.Errorf("150,000 characters got a %d window, want 65536", got.NumCtx)
	}
	if got := FitContext(context.TODO(), nil, "m", store.KindRoleplay, opts, long); got.NumCtx != 8192 {
		t.Errorf("a scene was widened to %d; it keeps to its budget", got.NumCtx)
	}
	huge := []ollama.Message{{Role: ollama.RoleUser, Content: strings.Repeat("word ", 200000)}}
	if got := FitContext(context.TODO(), nil, "m", store.KindAssistant, opts, huge); got.NumCtx != maxContext {
		t.Errorf("a million characters got %d, want the ceiling", got.NumCtx)
	}
}
