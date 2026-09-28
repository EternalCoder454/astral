package websearch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The shape of DuckDuckGo's results page, reduced to what the parser reads.
// Written by hand rather than saved from the site, so the test does not carry
// a copy of anyone's search results.
const ddgFixture = `<!DOCTYPE html><html><body>
<div class="results">
 <div class="result results_links results_links_deep result--ad">
  <h2 class="result__title"><a class="result__a" href="//duckduckgo.com/y.js?ad=1">Buy Now</a></h2>
  <a class="result__snippet" href="#">An advert.</a>
 </div>
 <div class="result results_links results_links_deep web-result ">
  <h2 class="result__title">
   <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2Freleases&amp;rut=abc">Releases &amp; notes</a>
  </h2>
  <div class="result__extras"><img class="result__icon__img" src="x.ico"><span class="result__url">example.org</span></div>
  <a class="result__snippet" href="#">The <b>latest</b> release<br>fixes the loader.</a>
 </div>
 <div class="result results_links results_links_deep web-result ">
  <h2 class="result__title"><a class="result__a" href="https://example.com/direct">Direct link</a></h2>
  <a class="result__snippet" href="#">Second snippet.</a>
 </div>
</div></body></html>`

func TestParseDuckDuckGo(t *testing.T) {
	got, err := parseDuckDuckGo(strings.NewReader(ddgFixture))
	if err != nil {
		t.Fatal(err)
	}
	got = Clean(got, 10)
	if len(got) != 2 {
		t.Fatalf("want 2 results with the advert skipped, got %d: %+v", len(got), got)
	}
	if got[0].URL != "https://example.org/releases" {
		t.Errorf("the redirect was not unwrapped: %q", got[0].URL)
	}
	if got[0].Title != "Releases & notes" {
		t.Errorf("title %q", got[0].Title)
	}
	if got[0].Snippet != "The latest release fixes the loader." {
		t.Errorf("snippet %q", got[0].Snippet)
	}
	if got[1].URL != "https://example.com/direct" || got[1].Snippet != "Second snippet." {
		t.Errorf("second result %+v", got[1])
	}
}

func TestDuckDuckGoSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("q") != "ollama release" {
			t.Errorf("sent query %q", r.Form.Get("q"))
		}
		w.Write([]byte(ddgFixture))
	}))
	defer srv.Close()
	d := NewDuckDuckGo()
	d.Endpoint = srv.URL
	got, err := d.Search(context.Background(), "ollama release", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("asked for 1, got %d", len(got))
	}
}

func TestReadablePrefersTheArticle(t *testing.T) {
	page := `<html><head><title> The Tide Tables </title><style>body{}</style></head><body>
<nav><a>Home</a><a>About</a></nav>
<header>Site banner</header>
<article><h1>Tide tables</h1><p>` + strings.Repeat("High water at Kestrel Bay comes forty minutes later each day. ", 6) + `</p>
<script>track()</script><p>Second paragraph.</p></article>
<footer>Copyright</footer></body></html>`
	title, text := Readable(strings.NewReader(page))
	if title != "The Tide Tables" {
		t.Errorf("title %q", title)
	}
	for _, unwanted := range []string{"Home", "Site banner", "track()", "Copyright", "body{}"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("kept %q:\n%s", unwanted, text)
		}
	}
	if !strings.Contains(text, "High water at Kestrel Bay") || !strings.Contains(text, "Second paragraph.") {
		t.Errorf("lost the content:\n%s", text)
	}
	if !strings.Contains(text, "Tide tables\nHigh water") {
		t.Errorf("block elements ran together:\n%s", text)
	}
}

func TestReadableFallsBackToTheBody(t *testing.T) {
	// A page with an empty <article> tag somewhere is not a page whose content
	// is empty.
	page := `<html><body><article></article><div><p>` + strings.Repeat("Useful text. ", 30) + `</p></div></body></html>`
	_, text := Readable(strings.NewReader(page))
	if !strings.Contains(text, "Useful text.") {
		t.Errorf("fell back to nothing: %q", text)
	}
}

func TestPrivateAddresses(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "10.0.0.5", "192.168.1.1", "172.16.0.1",
		"169.254.1.1", "100.64.0.1", "0.0.0.0", "fe80::1", "fd00::1"} {
		if !private(net.ParseIP(s)) {
			t.Errorf("%s was treated as public", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "93.184.216.34", "2606:4700::1111", "100.128.0.1"} {
		if private(net.ParseIP(s)) {
			t.Errorf("%s was treated as private", s)
		}
	}
}

func TestOpenRefusesThisMachine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a local server was reached")
	}))
	defer srv.Close()
	_, err := NewFetcher().Open(context.Background(), srv.URL)
	if !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("want ErrPrivateAddress, got %v", err)
	}
}

func TestOpenRefusesRedirectsIntoThisMachine(t *testing.T) {
	// The check is on the connection, so a redirect to localhost fails the same
	// way a direct link does. Simulated by allowing only the first hop.
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the redirect was followed to a local server")
	}))
	defer inner.Close()
	f := NewFetcher()
	hops := 0
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		f.allowPrivate = false // after the first hop, the guard is back on
		http.Redirect(w, r, inner.URL, http.StatusFound)
	}))
	defer outer.Close()
	f.allowPrivate = true
	_, err := f.Open(context.Background(), outer.URL)
	if !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("want ErrPrivateAddress, got %v", err)
	}
}

func TestOpenReadsAPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><head><title>Notes</title></head><body><main><p>` +
			strings.Repeat("The ferry runs at nine. ", 20) + `</p></main></body></html>`))
	}))
	defer srv.Close()
	f := NewFetcher()
	f.allowPrivate = true
	p, err := f.Open(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Notes" || !strings.Contains(p.Text, "The ferry runs at nine.") {
		t.Errorf("got %+v", p)
	}
}

func TestOpenRejectsWhatIsNotAWebAddress(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.org/x", "not a url", "javascript:alert(1)"} {
		if _, err := NewFetcher().Open(context.Background(), u); err == nil {
			t.Errorf("%q was opened", u)
		}
	}
}

func TestOpenRejectsBinaries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write([]byte("PK"))
	}))
	defer srv.Close()
	f := NewFetcher()
	f.allowPrivate = true
	if _, err := f.Open(context.Background(), srv.URL); err == nil {
		t.Error("a zip file was read as a page")
	}
}
