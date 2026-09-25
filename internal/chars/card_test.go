package chars

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const v2Card = `{
  "spec": "chara_card_v2",
  "spec_version": "2.0",
  "data": {
    "name": "Vesper Quill",
    "description": "A cartographer of places that have not happened yet.",
    "personality": "wry, guarded, precise",
    "scenario": "The map room, past midnight.",
    "first_mes": "*She does not look up.* \"You're late.\"",
    "mes_example": "<START>\n{{user}}: Hello.\n{{char}}: *A pin goes into the table.* \"Is it.\"",
    "creator": "someone",
    "character_version": "1.2",
    "tags": ["fantasy", "mystery", "fantasy"]
  }
}`

const v1Card = `{
  "name": "Flat Card",
  "description": "No envelope here.",
  "first_mes": "Hi."
}`

func TestParseCardV2(t *testing.T) {
	c, err := ParseCard([]byte(v2Card))
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	if c.Name != "Vesper Quill" {
		t.Errorf("Name = %q", c.Name)
	}
	if c.Personality != "wry, guarded, precise" {
		t.Errorf("Personality = %q", c.Personality)
	}
	if c.Version != "1.2" || c.Creator != "someone" {
		t.Errorf("metadata not carried: version=%q creator=%q", c.Version, c.Creator)
	}
	// Duplicate tags are dropped, and order is kept.
	if got := strings.Join(c.Tags, ","); got != "fantasy,mystery" {
		t.Errorf("Tags = %q, want deduplicated", got)
	}
}

// A V1 card has no "data" envelope. Cards this old are still in circulation,
// so falling back to the top-level fields is not optional.
func TestParseCardV1Flat(t *testing.T) {
	c, err := ParseCard([]byte(v1Card))
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	if c.Name != "Flat Card" || c.Description != "No envelope here." {
		t.Errorf("V1 fallback failed: %+v", c)
	}
}

func TestParseCardRejectsJunk(t *testing.T) {
	for _, in := range []string{"", "not json", "{}", `{"data":{}}`, "[]"} {
		if _, err := ParseCard([]byte(in)); err == nil {
			t.Errorf("ParseCard(%q) accepted a card with no name", in)
		}
	}
}

func TestRoundTripExport(t *testing.T) {
	c, err := ParseCard([]byte(v2Card))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ExportCard(c)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseCard(out)
	if err != nil {
		t.Fatalf("re-parsing our own export failed: %v", err)
	}
	if back.Name != c.Name || back.FirstMes != c.FirstMes || back.MesExample != c.MesExample {
		t.Errorf("round trip lost content:\n got %+v\nwant %+v", back, c)
	}
}

// --- PNG cards ---

// pngWithText builds a minimal but structurally valid PNG carrying the given
// text chunks. Real cards are ordinary images with the card in their metadata;
// this is the smallest thing with the same shape.
func pngWithText(chunks map[string]string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})

	write := func(typ string, data []byte) {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(data)))
		b.Write(length[:])
		payload := append([]byte(typ), data...)
		b.Write(payload)
		var crc [4]byte
		binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(payload))
		b.Write(crc[:])
	}

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 1) // width
	binary.BigEndian.PutUint32(ihdr[4:], 1) // height
	ihdr[8] = 8                             // bit depth
	ihdr[9] = 6                             // colour type: RGBA
	write("IHDR", ihdr)

	for k, v := range chunks {
		write("tEXt", append(append([]byte(k), 0), []byte(v)...))
	}
	write("IEND", nil)
	return b.Bytes()
}

func TestImportPNGCardV2(t *testing.T) {
	png := pngWithText(map[string]string{
		"chara": base64.StdEncoding.EncodeToString([]byte(v2Card)),
	})
	path := filepath.Join(t.TempDir(), "vesper.png")
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatal(err)
	}
	c, avatar, err := ImportFile(path)
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if c.Name != "Vesper Quill" {
		t.Errorf("Name = %q", c.Name)
	}
	// The image comes back too, so importing a card also gets the portrait.
	if !bytes.Equal(avatar, png) {
		t.Error("avatar bytes were not returned")
	}
}

// A card carrying both chunks is a V2 file upgraded in place; the V3 chunk is
// the newer of the two and must win.
func TestImportPNGPrefersV3(t *testing.T) {
	v3 := strings.Replace(v2Card, "Vesper Quill", "V3 Name", 1)
	png := pngWithText(map[string]string{
		"chara": base64.StdEncoding.EncodeToString([]byte(v2Card)),
		"ccv3":  base64.StdEncoding.EncodeToString([]byte(v3)),
	})
	c, err := importPNG(png)
	if err != nil {
		t.Fatalf("importPNG: %v", err)
	}
	if c.Name != "V3 Name" {
		t.Errorf("Name = %q, want the V3 chunk to win", c.Name)
	}
}

// Some exporters store the JSON unencoded rather than as base64.
func TestImportPNGPlainJSONPayload(t *testing.T) {
	png := pngWithText(map[string]string{"chara": v2Card})
	c, err := importPNG(png)
	if err != nil {
		t.Fatalf("importPNG: %v", err)
	}
	if c.Name != "Vesper Quill" {
		t.Errorf("Name = %q", c.Name)
	}
}

func TestImportPNGWithoutCard(t *testing.T) {
	png := pngWithText(map[string]string{"Comment": "just a picture"})
	if _, err := importPNG(png); err == nil {
		t.Error("a PNG with no card was accepted")
	}
}

// The chunk walker parses an untrusted, possibly hostile file. A truncated or
// lying length must stop the walk, not index past the buffer.
func TestPNGTextSurvivesMalformedInput(t *testing.T) {
	good := pngWithText(map[string]string{"chara": "x"})
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"magic only", good[:8]},
		{"truncated mid-chunk", good[:len(good)-9]},
		{"not a png", []byte("hello there, not a png at all")},
		{"lying length", func() []byte {
			b := append([]byte(nil), good...)
			binary.BigEndian.PutUint32(b[8:12], 0xFFFFFFF0) // absurd chunk length
			return b
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The requirement is simply that it returns rather than panicking
			// or reading out of bounds.
			_, _ = pngText(tc.data)
		})
	}
}

func TestImportFileSniffsPNGWithWrongExtension(t *testing.T) {
	png := pngWithText(map[string]string{
		"chara": base64.StdEncoding.EncodeToString([]byte(v2Card)),
	})
	path := filepath.Join(t.TempDir(), "downloaded.bin")
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatal(err)
	}
	c, _, err := ImportFile(path)
	if err != nil {
		t.Fatalf("ImportFile on a mis-named PNG: %v", err)
	}
	if c.Name != "Vesper Quill" {
		t.Errorf("Name = %q", c.Name)
	}
}
