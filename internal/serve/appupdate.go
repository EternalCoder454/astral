package serve

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"astral/internal/store"
	"astral/internal/update"
)

// Updating the phone app. Android will not update a sideloaded app by itself,
// so the app finds out whether there is a newer one, fetches it, and hands it
// to the system installer.
//
// The fetch goes through the PC: the phone may be on a network with no way
// out, and it already trusts exactly one machine. What makes that safe is not
// the transport — Android refuses a build signed with a different key from the
// one installed, so a tampered file is rejected by the installer.

// apkTimeout bounds the download. A few megabytes from GitHub, so this is
// generous rather than tight.
const apkTimeout = 3 * time.Minute

// releaseAPK is where a published build lives.
const releaseAPK = "https://github.com/EternalCoder454/astral/releases/download/v%s/astral-%s.apk"

// handleAppLatest says whether there is a newer app than the one asking, and
// what changed in it.
//
// The version comes from the phone rather than from here, because the PC and
// the phone update separately and the PC's own version says nothing about what
// is installed on a phone.
func (s *Server) handleAppLatest(w http.ResponseWriter, r *http.Request, d store.Device) {
	have := strings.TrimSpace(r.URL.Query().Get("have"))
	if have == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no version given"})
		return
	}
	cfg := s.config()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	rel, err := update.New().Check(ctx, cfg.UpdateChannel, have)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if rel == nil {
		writeJSON(w, http.StatusOK, map[string]any{"current": true, "have": have})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current": false,
		"have":    have,
		"version": rel.Version,
		"notes":   rel.Notes,
	})
}

// handleAppDownload fetches the published app and passes it through.
//
// Streamed rather than buffered: there is no reason for a few megabytes to sit
// in this machine's memory on the way past.
func (s *Server) handleAppDownload(w http.ResponseWriter, r *http.Request, d store.Device) {
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	if !plausibleVersion(version) {
		// The version goes into a URL, so it is checked rather than trusted.
		// Nothing here should be able to make this machine fetch an address of
		// somebody else's choosing.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a version"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), apkTimeout)
	defer cancel()

	url := fmt.Sprintf(releaseAPK, version, version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp, err := (&http.Client{Timeout: apkTimeout}).Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach the download: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway,
			map[string]string{"error": fmt.Sprintf("the download answered %d", resp.StatusCode)})
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="astral-`+version+`.apk"`)
	if n := resp.Header.Get("Content-Length"); n != "" {
		w.Header().Set("Content-Length", n)
	}
	io.Copy(w, resp.Body)
}

// plausibleVersion accepts three dot-separated numbers and nothing else.
func plausibleVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 4 {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
