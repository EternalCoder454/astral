package chars

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Character cards travel in two shapes: a .json file, or a .png whose pixels
// are the avatar and whose metadata carries the card as base64 JSON in a text
// chunk. The PNG form is what almost everything in circulation uses, so an
// importer that only reads JSON would in practice read almost nothing.
//
// Three spec generations exist. V1 is a flat object; V2 wraps the same fields
// in {"spec":"chara_card_v2","data":{…}} and adds creator metadata; V3 keeps
// V2's shape under a different chunk key. All three are accepted, because
// which one a card uses is an accident of when it was made.
const (
	chunkKeyV2 = "chara" // V1 and V2 both use this key
	chunkKeyV3 = "ccv3"
)

// maxCardBytes caps the decoded JSON. Cards are a few KB of prose; anything
// past this is a malformed or hostile file, and the cap keeps a bad one from
// being read into memory in full.
const maxCardBytes = 4 << 20 // 4 MiB

// cardFile is the V2/V3 envelope. V1 files have no "data", which is exactly
// how the two are told apart.
type cardFile struct {
	Spec string    `json:"spec"`
	Data *cardData `json:"data"`
	cardData
}

// cardData is the card's fields. The json tags are the spec's names.
type cardData struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	Personality            string   `json:"personality"`
	Scenario               string   `json:"scenario"`
	FirstMes               string   `json:"first_mes"`
	MesExample             string   `json:"mes_example"`
	SystemPrompt           string   `json:"system_prompt"`
	PostHistoryInstruction string   `json:"post_history_instructions"`
	AlternateGreetings     []string `json:"alternate_greetings"`
	Creator                string   `json:"creator"`
	CreatorNotes           string   `json:"creator_notes"`
	CharacterVersion       string   `json:"character_version"`
	Tags                   []string `json:"tags"`
}

func (d cardData) toCharacter() Character {
	return Character{
		Name:        strings.TrimSpace(d.Name),
		Description: d.Description,
		Personality: d.Personality,
		Scenario:    d.Scenario,
		FirstMes:    d.FirstMes,
		MesExample:  d.MesExample,
		// A card's two instruction fields both become Instructions. The spec
		// separates them by *where* they are injected, which is Astral's
		// decision to make rather than the card's — and one field the user
		// can actually find beats two they have to tell apart.
		Instructions: joinInstructions(d.SystemPrompt, d.PostHistoryInstruction),
		AltGreetings: d.AlternateGreetings,
		Creator:      strings.TrimSpace(d.Creator),
		Notes:        d.CreatorNotes,
		Version:      strings.TrimSpace(d.CharacterVersion),
		Tags:         cleanTags(d.Tags),
	}
}

// joinInstructions merges a card's instruction fields, dropping empties so
// the result never opens with a blank line.
func joinInstructions(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

func cleanTags(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseCard reads a character card from raw JSON, accepting V1, V2 and V3.
func ParseCard(data []byte) (Character, error) {
	if len(data) > maxCardBytes {
		return Character{}, fmt.Errorf("card is too large (%d bytes)", len(data))
	}
	var cf cardFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return Character{}, fmt.Errorf("not a valid character card: %w", err)
	}
	src := cf.cardData
	if cf.Data != nil {
		src = *cf.Data // V2/V3: the envelope wins over any stray top-level keys
	}
	ch := src.toCharacter()
	if ch.Name == "" {
		return Character{}, fmt.Errorf("card has no character name")
	}
	return ch, nil
}

// ImportFile reads a character card from a .json or .png file. For a PNG the
// image itself is returned as avatar bytes, so importing a card gets you the
// portrait as well as the persona in one step.
func ImportFile(path string) (Character, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Character{}, nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		ch, err := importPNG(data)
		if err != nil {
			return Character{}, nil, err
		}
		return ch, data, nil
	case ".json":
		ch, err := ParseCard(data)
		return ch, nil, err
	default:
		// Sniff rather than refuse: cards are routinely saved with no
		// extension, or with the wrong one after a download.
		if bytes.HasPrefix(data, pngMagic) {
			ch, err := importPNG(data)
			if err != nil {
				return Character{}, nil, err
			}
			return ch, data, nil
		}
		ch, err := ParseCard(data)
		return ch, nil, err
	}
}

