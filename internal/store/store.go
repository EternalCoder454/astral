package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no cgo)
)

// Store is Astral's database: characters, chats and messages.
//
// Unlike a search index, this database *is* the data — it holds your
// transcripts, and nothing can rebuild it from elsewhere. That single fact
// drives two choices below: synchronous=FULL rather than NORMAL, and a
// corrupt file being quarantined and reported rather than quietly replaced.
type Store struct {
	db   *sql.DB
	path string

	// writeMu serializes mutations so a write and the timestamp bump that
	// follows it cannot interleave with another writer's pair.
	writeMu sync.Mutex
}

// dsnFor builds the connection string. The pragmas are query parameters
// because modernc's driver applies them per connection that way, which is the
// only place they reliably take effect.
func dsnFor(path string) string {
	return path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(wal)" +
		// FULL, not NORMAL: an index can be rebuilt after a power cut, a
		// roleplay transcript cannot. The cost is an fsync per commit, which
		// is invisible next to the seconds a model spends generating.
		"&_pragma=synchronous(full)" +
		"&_pragma=foreign_keys(on)" +
		"&_pragma=cache_size(-8000)" + // negative = KiB, so 8 MiB
		"&_pragma=temp_store(memory)"
}

// Open prepares the data directory and opens the database, migrating it to the
// current schema. A file too damaged to migrate is moved aside and a fresh one
// is created; the returned bool reports that, so the UI can tell you rather
// than leaving you to discover your history is gone.
func Open(path string) (*Store, bool, error) {
	if path == "" {
		path = DefaultDBPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(AvatarDir(), 0o755); err != nil {
		return nil, false, err
	}

	db, err := openDB(path)
	if err != nil {
		return nil, false, err
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err == nil {
		return s, false, nil
	}

	// Migration failed, which in practice means the file is not a usable
	// database. Move it aside and start clean rather than refusing to launch.
	db.Close()
	if err := quarantine(path); err != nil {
		return nil, false, fmt.Errorf("database is damaged and could not be moved aside: %w", err)
	}
	db, err = openDB(path)
	if err != nil {
		return nil, true, err
	}
	s = &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		return nil, true, err
	}
	return s, true, nil
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		return nil, err
	}
	// SQLite serializes writers anyway; one connection turns a possible
	// "database is locked" into database/sql's own queue, which is what makes
	// the Store safe to call from the GTK thread and a goroutine at once.
	db.SetMaxOpenConns(1)
	return db, nil
}

// quarantine renames a damaged database out of the way, taking its WAL and
// shared-memory files with it so the fresh database does not inherit them.
func quarantine(path string) error {
	stamp := time.Now().Format("20060102-150405")
	if err := os.Rename(path, path+".broken-"+stamp); err != nil && !os.IsNotExist(err) {
		return err
	}
	os.Remove(path + "-wal")
	os.Remove(path + "-shm")
	return nil
}

// Close flushes and closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

