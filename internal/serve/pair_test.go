package serve

import (
	"strings"
	"testing"
	"time"
)

func TestPairingIsOneUse(t *testing.T) {
	var p pairing
	code, err := p.open()
	if err != nil {
		t.Fatal(err)
	}
	if !p.claim(code) {
		t.Fatal("the right code was refused")
	}
	if p.claim(code) {
		t.Error("the same code was accepted twice")
	}
}

func TestPairingRefusesWrongCodes(t *testing.T) {
	var p pairing
	code, _ := p.open()
	wrong := strings.Repeat("A", pairCodeLen)
	if wrong == code {
		wrong = strings.Repeat("B", pairCodeLen)
	}
	if p.claim(wrong) {
		t.Fatal("a wrong code was accepted")
	}
	if !p.claim(code) {
		t.Error("a wrong attempt invalidated the right code")
	}
}

// The attempt limit is what makes a short code safe, so it has to hold even
// when every guess is wrong and fast.
func TestPairingClosesAfterTooManyAttempts(t *testing.T) {
	var p pairing
	code, _ := p.open()
	for i := 0; i < pairAttempts+1; i++ {
		p.claim("XXXXXXXX")
	}
	if p.claim(code) {
		t.Error("the right code still worked after the attempt limit was passed")
	}
	if _, _, open := p.current(); open {
		t.Error("pairing is still open after the attempt limit")
	}
}

func TestPairingExpires(t *testing.T) {
	var p pairing
	code, _ := p.open()
	p.mu.Lock()
	p.expires = time.Now().Add(-time.Second)
	p.mu.Unlock()
	if p.claim(code) {
		t.Error("an expired code was accepted")
	}
	if _, _, open := p.current(); open {
		t.Error("an expired pairing still reports itself open")
	}
}

func TestNothingIsOpenUntilItIsOpened(t *testing.T) {
	var p pairing
	if _, _, open := p.current(); open {
		t.Error("a fresh pairing reports itself open")
	}
	if p.claim("") || p.claim("XXXXXXXX") {
		t.Error("a closed pairing accepted a code")
	}
}

// Codes and tokens must not repeat, and must be what they claim to be.
func TestCodesAndTokensAreRandom(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		c, err := randomCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != pairCodeLen {
			t.Fatalf("code %q is %d characters, want %d", c, len(c), pairCodeLen)
		}
		if strings.ContainsAny(c, "01OI") {
			t.Errorf("code %q contains a character that is misread when typed", c)
		}
		if seen[c] {
			t.Fatalf("code %q came up twice in 500", c)
		}
		seen[c] = true
	}
	tokens := map[string]bool{}
	for i := 0; i < 500; i++ {
		tok, err := newToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) < 40 {
			t.Fatalf("token %q is too short", tok)
		}
		if tokens[tok] {
			t.Fatal("a token came up twice")
		}
		tokens[tok] = true
	}
}
