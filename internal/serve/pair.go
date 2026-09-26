// Package serve lets another device on your network use this machine's Astral:
// its library, and the models running on it.
//
// The shape of the thing is the point. A 27B model does not run on a phone and
// will not for a long time, so the phone is a screen and the PC is the
// computer. Nothing is uploaded anywhere, there is no account on anyone's
// server, and with the server switched off the app is exactly what it was.
package serve

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"sync"
	"time"
)

// pairAlphabet has no 0/O or 1/I in it, because the code is read off one
// screen and typed into another.
const pairAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// pairCodeLen is eight characters of that alphabet: forty bits, which is not
// something anyone guesses inside a five minute window against a limiter that
// stops after ten wrong answers.
const pairCodeLen = 8

// pairWindow is how long a code is good for. Long enough to walk to the other
// room, short enough that a code left on screen is not a standing invitation.
const pairWindow = 5 * time.Minute

// pairAttempts is how many wrong codes are accepted before pairing closes and
// has to be started again from the PC. The limit is the real defence: it is
// what turns forty bits into far more than anyone can work through.
const pairAttempts = 10

// pairing is a code waiting to be used. One at a time: two open codes would
// mean two ways in, for a feature whose whole job is to have exactly one.
type pairing struct {
	mu       sync.Mutex
	code     string
	expires  time.Time
	attempts int
}

// open starts a pairing and returns the code to show.
func (p *pairing) open() (string, error) {
	code, err := randomCode()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.code, p.expires, p.attempts = code, time.Now().Add(pairWindow), 0
	return code, nil
}

// close ends any pairing in progress.
func (p *pairing) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.code, p.attempts = "", 0
}

// current returns the code being offered and how long is left, or false when
// nothing is open.
func (p *pairing) current() (string, time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.code == "" || time.Now().After(p.expires) {
		return "", 0, false
	}
	return p.code, time.Until(p.expires), true
}

// claim checks a code and consumes the pairing if it is right.
//
// The comparison is constant time, and a wrong answer costs an attempt whether
// the code has expired or not, so a caller cannot learn anything from how long
// a refusal took or which refusal they got.
func (p *pairing) claim(try string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.code == "" {
		return false
	}
	p.attempts++
	if p.attempts > pairAttempts || time.Now().After(p.expires) {
		p.code = ""
		return false
	}
	try = strings.ToUpper(strings.TrimSpace(try))
	if subtle.ConstantTimeCompare([]byte(try), []byte(p.code)) != 1 {
		return false
	}
	p.code = "" // one use
	return true
}

func randomCode() (string, error) {
	b := make([]byte, pairCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, pairCodeLen)
	for i, v := range b {
		out[i] = pairAlphabet[int(v)%len(pairAlphabet)]
	}
	return string(out), nil
}

// newToken is what a paired device keeps. Thirty-two random bytes: there is
// nothing to guess and nothing to derive.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