const schema = `
CREATE TABLE IF NOT EXISTS characters (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	name          TEXT    NOT NULL,
	description   TEXT    NOT NULL DEFAULT '',
	personality   TEXT    NOT NULL DEFAULT '',
	scenario      TEXT    NOT NULL DEFAULT '',
	first_mes     TEXT    NOT NULL DEFAULT '',
	mes_example   TEXT    NOT NULL DEFAULT '',
	instructions  TEXT    NOT NULL DEFAULT '',
	alt_greetings TEXT    NOT NULL DEFAULT '',
	creator       TEXT    NOT NULL DEFAULT '',
	notes         TEXT    NOT NULL DEFAULT '',
	version       TEXT    NOT NULL DEFAULT '',
	tags          TEXT    NOT NULL DEFAULT '',
	avatar_path   TEXT    NOT NULL DEFAULT '',
	portrait_path TEXT    NOT NULL DEFAULT '',
	world_id      INTEGER NOT NULL DEFAULT 0,
	accent        INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL DEFAULT 0,
	updated_at    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS worlds (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT    NOT NULL,
	description TEXT    NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL DEFAULT 0,
	updated_at  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS lore_entries (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	world_id   INTEGER NOT NULL REFERENCES worlds(id) ON DELETE CASCADE,
	name       TEXT    NOT NULL,
	"keys"     TEXT    NOT NULL DEFAULT '',
	content    TEXT    NOT NULL DEFAULT '',
	enabled    INTEGER NOT NULL DEFAULT 1,
	constant   INTEGER NOT NULL DEFAULT 0,
	auto       INTEGER NOT NULL DEFAULT 0,
	priority   INTEGER NOT NULL DEFAULT 0,
	confidence REAL    NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_lore_world ON lore_entries(world_id);
-- Identity is (world, name): it is what the model has when it learns something
-- more about a subject, and it is what turns re-learning into an update
-- rather than a duplicate.
CREATE UNIQUE INDEX IF NOT EXISTS idx_lore_world_name ON lore_entries(world_id, name);

CREATE TABLE IF NOT EXISTS chats (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	character_id INTEGER NOT NULL DEFAULT 0,
	title        TEXT    NOT NULL DEFAULT '',
	model        TEXT    NOT NULL DEFAULT '',
	kind         TEXT    NOT NULL DEFAULT 'roleplay',
	summary      TEXT    NOT NULL DEFAULT '',
	summary_upto INTEGER NOT NULL DEFAULT 0,
	lore_upto    INTEGER NOT NULL DEFAULT 0,
	style_name   TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL DEFAULT 0,
	updated_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_chats_updated ON chats(updated_at DESC);

CREATE TABLE IF NOT EXISTS messages (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id     INTEGER NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
	role        TEXT    NOT NULL,
	content     TEXT    NOT NULL DEFAULT '',
	thinking    TEXT    NOT NULL DEFAULT '',
	eval_count  INTEGER NOT NULL DEFAULT 0,
	tok_per_sec REAL    NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_messages_chat ON messages(chat_id, id);
`

// migrate creates the schema and applies later additions.
//
// There is no version table. Each later change is a bare ALTER TABLE whose
// error is ignored, because the only error it can raise is "duplicate column"
// — which is precisely the signal that the migration already ran.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Added after the first release. A database made before this has every
	// chat defaulting to roleplay, which is what those chats were.
	s.db.Exec(`ALTER TABLE chats ADD COLUMN kind TEXT NOT NULL DEFAULT 'roleplay'`)

	// system_prompt and post_history merged into one instructions field. On a
	// database that predates the merge the ALTER succeeds and the backfill
	// joins the two old columns; on a fresh one the ALTER fails (the column is
	// already in the schema above) and the backfill fails too, because those
	// columns no longer exist. Both failures are the correct outcome, which is
	// why both errors are ignored.
	s.db.Exec(`ALTER TABLE characters ADD COLUMN instructions TEXT NOT NULL DEFAULT ''`)
	// How far the lorebook has been taught from this chat.
	s.db.Exec(`ALTER TABLE chats ADD COLUMN lore_upto INTEGER NOT NULL DEFAULT 0`)

	// How sure the model was, on entries it wrote itself.
	s.db.Exec(`ALTER TABLE lore_entries ADD COLUMN confidence REAL NOT NULL DEFAULT 0`)

	// Which world a character belongs to, added with lorebooks.
	s.db.Exec(`ALTER TABLE characters ADD COLUMN world_id INTEGER NOT NULL DEFAULT 0`)

	// The larger image shown beside a scene, added after avatars.
	s.db.Exec(`ALTER TABLE characters ADD COLUMN portrait_path TEXT NOT NULL DEFAULT ''`)

	// The running recap of a long scene, and the last message it covers.
	s.db.Exec(`ALTER TABLE chats ADD COLUMN summary TEXT NOT NULL DEFAULT ''`)
	s.db.Exec(`ALTER TABLE chats ADD COLUMN summary_upto INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`ALTER TABLE chats ADD COLUMN style_name TEXT NOT NULL DEFAULT ''`)

	s.db.Exec(`
		UPDATE characters SET instructions = TRIM(
			COALESCE(system_prompt, '') ||
			CASE WHEN TRIM(COALESCE(system_prompt, '')) <> ''
			      AND TRIM(COALESCE(post_history, '')) <> ''
			     THEN char(10) || char(10) ELSE '' END ||
			COALESCE(post_history, ''))
		WHERE TRIM(instructions) = ''`)
	return nil
}

// unix converts a time to the int64 the schema stores, mapping the zero time
// to 0 rather than to a negative epoch offset.
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// fromUnix is the inverse, mapping 0 back to the zero time.
func fromUnix(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}
