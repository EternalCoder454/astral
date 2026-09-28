package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Reading a page, not just its search result.
//
// A search result is a title and two lines, which is enough to know a page is
// relevant and rarely enough to answer from. The model can open one, and gets
// the page's text with the navigation, scripts and footers taken out. What it
// opens is also what Astral keeps for later, which is most of what "studying" a
// subject means here.

// Page is one page's readable text.
type Page struct {
	URL   string
	Title string
	Text  string
}

// MaxPageText bounds what one page contributes to the context. Six thousand
// characters is a long article's worth of substance and still leaves an 8k
// window room for the conversation it is answering.
const MaxPageText = 6000

// maxPageBytes bounds what is downloaded. A page that is still arriving after
// three megabytes is not an article.
const maxPageBytes = 3 << 20

// ErrPrivateAddress is returned for an address on this machine or this network.
var ErrPrivateAddress = errors.New("that address is on this machine or this network, and is not opened")

// Fetcher opens pages.
type Fetcher struct {
	client *http.Client
	// allowPrivate lets the tests open a page on a local test server. Nothing
	// else sets it.
	allowPrivate bool
}

// NewFetcher returns a Fetcher that refuses private addresses.
//
// The model chooses what to open, and the model is reading pages written by
// strangers, any of which can ask it to open something. Without this, a page
// could have it fetch the router's admin page or Ollama's own API on
// localhost and read the answer back into the conversation. The check is made
// on the address actually connected to, after the name is resolved, so a
// public name that resolves to a private address is refused as well.
func NewFetcher() *Fetcher {
	f := &Fetcher{}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			if f.allowPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || private(ip) {
				return ErrPrivateAddress
			}
			return nil
		},
	}
	f.client = &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext, Proxy: nil},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("redirected somewhere that is not a web page")
			}
			return nil
		},
	}
	return f
}

// private reports whether an address belongs to this machine or a private
// network.
func private(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		// Carrier-grade NAT, which Go does not count as private and which is
		// still somebody's network rather than the internet.
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64)
}

// Open fetches a page and returns its readable text.
func (f *Fetcher) Open(ctx context.Context, rawURL string) (Page, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Page{}, fmt.Errorf("%q is not a web address", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("User-Agent", browserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9")
	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrPrivateAddress) {
			return Page{}, ErrPrivateAddress
		}
		return Page{}, fmt.Errorf("could not open the page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Page{}, fmt.Errorf("the page answered %s", resp.Status)
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	body := io.LimitReader(resp.Body, maxPageBytes)
	page := Page{URL: resp.Request.URL.String()}
	switch {
	case mediaType == "text/plain":
		b, err := io.ReadAll(body)
		if err != nil {
			return Page{}, err
		}
		page.Text = collapse(string(b))
	case mediaType == "" || strings.Contains(mediaType, "html"):
		page.Title, page.Text = Readable(body)
	default:
		return Page{}, fmt.Errorf("the page is %s, which cannot be read as text", mediaType)
	}
	if strings.TrimSpace(page.Text) == "" {
		return Page{}, errors.New("the page had no readable text; it may need JavaScript")
	}
	page.Text = truncate(page.Text, MaxPageText)
	return page, nil
}

// skipped are elements whose text is never the page's content.
var skipped = map[string]bool{
	"script": true, "style": true, "noscript": true, "svg": true, "template": true,
	"nav": true, "header": true, "footer": true, "aside": true, "form": true,
	"button": true, "select": true, "iframe": true, "head": true,
}

// block elements end a line, so their text does not run into the next one's.
var block = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "tr": true, "section": true, "article": true,
	"pre": true, "blockquote": true, "dd": true, "dt": true, "table": true, "ul": true, "ol": true,
}

// Readable returns a page's title and the text of its content.
//
// Where the page marks its content with <main> or <article>, only that is
// taken, which removes most of what a site wraps around every page. Otherwise
// the whole body is used, minus the elements that are never content.
func Readable(r io.Reader) (title, text string) {
	z := html.NewTokenizer(r)
	var all, main strings.Builder
	skip := 0   // depth inside a skipped element
	inMain := 0 // depth inside <main> or <article>
	inTitle := false
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			body := main.String()
			if strings.TrimSpace(body) == "" || utf8.RuneCountInString(strings.TrimSpace(body)) < 200 {
				body = all.String()
			}
			return collapse(title), collapseLines(body)
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if tag == "title" && tt == html.StartTagToken {
				inTitle = true
			}
			if voidElement[tag] || tt == html.SelfClosingTagToken {
				if block[tag] {
					all.WriteByte('\n')
					if inMain > 0 {
						main.WriteByte('\n')
					}
				}
				continue
			}
			if skipped[tag] {
				skip++
				continue
			}
			if tag == "main" || tag == "article" {
				inMain++
			}
			if block[tag] {
				all.WriteByte('\n')
				if inMain > 0 {
					main.WriteByte('\n')
				}
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if tag == "title" {
				inTitle = false
			}
			if skipped[tag] {
				if skip > 0 {
					skip--
				}
				continue
			}
			if (tag == "main" || tag == "article") && inMain > 0 {
				inMain--
			}
			if block[tag] {
				all.WriteByte('\n')
				if inMain > 0 {
					main.WriteByte('\n')
				}
			}
		case html.TextToken:
			t := string(z.Text())
			if inTitle {
				title += t
				continue
			}
			if skip > 0 {
				continue
			}
			all.WriteString(t)
			if inMain > 0 {
				main.WriteString(t)
			}
		}
	}
}

// collapse squeezes all whitespace to single spaces.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// collapseLines keeps line breaks between blocks but squeezes everything else,
// and drops the blank and near-blank lines a page leaves behind.
func collapseLines(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = collapse(line)
		if len(line) < 2 {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
