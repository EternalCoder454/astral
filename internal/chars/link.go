package chars

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Importing a character from a link, the way SillyTavern and KoboldAI Lite
// take a Chub page or a card's own address.
//
// A Chub page is turned into the address of its card image on Chub's image
// server, which serves the card whole; anything else is fetched as it is and
// read as a card. What arrives is a downloaded file from a stranger, and is
// treated as data exactly as an imported file is: see ParseCard.

// maxLinkedCardBytes bounds a downloaded card. Real ones are a few megabytes at
// most, the image being most of it.
const maxLinkedCardBytes = 24 << 20

// CardLink resolves a link someone pasted to the address a card can be
// downloaded from.
func CardLink(link string) (string, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return "", fmt.Errorf("paste a link first")
	}
	if !strings.Contains(link, "://") {
		link = "https://" + link
	}
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("that is not a link")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("only web links can be imported")
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	switch host {
	case "chub.ai", "venus.chub.ai", "characterhub.org":
		// /characters/{creator}/{name}, sometimes with more after it.
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 3 && parts[0] == "characters" {
			return "https://avatars.charhub.io/avatars/" + url.PathEscape(parts[1]) + "/" +
				url.PathEscape(parts[2]) + "/chara_card_v2.png", nil
		}
		return "", fmt.Errorf("that Chub link is not a character's page")
	}
	return u.String(), nil
}

// FetchCard downloads a card from a link and reads it, returning the image as
// the avatar when the card is a PNG.
func FetchCard(ctx context.Context, link string) (Character, []byte, error) {
	target, err := CardLink(link)
	if err != nil {
		return Character{}, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Character{}, nil, err
	}
	req.Header.Set("User-Agent", "Astral")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return Character{}, nil, fmt.Errorf("could not download it: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Character{}, nil, fmt.Errorf("the site answered %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxLinkedCardBytes+1))
	if err != nil {
		return Character{}, nil, fmt.Errorf("the download stopped: %w", err)
	}
	if len(data) > maxLinkedCardBytes {
		return Character{}, nil, fmt.Errorf("that file is too large to be a character card")
	}
	if bytes.HasPrefix(data, pngMagic) {
		ch, err := importPNG(data)
		if err != nil {
			return Character{}, nil, fmt.Errorf("that image has no character card in it")
		}
		return ch, data, nil
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		ch, err := ParseCard(trimmed)
		return ch, nil, err
	}
	return Character{}, nil, fmt.Errorf("that link is a web page, not a card: link to the card's .png or .json, or a Chub character page")
}

// AccentCount is how many character tints the stylesheets define.
const AccentCount = 8

// AccentFor derives a stable tint from a character's name, so the same
// character keeps the same colour across machines without it being stored,
// and so importing a folder of cards produces a varied cast rather than eight
// shades of clay. FNV-1a because it is three lines and spreads short strings
// well; nothing here needs a cryptographic hash.
func AccentFor(name string, count int) int {
	const (
		offset = 2166136261
		prime  = 16777619
	)
	h := uint32(offset)
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= prime
	}
	if count <= 0 {
		count = 1
	}
	return int(h % uint32(count))
}
