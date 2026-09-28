package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"astral/internal/chars"
	"astral/internal/imageconv"
	"astral/internal/store"
)

// Characters' pictures on the phone's lists.
//
// A row showed a letter where the window shows a face. The picture is sent as
// a small square made once and kept: an avatar is drawn at 34 pixels, and the
// file behind it is often a full portrait of a megabyte or more.

// avatarSize is the square sent, enough for a phone's density at 34 points.
const avatarSize = 128

// avatars keeps each thumbnail made, by the file and its modification time, so
// a changed picture is made again and an unchanged one never is.
var avatars sync.Map

// pictureOf is the file a character's avatar is made from: their avatar, or
// their portrait when they have only that.
func pictureOf(c chars.Character) string {
	for _, p := range []string{c.AvatarPath, c.PortraitPath} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request, d store.Device) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a character id"})
		return
	}
	c, err := s.store.Character(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such character"})
		return
	}
	path := pictureOf(c)
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no picture"})
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no picture"})
		return
	}
	key := fmt.Sprintf("%s|%d", path, info.ModTime().UnixNano())
	thumb, ok := avatars.Load(key)
	if !ok {
		data, err := os.ReadFile(path)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no picture"})
			return
		}
		made, err := imageconv.Thumbnail(data, avatarSize)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "the picture could not be read"})
			return
		}
		thumb = made
		avatars.Store(key, made)
	}
	body := thumb.([]byte)
	sum := sha256.Sum256(body)
	tag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if strings.Contains(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(body))
	w.Write(body)
}
