package websearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// DuckDuckGo searches through DuckDuckGo's plain HTML page, which needs no key
// and no account.
//
// It exists so search works on a machine with nothing set up. SearXNG is the
// better choice, it is yours and it asks several engines at once, but it is a
// server someone has to run, and search switched on by default has to do
// something on the first launch. This is the fallback, and it sends DuckDuckGo
// the search itself and nothing else: not the conversation, not who is asking.
type DuckDuckGo struct {
	// Endpoint is the page searched, swapped out by the tests.
	Endpoint string
	client   *http.Client
}

// ddgEndpoint is the no-JavaScript results page.
const ddgEndpoint = "https://html.duckduckgo.com/html/"

// NewDuckDuckGo returns the provider.
func NewDuckDuckGo() *DuckDuckGo {
	return &DuckDuckGo{Endpoint: ddgEndpoint, client: &http.Client{Timeout: Timeout}}
}

// Name is what the settings and the notes call it.
func (d *DuckDuckGo) Name() string { return "DuckDuckGo" }

// Search runs one query.
func (d *DuckDuckGo) Search(ctx context.Context, query string, n int) ([]Result, error) {
	form := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// A plain browser string. The page serves a challenge to clients that
	// announce themselves as programs, and there is nothing to be gained by
	// naming Astral to a search engine.
	req.Header.Set("User-Agent", browserAgent)
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach DuckDuckGo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DuckDuckGo answered %s", resp.Status)
	}
	results, err := parseDuckDuckGo(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return Clean(results, n), nil
}

// browserAgent is sent to the search page and to pages that are opened.
const browserAgent = "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"

// parseDuckDuckGo reads the results out of the page.
//
// Each result is an anchor with class result__a, whose href wraps the real
// address in a redirect, and a result__snippet after it. Adverts carry the
// same markup inside an element marked result--ad, and are skipped: a search
// that answers a question with an advert is worse than one that found less.
func parseDuckDuckGo(r io.Reader) ([]Result, error) {
	z := html.NewTokenizer(r)
	var out []Result
	var cur *Result
	// What the text currently being read belongs to: a title, a snippet, or
	// neither. depth counts open elements inside it, so nested <b> tags in a
	// snippet do not end it early.
	reading, depth := "", 0
	adDepth := -1
	level := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() == io.EOF {
				if cur != nil {
					out = append(out, *cur)
				}
				return out, nil
			}
			return out, z.Err()
		case html.StartTagToken:
			tn, hasAttr := z.TagName()
			if voidElement[string(tn)] {
				// Opens and never closes. Counting it would leave every depth
				// after it one too deep. A line break inside a snippet still
				// separates two words.
				if string(tn) == "br" && cur != nil && reading == "snippet" {
					cur.Snippet += " "
				}
				continue
			}
			level++
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				attrs[string(k)] = string(v)
			}
			class := " " + attrs["class"] + " "
			if strings.Contains(class, " result--ad ") && adDepth < 0 {
				adDepth = level
			}
			if reading != "" {
				depth++
				continue
			}
			if adDepth >= 0 {
				continue
			}
			switch {
			case string(tn) == "a" && strings.Contains(class, " result__a "):
				if cur != nil {
					out = append(out, *cur)
				}
				cur = &Result{URL: unwrapDDG(attrs["href"])}
				reading, depth = "title", 0
			case strings.Contains(class, " result__snippet ") && cur != nil:
				reading, depth = "snippet", 0
			}
		case html.EndTagToken:
			if adDepth >= 0 && level == adDepth {
				adDepth = -1
			}
			level--
			if reading != "" {
				if depth == 0 {
					reading = ""
				} else {
					depth--
				}
			}
		case html.TextToken:
			if reading == "" || cur == nil || adDepth >= 0 {
				continue
			}
			text := string(z.Text())
			if reading == "title" {
				cur.Title += text
			} else {
				cur.Snippet += text
			}
		}
	}
}

// unwrapDDG takes the real address out of DuckDuckGo's redirect.
func unwrapDDG(href string) string {
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if real := u.Query().Get("uddg"); real != "" {
		return real
	}
	return href
}

// voidElement lists the elements that have no closing tag.
var voidElement = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true,
	"track": true, "wbr": true,
}
