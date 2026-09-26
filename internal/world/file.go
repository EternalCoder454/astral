package world

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A world in a file, so it can be backed up, moved to another machine, or given
// to somebody.
//
// Characters have had this since the beginning, because the character card is a
// format other applications already speak. A world had nothing: it lived in one
// SQLite file alongside every other world, and the only way to keep one was to
// keep the whole database. A setting someone spends weeks on should be something
// they own a copy of.
//
// There is no existing format to be compatible with, so this is Astral's own and
// says so in the file. It is plain JSON with a version on it, because the first
// thing a format needs is the ability to be read by the version that comes after
// the one that wrote it.

// fileFormat identifies the file, so a JSON document that happens to have a name
// and a description is not mistaken for a world.
const fileFormat = "astral-world"

// fileVersion is the shape below. A reader accepts anything at or under its own
// version and refuses what is above it, which is the honest answer: a file from a
// later Astral may contain fields this one would silently drop.
const fileVersion = 1

// File is a world and its lorebook, as written to disk.
type File struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	// Astral is the version that wrote it, for a person reading the file rather
	// than for the parser. Nothing branches on it.
	Astral string `json:"astral,omitempty"`

	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Rules       string      `json:"rules,omitempty"`
	Entries     []FileEntry `json:"entries"`
}

// FileEntry is one lore entry. Ids, timestamps and the world it belonged to are
// deliberately absent: they are facts about one database, not about the world.
type FileEntry struct {
	Name     string   `json:"name"`
	Keys     []string `json:"keys"`
	Content  string   `json:"content"`
	Enabled  bool     `json:"enabled"`
	Constant bool     `json:"constant,omitempty"`
	Priority int      `json:"priority,omitempty"`
	// Auto records that the model wrote this rather than a person, which is worth
	// carrying: it tells whoever receives the file which parts were observed from
	// play and which were written deliberately.
	Auto bool `json:"auto,omitempty"`
}

// Encode writes a world and its entries.
func Encode(w World, entries []Entry, astralVersion string) ([]byte, error) {
	f := File{
		Format:      fileFormat,
		Version:     fileVersion,
		Astral:      astralVersion,
		Name:        strings.TrimSpace(w.Name),
		Description: strings.TrimSpace(w.Description),
		Rules:       strings.TrimSpace(w.Rules),
	}
	if f.Name == "" {
		return nil, fmt.Errorf("a world needs a name to be exported")
	}
	for _, e := range entries {
		if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Content) == "" {
			continue
		}
		f.Entries = append(f.Entries, FileEntry{
			Name:     strings.TrimSpace(e.Name),
			Keys:     e.Keys,
			Content:  strings.TrimSpace(e.Content),
			Enabled:  e.Enabled,
			Constant: e.Constant,
			Priority: e.Priority,
			Auto:     e.Auto,
		})
	}
	// Indented, because a person will open this in a text editor sooner or later
	// and a world is worth being able to read and hand-edit.
	return json.MarshalIndent(f, "", "  ")
}

// Decode reads a world file into something the store can save.
//
// Everything a file says about a lore entry is checked rather than trusted. A
// world file is a document from outside this machine, so its entries go through
// the same key cleaning as one the model just wrote, and an entry nothing could
// trigger is dropped instead of being stored looking functional.
func Decode(data []byte) (Draft, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return Draft{}, fmt.Errorf("that file is not a world Astral can read: %w", err)
	}
	if f.Format != "" && f.Format != fileFormat {
		return Draft{}, fmt.Errorf("that file says it is %q, not a world", f.Format)
	}
	if f.Version > fileVersion {
		return Draft{}, fmt.Errorf("that world was written by a newer Astral (format %d, this one reads %d). Update and try again",
			f.Version, fileVersion)
	}
	name := strings.TrimSpace(f.Name)
	if name == "" {
		return Draft{}, fmt.Errorf("that file has no world name in it")
	}

	d := Draft{World: World{
		Name:        name,
		Description: strings.TrimSpace(f.Description),
		Rules:       strings.TrimSpace(f.Rules),
	}}
	seen := make(map[string]bool, len(f.Entries))
	for _, e := range f.Entries {
		n := strings.TrimSpace(e.Name)
		content := strings.TrimSpace(e.Content)
		if n == "" || content == "" {
			continue
		}
		if low := strings.ToLower(n); seen[low] {
			continue
		} else {
			seen[low] = true
		}
		keys := cleanKeys(append(e.Keys, n), nil)
		if len(keys) == 0 {
			continue
		}
		d.Entries = append(d.Entries, Entry{
			Name:     n,
			Keys:     keys,
			Content:  content,
			Enabled:  e.Enabled || e.Constant,
			Constant: e.Constant,
			Priority: e.Priority,
			Auto:     e.Auto,
		})
	}
	return d, nil
}

// Filename is a safe file name for a world, without its extension.
func Filename(w World) string {
	name := strings.TrimSpace(w.Name)
	if name == "" {
		name = "world"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if out == "" {
		return "world"
	}
	return out
}