func importPNG(data []byte) (Character, error) {
	text, err := pngText(data)
	if err != nil {
		return Character{}, err
	}
	// V3 first: a card carrying both is a V2 file upgraded in place, and the
	// V3 chunk is the newer of the two.
	for _, key := range []string{chunkKeyV3, chunkKeyV2} {
		enc, ok := text[key]
		if !ok {
			continue
		}
		raw, err := decodeCardPayload(enc)
		if err != nil {
			return Character{}, fmt.Errorf("card metadata in this PNG is corrupt: %w", err)
		}
		return ParseCard(raw)
	}
	return Character{}, fmt.Errorf("this PNG has no character card embedded in it")
}

// decodeCardPayload un-base64s a chunk value. Some exporters store the JSON
// unencoded, so a value that already looks like JSON is passed through.
func decodeCardPayload(s string) ([]byte, error) {
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") {
		return []byte(t), nil
	}
	// Encoders disagree about padding and line breaks; strip whitespace and
	// accept either alphabet rather than failing on a cosmetic difference.
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(clean); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("could not decode base64 payload")
}

var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// pngText walks a PNG's chunks and returns its textual metadata.
//
// Go's image/png does not expose text chunks at all, so this is a manual walk.
// It is also why the walk is written defensively: it is parsing an untrusted
// file, and every length it reads is checked against what is actually left in
// the buffer before being used.
func pngText(data []byte) (map[string]string, error) {
	if !bytes.HasPrefix(data, pngMagic) {
		return nil, fmt.Errorf("not a PNG file")
	}
	out := map[string]string{}
	pos := len(pngMagic)
	for pos+8 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		typ := string(data[pos+4 : pos+8])
		body := pos + 8
		// length is attacker-controlled: reject anything that would run past
		// the end of the buffer (or wrap negative) before slicing with it.
		if length < 0 || body+length+4 > len(data) {
			break
		}
		chunk := data[body : body+length]
		switch typ {
		case "tEXt":
			if k, v, ok := bytes.Cut(chunk, []byte{0}); ok {
				out[string(k)] = string(v)
			}
		case "zTXt":
			if k, rest, ok := bytes.Cut(chunk, []byte{0}); ok && len(rest) > 1 {
				if v, err := zlibInflate(rest[1:]); err == nil { // rest[0] = method
					out[string(k)] = v
				}
			}
		case "iTXt":
			if k, v, ok := parseITXt(chunk); ok {
				out[k] = v
			}
		case "IEND":
			return out, nil
		}
		pos = body + length + 4 // skip the chunk's CRC
	}
	return out, nil
}

// parseITXt reads an international text chunk:
//
//	keyword \0 compressionFlag compressionMethod languageTag \0 translatedKeyword \0 text
func parseITXt(chunk []byte) (string, string, bool) {
	key, rest, ok := bytes.Cut(chunk, []byte{0})
	if !ok || len(rest) < 2 {
		return "", "", false
	}
	compressed := rest[0] == 1
	rest = rest[2:]                                 // compression flag + method
	if _, r, ok := bytes.Cut(rest, []byte{0}); ok { // language tag
		rest = r
	} else {
		return "", "", false
	}
	if _, r, ok := bytes.Cut(rest, []byte{0}); ok { // translated keyword
		rest = r
	} else {
		return "", "", false
	}
	if !compressed {
		return string(key), string(rest), true
	}
	v, err := zlibInflate(rest)
	if err != nil {
		return "", "", false
	}
	return string(key), v, true
}

// zlibInflate decompresses a chunk payload, bounded so a compression bomb in a
// downloaded card cannot exhaust memory.
func zlibInflate(b []byte) (string, error) {
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, maxCardBytes))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ExportCard renders a character as V2 card JSON, so anything imported can be
// handed back to the ecosystem it came from.
func ExportCard(c Character) ([]byte, error) {
	d := cardData{
		Name:        c.Name,
		Description: c.Description,
		Personality: c.Personality,
		Scenario:    c.Scenario,
		FirstMes:    c.FirstMes,
		MesExample:  c.MesExample,
		// Written to system_prompt so a round trip through Astral returns the
		// same text to the same place. Other tools read that field as a
		// framing replacement; Astral does not, which is the point.
		SystemPrompt:       c.Instructions,
		AlternateGreetings: c.AltGreetings,
		Creator:            c.Creator,
		CreatorNotes:       c.Notes,
		CharacterVersion:   c.Version,
		Tags:               c.Tags,
	}
	// Both shapes are written: the envelope for V2 readers, and the same
	// fields at the top level so a V1-only reader still gets the character.
	return json.MarshalIndent(struct {
		Spec    string   `json:"spec"`
		Version string   `json:"spec_version"`
		Data    cardData `json:"data"`
		cardData
	}{
		Spec:     "chara_card_v2",
		Version:  "2.0",
		Data:     d,
		cardData: d,
	}, "", "  ")
}
