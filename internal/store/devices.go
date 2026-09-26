package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// A device is a phone, or anything else, let in to this machine's Astral over
// the network.
//
// Only the token's SHA-256 is stored, so the database holds nothing replayable
// against the server. A plain hash is right because the tokens are 32 random
// bytes: nothing to brute force, and a slow hash would only slow every
// request.
type Device struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	LastSeen  time.Time
}

// HashToken is the one place a token becomes what is stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// AddDevice records a paired device and returns it.
func (s *Store) AddDevice(name, token string) (Device, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "A phone"
	}
	if token == "" {
		return Device{}, fmt.Errorf("a device needs a token")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now()
	res, err := s.db.Exec(`
		INSERT INTO devices (name, token_hash, created_at, last_seen) VALUES (?,?,?,?)`,
		name, HashToken(token), unix(now), unix(now))
	if err != nil {
		return Device{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Device{}, err
	}
	return Device{ID: id, Name: name, CreatedAt: now, LastSeen: now}, nil
}

// DeviceByToken finds the device a token belongs to, and records that it was
// used. A miss is not an error: it is simply not a device.
func (s *Store) DeviceByToken(token string) (Device, bool) {
	if token == "" {
		return Device{}, false
	}
	var d Device
	var created, seen int64
	err := s.db.QueryRow(`
		SELECT id, name, created_at, last_seen FROM devices WHERE token_hash = ?`,
		HashToken(token)).Scan(&d.ID, &d.Name, &created, &seen)
	if err != nil {
		return Device{}, false
	}
	d.CreatedAt, d.LastSeen = fromUnix(created), fromUnix(seen)
	return d, true
}

// TouchDevice records that a device was seen. Deliberately not done on every
// request: this is for the list in settings, not an audit log, and a write per
// request would serialise behind the same mutex every other write uses.
func (s *Store) TouchDevice(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE devices SET last_seen = ? WHERE id = ?`, unix(time.Now()), id)
	return err
}

// Devices lists what has been paired, most recently seen first.
func (s *Store) Devices() ([]Device, error) {
	rows, err := s.db.Query(`
		SELECT id, name, created_at, last_seen FROM devices ORDER BY last_seen DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var created, seen int64
		if err := rows.Scan(&d.ID, &d.Name, &created, &seen); err != nil {
			return nil, err
		}
		d.CreatedAt, d.LastSeen = fromUnix(created), fromUnix(seen)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDevice revokes a device. The next request it makes is refused.
func (s *Store) DeleteDevice(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM devices WHERE id = ?`, id)
	return err
}
