package store

import (
	"os"
	"path/filepath"
	"strings"
)

// SaveAvatar copies an imported card's image into the data directory.
func SaveAvatar(name string, data []byte) (string, error) {
	if err := os.MkdirAll(AvatarDir(), 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(AvatarDir(), SafeFileName(name)+".png")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// SafeFileName reduces a character's name to something safe to write to disk.
// A card is a downloaded file and its name is attacker-controlled, so this
// keeps only characters that cannot traverse or escape a directory, rather
// than trying to escape the ones that can.
func SafeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "character"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
