package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Daily copies of the library.
//
// The database is the one thing nothing can rebuild: every transcript is in
// it and nowhere else. A damaged file is moved aside at startup rather than
// deleted, but that only helps if the damage is noticed, and it does nothing
// for a scene deleted by mistake last Tuesday. So once a day a copy is made,
// and the last week of them are kept.

// keepBackups is how many daily copies are kept.
const keepBackups = 7

// BackupDir is where the daily copies are kept.
func BackupDir() string { return filepath.Join(dataDir(), "backups") }

var backupName = regexp.MustCompile(`^astral-\d{4}-\d{2}-\d{2}\.db$`)

// BackupDaily writes today's copy if there is not one yet, and removes the
// oldest beyond the week. It returns the path it wrote, or "" when today's copy
// already existed.
//
// VACUUM INTO writes a clean, compacted copy that is consistent even while the
// app is writing, which a file copy of a database in WAL mode is not. It runs
// on a connection of its own, so the app's single connection is never held up
// behind it. It is written under a temporary name and renamed, so a copy cut
// short by a crash is never mistaken for a good one.
func (s *Store) BackupDaily(dir string, now time.Time) (string, error) {
	if s == nil || s.path == "" {
		return "", fmt.Errorf("no database to back up")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "astral-"+now.Format("2006-01-02")+".db")
	if _, err := os.Stat(dst); err == nil {
		return "", nil
	}
	tmp := dst + ".partial"
	_ = os.Remove(tmp)

	db, err := sql.Open("sqlite", dsnFor(s.path))
	if err != nil {
		return "", err
	}
	_, err = db.Exec(`VACUUM INTO ?`, tmp)
	db.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	pruneBackups(dir, keepBackups)
	return dst, nil
}

// pruneBackups removes the oldest daily copies past keep. The names sort by
// date, so the oldest are the first. Nothing but Astral's own copies is
// touched, whatever else is in the folder.
func pruneBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && backupName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
