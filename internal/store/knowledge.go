package store

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// The knowledge base: notes, saved pages and saved answers that a conversation
// can draw on without being told to.
//
// A local model knows what its weights knew, and a search only helps with what
// is on the web. Neither holds what you have worked out yourself, or the page
// you read last week and would rather not search for again. Entries here are
// found by what a conversation is about, not by name, and the relevant parts
// are put in front of the model when it answers.
//
// An entry is split into chunks of a few paragraphs, because a model given one
// relevant paragraph answers better than one given a whole document around it,
// and because a long entry would otherwise crowd everything else out of the
// window. Chunks are indexed for full-text search, which works on every
// machine; when an embedding model is installed they also carry a vector, and
// search blends the two.

// Knowledge origins: how an entry came to exist.
const (
	OriginWritten = "written" // typed in by hand
	OriginWeb     = "web"     // a page a search opened
	OriginChat    = "chat"    // an answer saved from a conversation
	OriginFile    = "file"    // imported from a text file
	OriginStudy   = "study"   // notes written by the model on a subject it was asked to study
)

// KnowledgeEntry is one entry.
type KnowledgeEntry struct {
	ID     int64
	Title  string
	Body   string
	Source string // an address, "chat 12", or empty for something written here
	Tags   []string
	Origin string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// KnowledgeHit is one chunk that matched a search.
type KnowledgeHit struct {
	EntryID int64
	ChunkID int64
	Title   string
	Source  string
	Origin  string
	Text    string
	Updated time.Time
	// Rank is the position in the search it came from, best first. Fusing two
	// searches is done on rank rather than score, because a text search's score
	// and a vector's similarity are not on any common scale.
	Rank int
}

// ChunkChars is roughly how long a chunk is allowed to grow. A few paragraphs:
// long enough to carry a thought whole, short enough that four of them fit
// beside a conversation in an 8k window.
const ChunkChars = 900

func (s *Store) migrateKnowledge() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		title      TEXT    NOT NULL,
		body       TEXT    NOT NULL DEFAULT '',
		source     TEXT    NOT NULL DEFAULT '',
		tags       TEXT    NOT NULL DEFAULT '',
		origin     TEXT    NOT NULL DEFAULT 'written',
		created_at INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL DEFAULT 0
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_knowledge_updated ON knowledge(updated_at DESC)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_knowledge_source ON knowledge(source)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_chunks (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id     INTEGER NOT NULL REFERENCES knowledge(id) ON DELETE CASCADE,
		seq          INTEGER NOT NULL,
		text         TEXT    NOT NULL,
		vector       BLOB,
		vector_model TEXT    NOT NULL DEFAULT ''
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_entry ON knowledge_chunks(entry_id)`)
	// The full-text index, one row per chunk under the chunk's id. The title is
	// indexed with every chunk so a chunk that never repeats the subject's name
	// is still found by it.
	s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_fts USING fts5(
		title, text, tokenize = 'porter unicode61')`)
}

// SaveKnowledge writes an entry, new when its ID is zero, and re-indexes it.
func (s *Store) SaveKnowledge(e KnowledgeEntry) (int64, error) {
	e.Title = strings.TrimSpace(e.Title)
	e.Body = strings.TrimSpace(e.Body)
	if e.Title == "" {
		e.Title = firstLine(e.Body, 80)
	}
	if e.Title == "" {
		return 0, errors.New("an entry needs a title or some text")
	}
	if e.Origin == "" {
		e.Origin = OriginWritten
	}
	now := unix(time.Now())

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() // no-op after a successful Commit

	id := e.ID
	tags := strings.Join(cleanTags(e.Tags), ", ")
	if id == 0 {
		res, err := tx.Exec(`INSERT INTO knowledge (title, body, source, tags, origin, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?)`, e.Title, e.Body, strings.TrimSpace(e.Source), tags, e.Origin, now, now)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	} else {
		if _, err := tx.Exec(`UPDATE knowledge SET title = ?, body = ?, source = ?, tags = ?, origin = ?, updated_at = ?
			WHERE id = ?`, e.Title, e.Body, strings.TrimSpace(e.Source), tags, e.Origin, now, id); err != nil {
			return 0, err
		}
		if err := dropChunks(tx, id); err != nil {
			return 0, err
		}
	}
	for i, text := range ChunkText(e.Body) {
		res, err := tx.Exec(`INSERT INTO knowledge_chunks (entry_id, seq, text) VALUES (?,?,?)`, id, i, text)
		if err != nil {
			return 0, err
		}
		cid, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO knowledge_fts (rowid, title, text) VALUES (?,?,?)`, cid, e.Title, text); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// SaveKnowledgeBySource writes an entry that is identified by where it came
// from, replacing the one already saved from there. A page read twice is kept
// once, as it was the second time.
func (s *Store) SaveKnowledgeBySource(e KnowledgeEntry) (int64, error) {
	src := strings.TrimSpace(e.Source)
	if src != "" {
		var id int64
		err := s.db.QueryRow(`SELECT id FROM knowledge WHERE source = ? ORDER BY id LIMIT 1`, src).Scan(&id)
		if err == nil {
			e.ID = id
		} else if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	return s.SaveKnowledge(e)
}

// dropChunks removes an entry's chunks and their index rows. The index is a
// virtual table with no foreign key, so it has to be told.
func dropChunks(tx *sql.Tx, entryID int64) error {
	if _, err := tx.Exec(`DELETE FROM knowledge_fts WHERE rowid IN
		(SELECT id FROM knowledge_chunks WHERE entry_id = ?)`, entryID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM knowledge_chunks WHERE entry_id = ?`, entryID)
	return err
}

