package websearch

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Auto uses SearXNG when there is one and DuckDuckGo when there is not.
//
// Search is switched on by default, and on a fresh machine there is no SearXNG:
// it is a server someone has to install and run. Without a fallback, the
// default would be a model told it can search and every search failing. With
// one, the first question that needs the web gets it, and anyone who does run
// SearXNG keeps using theirs.
type Auto struct {
	searx    *SearXNGProvider
	fallback Provider

	mu sync.Mutex
	// downUntil is when SearXNG is next worth trying after a failure. A
	// server that is not running refuses instantly, but one on a machine that
	// has gone away times out, and nobody should wait for that on every search.
	downUntil time.Time
	last      string
}

// autoRetry is how long a SearXNG that failed is left alone.
const autoRetry = 2 * time.Minute

// NewAuto returns the provider. An empty address means DuckDuckGo only.
func NewAuto(searxURL string) *Auto {
	a := &Auto{fallback: NewDuckDuckGo()}
	if strings.TrimSpace(searxURL) != "" {
		a.searx = NewSearXNG(searxURL)
	}
	return a
}

// Name says which engine answered last, or which one will be tried first.
func (a *Auto) Name() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last != "" {
		return a.last
	}
	if a.searx != nil {
		return "SearXNG"
	}
	return a.fallback.Name()
}

// Search tries SearXNG first when it is configured and has not just failed.
func (a *Auto) Search(ctx context.Context, query string, n int) ([]Result, error) {
	if a.searx != nil && a.searxUp() {
		results, err := a.searx.Search(ctx, query, n)
		if err == nil {
			a.used(a.searx.Name())
			return results, nil
		}
		// The person pressing Stop is not SearXNG being down.
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return nil, err
		}
		a.markDown()
	}
	results, err := a.fallback.Search(ctx, query, n)
	if err == nil {
		a.used(a.fallback.Name())
	}
	return results, err
}

func (a *Auto) searxUp() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().After(a.downUntil)
}

func (a *Auto) markDown() {
	a.mu.Lock()
	a.downUntil = time.Now().Add(autoRetry)
	a.mu.Unlock()
}

func (a *Auto) used(name string) {
	a.mu.Lock()
	a.last = name
	a.mu.Unlock()
}
