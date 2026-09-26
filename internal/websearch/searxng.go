package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SearXNG is a self-hosted metasearch engine, and the default here.
//
// It is the default for the reason it exists: it aggregates other engines without
// an account, without an API key, and without keeping anything, and you run it
// yourself, so a query leaves this machine for the search and nothing else about
// it does. That is the closest thing to Astral's arrangement that a web search
// can be.
//
// The trade is that it has to be running somewhere. A public instance will
// usually work and will usually rate-limit, which is why the setting is an
// address rather than a switch.

// SearXNGProvider searches a SearXNG instance.
type SearXNGProvider struct {
	// BaseURL is the instance, with or without a trailing slash.
	BaseURL string
	// HTTP is the client to use. The zero value gets one with Timeout.
	HTTP *http.Client
}

// NewSearXNG builds a provider for an instance address.
func NewSearXNG(baseURL string) *SearXNGProvider {
	return &SearXNGProvider{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		HTTP:    &http.Client{Timeout: Timeout},
	}
}

// Name is what to call it in a message to the user.
func (s *SearXNGProvider) Name() string { return "SearXNG" }

// searxngResponse is the part of the JSON worth reading.
type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// Search queries the instance.
func (s *SearXNGProvider) Search(ctx context.Context, query string, n int) ([]Result, error) {
	base := strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("no SearXNG address is set")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("nothing to search for")
	}
	if n <= 0 {
		n = DefaultResults
	}

	q := url.Values{}
	q.Set("q", query)
	// The JSON format has to be enabled in the instance's settings.yml. When it
	// is not, SearXNG answers 403 and the error below says which knob it is,
	// because nothing else about the failure hints at it.
	q.Set("format", "json")
	q.Set("safesearch", "0")
	// One page is what gets read. Asking for more would multiply the wait for
	// results that are already past the point of being useful.
	q.Set("pageno", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	// Some instances refuse a request with no user agent outright.
	req.Header.Set("User-Agent", "Astral/1 (+local)")

	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: Timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach SearXNG at %s: %w", base, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("SearXNG at %s refused the request. Its JSON format is usually off by default: add \"json\" to search.formats in settings.yml and restart it", base)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("SearXNG at %s is rate limiting. A public instance will do this; running your own does not", base)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("SearXNG at %s returned %s", base, resp.Status)
	}

	// Bounded: this is a document from off the machine, and a provider that
	// answers with a hundred megabytes should fail rather than be believed.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var out searxngResponse
	if err := json.Unmarshal(body, &out); err != nil {
		// The usual cause is an instance that returned its HTML search page
		// because JSON is not enabled, which is worth saying rather than
		// reporting a parse error about a document nobody asked to see.
		if looksLikeHTML(body) {
			return nil, fmt.Errorf("SearXNG at %s answered with a web page rather than JSON. Add \"json\" to search.formats in its settings.yml", base)
		}
		return nil, fmt.Errorf("could not read SearXNG's answer: %w", err)
	}

	results := make([]Result, 0, len(out.Results))
	for _, r := range out.Results {
		results = append(results, Result{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return Clean(results, n), nil
}

func looksLikeHTML(b []byte) bool {
	head := strings.ToLower(strings.TrimSpace(string(b[:min(len(b), 256)])))
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
}

// Probe checks that an address answers, for the button in Settings that says so
// before a scene depends on it.
//
// It searches for something rather than asking for a status page, because the
// failure worth catching is an instance that is running and has JSON turned off,
// and only a real query shows that.
func Probe(ctx context.Context, p Provider) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	results, err := p.Search(ctx, "astral local model test", 3)
	if err != nil {
		return 0, err
	}
	return len(results), nil
}

// ParseResultCount reads a count from a settings field, falling back to the
// default rather than refusing a value somebody typed.
func ParseResultCount(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return DefaultResults
	}
	if n > 10 {
		return 10
	}
	return n
}