// DeleteKnowledge removes an entry.
func (s *Store) DeleteKnowledge(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := dropChunks(tx, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM knowledge WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

const knowledgeColumns = `id, title, body, source, tags, origin, created_at, updated_at`

func scanKnowledge(row interface{ Scan(...any) error }) (KnowledgeEntry, error) {
	var e KnowledgeEntry
	var tags string
	var created, updated int64
	if err := row.Scan(&e.ID, &e.Title, &e.Body, &e.Source, &tags, &e.Origin, &created, &updated); err != nil {
		return e, err
	}
	e.Tags = splitTags(tags)
	e.CreatedAt, e.UpdatedAt = fromUnix(created), fromUnix(updated)
	return e, nil
}

// Knowledge returns one entry.
func (s *Store) Knowledge(id int64) (KnowledgeEntry, error) {
	return scanKnowledge(s.db.QueryRow(`SELECT `+knowledgeColumns+` FROM knowledge WHERE id = ?`, id))
}

// KnowledgeEntries lists every entry, most recently changed first.
func (s *Store) KnowledgeEntries() ([]KnowledgeEntry, error) {
	rows, err := s.db.Query(`SELECT ` + knowledgeColumns + ` FROM knowledge ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeEntry
	for rows.Next() {
		e, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// KnowledgeCount is how many entries there are, for the sidebar and settings.
func (s *Store) KnowledgeCount() int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM knowledge`).Scan(&n)
	return n
}

// SearchKnowledgeText finds the chunks that match a piece of text, best first.
//
// The text is anything: a question, a message, a paragraph. It is reduced to
// its distinctive words and those are searched for with any of them matching,
// ranked by BM25 with the title counted four times over the body, since a
// chunk whose entry is named for the subject is almost always the one wanted.
func (s *Store) SearchKnowledgeText(text string, limit int) ([]KnowledgeHit, error) {
	q := FTSQuery(text)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT c.id, c.entry_id, k.title, k.source, k.origin, c.text, k.updated_at
		FROM knowledge_fts f
		JOIN knowledge_chunks c ON c.id = f.rowid
		JOIN knowledge k ON k.id = c.entry_id
		WHERE knowledge_fts MATCH ?
		ORDER BY bm25(knowledge_fts, 4.0, 1.0)
		LIMIT ?`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeHit
	for rows.Next() {
		var h KnowledgeHit
		var updated int64
		if err := rows.Scan(&h.ChunkID, &h.EntryID, &h.Title, &h.Source, &h.Origin, &h.Text, &updated); err != nil {
			return nil, err
		}
		h.Updated = fromUnix(updated)
		h.Rank = len(out)
		out = append(out, h)
	}
	return out, rows.Err()
}

// SearchKnowledgeVector finds the chunks whose vectors are closest to one,
// best first. Only chunks embedded with the same model are compared: vectors
// from two models live in unrelated spaces.
//
// A scan rather than an index. A knowledge base of a few thousand chunks is a
// few megabytes of vectors and a millisecond of arithmetic, and an index would
// be a dependency for a problem this size does not have.
func (s *Store) SearchKnowledgeVector(model string, vec []float32, limit int) ([]KnowledgeHit, error) {
	if len(vec) == 0 || model == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT c.id, c.entry_id, k.title, k.source, k.origin, c.text, k.updated_at, c.vector
		FROM knowledge_chunks c JOIN knowledge k ON k.id = c.entry_id
		WHERE c.vector_model = ? AND c.vector IS NOT NULL`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		hit KnowledgeHit
		sim float64
	}
	var all []scored
	for rows.Next() {
		var h KnowledgeHit
		var updated int64
		var blob []byte
		if err := rows.Scan(&h.ChunkID, &h.EntryID, &h.Title, &h.Source, &h.Origin, &h.Text, &updated, &blob); err != nil {
			return nil, err
		}
		h.Updated = fromUnix(updated)
		all = append(all, scored{h, cosine(vec, decodeVector(blob))})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].sim > all[j].sim })
	if limit <= 0 {
		limit = 10
	}
	var out []KnowledgeHit
	for i, sc := range all {
		if i == limit {
			break
		}
		sc.hit.Rank = i
		out = append(out, sc.hit)
	}
	return out, nil
}

