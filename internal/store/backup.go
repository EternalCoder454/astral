package store

import (
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// backupName matches a daily copy: compressed since 0.5.25, plain before.
var backupName = regexp.MustCompile(`^astral-\d{4}-\d{2}-\d{2}\.db(\.gz)?$`)

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
	// Compressed: a library is prose, which gzip takes to about a third, and
	// seven days of copies were seven whole libraries.
	plain := filepath.Join(dir, "astral-"+now.Format("2006-01-02")+".db")
	dst := plain + ".gz"
	for _, p := range []string{dst, plain} {
		if _, err := os.Stat(p); err == nil {
			return "", nil
		}
	}
	tmp := plain + ".partial"
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
	err = gzipFile(tmp, dst+".partial")
	_ = os.Remove(tmp)
	if err != nil {
		_ = os.Remove(dst + ".partial")
		return "", err
	}
	if err := os.Rename(dst+".partial", dst); err != nil {
		_ = os.Remove(dst + ".partial")
		return "", err
	}
	pruneBackups(dir, keepBackups)
	return dst, nil
}

// gzipFile writes src compressed to dst.
func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// gunzipFile writes src decompressed to dst.
func gunzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("the backup is damaged: %w", err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, zr); err != nil {
		out.Close()
		return fmt.Errorf("the backup is damaged: %w", err)
	}
	return out.Close()
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

// Backup is one of the daily copies.
type Backup struct {
	Path string
	Day  time.Time
	Size int64
}

// Backups lists the daily copies in dir, newest first.
func Backups(dir string) []Backup {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Backup
	for _, e := range entries {
		if e.IsDir() || !backupName.MatchString(e.Name()) {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", e.Name()[len("astral-"):len("astral-")+10], time.Local)
		if err != nil {
			continue
		}
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		out = append(out, Backup{Path: filepath.Join(dir, e.Name()), Day: day, Size: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.After(out[j].Day) })
	return out
}

// RestoreBackup puts a daily copy back as the library at dbPath. It must run
// with the library closed. The library being replaced is not deleted: it is
// moved aside with the time in its name, next to where it was, so a restore
// chosen by mistake can itself be undone. The copy is checked before anything
// is moved, so a damaged one leaves the library as it was.
func RestoreBackup(dbPath, backupPath string, now time.Time) (keptAs string, err error) {
	// A compressed copy is unpacked beside the library first, and it is that
	// unpacked file which is checked and then put in place.
	unpacked := dbPath + ".unpacked"
	_ = os.Remove(unpacked)
	defer os.Remove(unpacked)
	source := backupPath
	if strings.HasSuffix(backupPath, ".gz") {
		if err := gunzipFile(backupPath, unpacked); err != nil {
			return "", err
		}
		source = unpacked
	}
	check, err := sql.Open("sqlite", dsnFor(source)+"&mode=ro")
	if err != nil {
		return "", err
	}
	var verdict string
	err = check.QueryRow(`PRAGMA quick_check`).Scan(&verdict)
	check.Close()
	if err != nil {
		return "", fmt.Errorf("the backup could not be read: %w", err)
	}
	if verdict != "ok" {
		return "", fmt.Errorf("the backup is damaged: %s", verdict)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	keptAs = dbPath + ".before-restore-" + now.Format("2006-01-02-150405")
	if _, err := os.Stat(dbPath); err == nil {
		if err := os.Rename(dbPath, keptAs); err != nil {
			return "", err
		}
		// The write-ahead log and its index belong to the library just moved
		// aside, and left in place they would be read into the restored one.
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(dbPath + suffix); err == nil {
				_ = os.Rename(dbPath+suffix, keptAs+suffix)
			}
		}
	} else {
		keptAs = ""
	}
	tmp := dbPath + ".restoring"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return keptAs, err
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		return keptAs, err
	}
	return keptAs, nil
}
