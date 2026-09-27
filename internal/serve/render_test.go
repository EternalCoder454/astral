package serve

import (
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"astral/internal/ui"
)

// The desktop and the phone render the same reply with two separate
// implementations of the same rules, in two languages. They drifted, twice: the
// phone stripped bold inside narration where the desktop kept it, and the phone
// ran its markers over a whole message where the desktop splits on newlines
// first, so one unpaired asterisk re-paired everything after it and a reply came
// out as alternating stretches of correct and broken italics.
//
// Neither was visible from inside either implementation. This renders one corpus
// through both and compares what a reader ends up seeing.
//
// Prose only. The desktop also turns "- " into a bullet, strips heading hashes
// and understands fenced code, none of which the phone claims to do, and none of
// which appears in a roleplay reply.

var tagPattern = regexp.MustCompile(`<[^>]*>`)

// visible is what is left after the markup: the words a reader actually sees.
func visible(markup string) string {
	s := tagPattern.ReplaceAllString(markup, "")
	// Undone in this order, and ampersand last, or the entities produced by
	// undoing the other two would be unescaped a second time.
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	return strings.ReplaceAll(s, "&amp;", "&")
}

type phoneRender struct {
	Reply       string `json:"reply"`
	Own         string `json:"own"`
	Plain       string `json:"plain"`
	ReplyStyles string `json:"replyStyles"`
	OwnStyles   string `json:"ownStyles"`
	PlainStyles string `json:"plainStyles"`
}

// styles reduces Pango markup to one letter per visible character, naming what
// is styling it. Comparing only the words would miss the half of a rendering
// bug that shows the right words in the wrong style, which is most of what a
// reader notices: speech set as narration reads as narration.
//
// The letters match the ones the phone's side of this emits.
func styles(markup string) string {
	var out, stack []byte
	for i := 0; i < len(markup); {
		if markup[i] != '<' {
			if markup[i] == '&' {
				if end := strings.IndexByte(markup[i:], ';'); end >= 0 {
					i += end + 1
				} else {
					i++
				}
			} else {
				// One letter per character the reader sees, and a rune is one
				// character however many bytes it takes.
				_, size := utf8.DecodeRuneInString(markup[i:])
				i += size
			}
			if len(stack) > 0 {
				out = append(out, stack[len(stack)-1])
			} else {
				out = append(out, '-')
			}
			continue
		}
		end := strings.IndexByte(markup[i:], '>')
		if end < 0 {
			break
		}
		tag := markup[i+1 : i+end]
		i += end + 1
		if strings.HasPrefix(tag, "/") {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		switch {
		case strings.HasPrefix(tag, `span alpha=`):
			stack = append(stack, 'N')
		case strings.HasPrefix(tag, `span weight=`):
			stack = append(stack, 'S')
		case tag == "b":
			stack = append(stack, 'B')
		case tag == "i":
			stack = append(stack, 'I')
		case tag == "tt":
			stack = append(stack, 'C')
		default:
			stack = append(stack, '?')
		}
	}
	return string(out)
}

// The desktop opens narration as a span and an italic together, so every
// character inside it is reported as italic rather than as narration. The
// phone has one element for the same thing. Flattening the pair is what lets
// the two be compared at all.
func flattenNarration(s string) string {
	return strings.ReplaceAll(s, "I", "N")
}

func TestBothRenderersShowTheReaderTheSameWords(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed, so the phone's renderer cannot be run here")
	}
	dir := filepath.Join("web")

	raw, err := os.ReadFile(filepath.Join(dir, "rendercases.json"))
	if err != nil {
		t.Fatalf("reading the corpus: %v", err)
	}
	var cases []string
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parsing the corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("the corpus is empty")
	}

	// The committed cases, then generated ones. The hand written corpus covers
	// the shapes that have actually gone wrong; the generated ones cover the
	// combinations nobody thought to write down, which is where both of the
	// divergences found so far were hiding.
	cases = append(cases, generatedCases(20000)...)

	corpus := filepath.Join(t.TempDir(), "cases.json")
	packed, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corpus, packed, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(node, filepath.Join(dir, "render_check.mjs"), corpus).Output()
	if err != nil {
		t.Fatalf("running the phone's renderer: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("the phone rendered %d cases, the corpus has %d", len(lines), len(cases))
	}

	for i, in := range cases {
		var phone phoneRender
		if err := json.Unmarshal([]byte(lines[i]), &phone); err != nil {
			t.Fatalf("case %d: parsing the phone's output: %v", i, err)
		}
		reply := ui.Markup(in, ui.Roleplay)
		own := ui.Markup(in, ui.RoleplayAsWritten)
		plain := ui.Markup(in, ui.Plain)
		for _, pair := range []struct {
			what  string
			desk  string
			phone string
		}{
			{"the words of a model's reply", visible(reply), phone.Reply},
			{"the words of your own message", visible(own), phone.Own},
			{"the styling of a model's reply", flattenNarration(styles(reply)), flattenNarration(phone.ReplyStyles)},
			{"the styling of your own message", flattenNarration(styles(own)), flattenNarration(phone.OwnStyles)},
			{"the words of a general chat reply", visible(plain), phone.Plain},
			{"the styling of a general chat reply", styles(plain), phone.PlainStyles},
		} {
			if pair.desk != pair.phone {
				t.Errorf("case %d, %s, the two screens disagree\n  in      %q\n  desktop %q\n  phone   %q",
					i, pair.what, in, pair.desk, pair.phone)
			}
		}
	}
}

// generatedCases builds roleplay-shaped prose out of the pieces that decide how
// it is rendered: the markers, the quotation marks, and the line breaks between
// paragraphs. Words are incidental, so there are only a few.
//
// Two markers are never emitted back to back. The two renderers do disagree on
// input like "**_****_", where the pairing is ambiguous and each resolves it
// its own way, and matching them there would mean contorting both for text no
// model produces. Everything a model does produce has words between its
// markers, and that is what is generated here.
//
// Seeded, so a failure is reproducible and reviewing this test does not mean
// waiting to see which run finds the problem.
func generatedCases(n int) []string {
	markers := []string{"*", "**", "_", `"`, "\u201c", "\u201d", "`"}
	text := []string{
		" ", "\n", "\n\n", "she said", "late", "turned", ".", ",",
		"5 * 3", "the Gannet", "- a bullet", "# a heading",
	}
	rng := rand.New(rand.NewSource(1))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var b strings.Builder
		wasMarker := false
		for j := 0; j < 2+rng.Intn(12); j++ {
			if !wasMarker && rng.Intn(2) == 0 {
				b.WriteString(markers[rng.Intn(len(markers))])
				wasMarker = true
				continue
			}
			b.WriteString(text[rng.Intn(len(text))])
			wasMarker = false
		}
		out = append(out, b.String())
	}
	return out
}