// PendingChunk is a chunk waiting to be embedded.
type PendingChunk struct {
	ID    int64
	Title string
	Text  string
}

// ChunksWithoutVector returns chunks not yet embedded with this model.
func (s *Store) ChunksWithoutVector(model string, limit int) ([]PendingChunk, error) {
	rows, err := s.db.Query(`
		SELECT c.id, k.title, c.text FROM knowledge_chunks c JOIN knowledge k ON k.id = c.entry_id
		WHERE c.vector IS NULL OR c.vector_model <> ?
		ORDER BY c.id LIMIT ?`, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingChunk
	for rows.Next() {
		var p PendingChunk
		if err := rows.Scan(&p.ID, &p.Title, &p.Text); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetChunkVector stores a chunk's embedding.
func (s *Store) SetChunkVector(chunkID int64, model string, vec []float32) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE knowledge_chunks SET vector = ?, vector_model = ? WHERE id = ?`,
		encodeVector(vec), model, chunkID)
	return err
}

// encodeVector packs a vector as little-endian float32s: four bytes a
// dimension, which for a 768-dimension model is three kilobytes a chunk.
func encodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func decodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

// cosine is the similarity of two vectors, 1 for the same direction. Vectors of
// different lengths come from different models and are not similar at all.
func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return -1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return -1
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// ChunkText splits a body into chunks at paragraph boundaries, packing short
// paragraphs together and splitting long ones at sentences.
func ChunkText(body string) []string {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if body == "" {
		return nil
	}
	var pieces []string
	for _, para := range strings.Split(body, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if len(para) <= ChunkChars {
			pieces = append(pieces, para)
			continue
		}
		pieces = append(pieces, splitSentences(para, ChunkChars)...)
	}
	var out []string
	var cur strings.Builder
	for _, p := range pieces {
		if cur.Len() > 0 && cur.Len()+2+len(p) > ChunkChars {
			out = append(out, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// splitSentences breaks a long paragraph into pieces no longer than max,
// cutting after a sentence where it can and at a space where it cannot.
func splitSentences(para string, max int) []string {
	var out []string
	for len(para) > max {
		cut := -1
		for i := max; i > max/3; i-- {
			if (para[i-1] == '.' || para[i-1] == '?' || para[i-1] == '!') && para[i] == ' ' {
				cut = i
				break
			}
		}
		if cut < 0 {
			cut = strings.LastIndexByte(para[:max], ' ')
			if cut <= 0 {
				cut = max
			}
		}
		out = append(out, strings.TrimSpace(para[:cut]))
		para = strings.TrimSpace(para[cut:])
	}
	if para != "" {
		out = append(out, para)
	}
	return out
}

// FTSQuery turns free text into a full-text query: its distinctive words, each
// quoted, any of them matching.
//
// Quoted so nothing the text contains can be read as query syntax: a message
// with a colon, a hyphen or the word NOT in it must not become an operator, and
// a stray double quote must not end the string early.
func FTSQuery(text string) string {
	seen := map[string]bool{}
	var terms []string
	var w strings.Builder
	flush := func() {
		word := strings.ToLower(w.String())
		w.Reset()
		if len([]rune(word)) < 3 || ftsStop[word] || seen[word] {
			return
		}
		seen[word] = true
		terms = append(terms, `"`+word+`"`)
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			w.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	if len(terms) > 24 {
		// The words of a long message that are not in the list are mostly
		// the ones that would have matched everything anyway.
		terms = terms[:24]
	}
	return strings.Join(terms, " OR ")
}

// ftsStop are words that match nearly every chunk and so rank nothing.
var ftsStop = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`the and for are but not you your yours with this that these those from
		have has had was were been being what when where which who whom why how can could would should
		will shall may might must does did doing done its it's into onto about above after again against
		all any both each few more most other some such than too very just also then there here our ours
		out over under only own same she her his him they them their what's i'm i've let's tell know
		want like please thanks thank hello yes okay one two get got make made use used`) {
		m[w] = true
	}
	return m
}()

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len([]rune(s)) > max {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}

func cleanTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.TrimSpace(strings.ToLower(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func splitTags(s string) []string { return cleanTags(strings.Split(s, ",")) }
