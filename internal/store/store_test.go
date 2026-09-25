package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
)

// openTest gives each test its own database and XDG directories, so nothing
// here can touch the real one.
func openTest(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))

	s, recovered, err := Open(filepath.Join(dir, "astral.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if recovered {
		t.Fatal("a fresh database reported itself as recovered")
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := openTest(t)
	for i := 0; i < 3; i++ {
		if err := s.migrate(); err != nil {
			t.Fatalf("migrate run %d: %v", i, err)
		}
	}
}

// A file that is not a database at all must not stop the app launching: it is
// moved aside and a fresh one takes its place.
func TestOpenQuarantinesDamagedDatabase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	path := filepath.Join(dir, "astral.db")
	if err := os.WriteFile(path, []byte("this is not a database"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, recovered, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a damaged file: %v", err)
	}
	defer s.Close()
	if !recovered {
		t.Error("damage was not reported to the caller")
	}
	// The damaged file is kept rather than deleted — it is the user's data,
	// even when it is unreadable.
	matches, _ := filepath.Glob(path + ".broken-*")
	if len(matches) != 1 {
		t.Errorf("damaged file was not preserved (found %d)", len(matches))
	}
	if _, err := s.CountCharacters(); err != nil {
		t.Errorf("replacement database is not usable: %v", err)
	}
}

func TestCharacterRoundTrip(t *testing.T) {
	s := openTest(t)
	in := chars.Character{
		Name:         "Vesper",
		Description:  "A cartographer.",
		Personality:  "wry",
		FirstMes:     "\"You're late.\"",
		Tags:         []string{"fantasy", "mystery"},
		AltGreetings: []string{"alt one"},
		Accent:       3,
	}
	id, err := s.SaveCharacter(in)
	if err != nil {
		t.Fatalf("SaveCharacter: %v", err)
	}

	got, err := s.Character(id)
	if err != nil {
		t.Fatalf("Character: %v", err)
	}
	if got.Name != in.Name || got.FirstMes != in.FirstMes || got.Accent != in.Accent {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if strings.Join(got.Tags, ",") != "fantasy,mystery" {
		t.Errorf("Tags = %v", got.Tags)
	}
	if strings.Join(got.AltGreetings, ",") != "alt one" {
		t.Errorf("AltGreetings = %v", got.AltGreetings)
	}

	got.Name = "Vesper Quill"
	if _, err := s.SaveCharacter(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, _ := s.Character(id)
	if again.Name != "Vesper Quill" {
		t.Errorf("update did not stick: %q", again.Name)
	}
	if n, _ := s.CountCharacters(); n != 1 {
		t.Errorf("update inserted a second row (count=%d)", n)
	}
}

func TestSaveCharacterRequiresName(t *testing.T) {
	s := openTest(t)
	if _, err := s.SaveCharacter(chars.Character{Name: "  "}); err == nil {
		t.Error("a nameless character was accepted")
	}
}

func TestMessagesCascadeWithTheirChat(t *testing.T) {
	s := openTest(t)
	ch, err := s.NewChat(0, "Scene", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"one", "two", "three"} {
		if _, err := s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: text}); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := s.Messages(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 || msgs[0].Content != "one" || msgs[2].Content != "three" {
		t.Fatalf("messages not stored in order: %+v", msgs)
	}

	if err := s.DeleteChat(ch.ID); err != nil {
		t.Fatal(err)
	}
	// The foreign key's ON DELETE CASCADE only fires if foreign_keys=on
	// actually took effect, which is really what this asserts.
	left, _ := s.Messages(ch.ID)
	if len(left) != 0 {
		t.Errorf("deleting a chat left %d orphaned messages", len(left))
	}
}

func TestDeleteMessagesFromRewindsTheScene(t *testing.T) {
	s := openTest(t)
	ch, _ := s.NewChat(0, "Scene", "m", "")
	var ids []int64
	for _, text := range []string{"one", "two", "three", "four"} {
		id, _ := s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: text})
		ids = append(ids, id)
	}
	if err := s.DeleteMessagesFrom(ch.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Messages(ch.ID)
	if len(msgs) != 2 {
		t.Errorf("rewind left %d messages, want 2", len(msgs))
	}
}

// Deleting a character keeps the chats played with them: the transcript is the
// user's, and losing a scene because the cast was tidied is not a trade
// anyone would choose.
func TestDeletingCharacterKeepsChats(t *testing.T) {
	s := openTest(t)
	id, _ := s.SaveCharacter(chars.Character{Name: "Vesper"})
	ch, _ := s.NewChat(id, "Scene", "m", KindRoleplay)
	s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: "hi"})

	if err := s.DeleteCharacter(id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Chat(ch.ID)
	if err != nil {
		t.Fatalf("chat disappeared with its character: %v", err)
	}
	if got.CharacterName != "" {
		t.Errorf("CharacterName = %q, want empty for a deleted character", got.CharacterName)
	}
	if msgs, _ := s.Messages(ch.ID); len(msgs) != 1 {
		t.Error("transcript was lost")
	}
}

// The sidebar orders by updated_at, so a new message must bump it — in the
// same transaction, or a message can exist in a chat that sorts as untouched.
func TestAddMessageBumpsChatOrder(t *testing.T) {
	s := openTest(t)
	first, _ := s.NewChat(0, "older", "m", "")
	second, _ := s.NewChat(0, "newer", "m", "")

	if _, err := s.AddMessage(Message{
		ChatID: first.ID, Role: ollama.RoleUser, Content: "hi",
		CreatedAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	chats, err := s.Chats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 || chats[0].ID != first.ID {
		t.Errorf("chat order = %v, want the one just written to first", chats)
	}
	if chats[0].MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1", chats[0].MessageCount)
	}
	_ = second
}

func TestTitleFrom(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "New chat"},
		{"Hello there", "Hello there"},
		{"first line\nsecond line", "first line"},
		{"  padded  ", "padded"},
	}
	for _, c := range cases {
		if got := TitleFrom(c.in); got != c.want {
			t.Errorf("TitleFrom(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := TitleFrom(strings.Repeat("alpha ", 30))
	if len([]rune(long)) > 50 {
		t.Errorf("long title not truncated: %q", long)
	}
	if !strings.HasSuffix(long, "…") {
		t.Errorf("truncated title lacks an ellipsis: %q", long)
	}
}

func TestConfigRoundTripAndNormalize(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig on a fresh profile: %v", err)
	}
	if cfg.Theme != ThemeDark || cfg.Temperature != DefaultTemperature {
		t.Errorf("defaults not applied: %+v", cfg)
	}

	cfg.PersonaName = "Wren"
	cfg.Temperature = 99 // out of range
	cfg.WindowWidth = 10 // absurd
	cfg.Theme = "chartreuse"
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	back, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if back.PersonaName != "Wren" {
		t.Errorf("PersonaName lost: %q", back.PersonaName)
	}
	if back.Temperature != DefaultTemperature {
		t.Errorf("Temperature not clamped: %v", back.Temperature)
	}
	if back.WindowWidth < 640 {
		t.Errorf("WindowWidth not repaired: %d", back.WindowWidth)
	}
	if back.Theme != ThemeDark {
		t.Errorf("unknown theme not repaired: %q", back.Theme)
	}
}

// A config written by an older version is missing the newer keys; unmarshalling
// over a defaults struct is what makes them backfill instead of arriving zeroed.
func TestConfigBackfillsNewSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"model":"qwen3:8b","persona_name":"Wren"}`
	if err := os.WriteFile(ConfigPath(), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "qwen3:8b" || cfg.PersonaName != "Wren" {
		t.Errorf("stored values lost: %+v", cfg)
	}
	if cfg.NumCtx != DefaultNumCtx || cfg.Theme != ThemeDark || !cfg.ShowStats {
		t.Errorf("new settings did not backfill: %+v", cfg)
	}
}

// A database written before chat kinds existed has no `kind` column. The
// migration has to add it without touching the chats already in there — and
// those chats were all roleplay, which is what the column defaults to.
func TestMigrationAddsKindToAnOlderDatabase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	path := filepath.Join(dir, "astral.db")

	// Build the pre-kind schema by hand, and put a chat in it.
	old, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE chats (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			character_id INTEGER NOT NULL DEFAULT 0,
			title        TEXT    NOT NULL DEFAULT '',
			model        TEXT    NOT NULL DEFAULT '',
			created_at   INTEGER NOT NULL DEFAULT 0,
			updated_at   INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO chats (title, model, created_at, updated_at)
		VALUES ('An older scene', 'qwen3:8b', 1, 1);`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, recovered, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a pre-kind database: %v", err)
	}
	defer s.Close()
	if recovered {
		t.Fatal("a migratable database was quarantined instead of migrated")
	}

	chats, err := s.Chats()
	if err != nil {
		t.Fatalf("Chats after migration: %v", err)
	}
	if len(chats) != 1 {
		t.Fatalf("got %d chats, want the existing one preserved", len(chats))
	}
	if chats[0].Title != "An older scene" {
		t.Errorf("Title = %q, want the original", chats[0].Title)
	}
	if chats[0].Kind != KindRoleplay {
		t.Errorf("Kind = %q, want existing chats to default to roleplay", chats[0].Kind)
	}

	// And the migrated database still works for writes.
	if _, err := s.NewChat(0, "New designer chat", "m", KindDesigner); err != nil {
		t.Fatalf("writing to a migrated database: %v", err)
	}
}

func TestNewChatRecordsKind(t *testing.T) {
	s := openTest(t)
	for _, kind := range []string{KindRoleplay, KindAssistant, KindDesigner} {
		ch, err := s.NewChat(0, "t", "m", kind)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Chat(ch.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != kind {
			t.Errorf("Kind = %q, want %q", got.Kind, kind)
		}
	}
	// An empty kind is a roleplay chat, so old call sites cannot write a blank.
	ch, _ := s.NewChat(0, "t", "m", "")
	got, _ := s.Chat(ch.ID)
	if got.Kind != KindRoleplay {
		t.Errorf("empty kind stored as %q, want %q", got.Kind, KindRoleplay)
	}
}

// system_prompt and post_history were merged into one instructions field. A
// database written before that has the two old columns, and their contents
// must survive into the new one — losing someone's carefully written
// instructions to a schema change is not an acceptable upgrade.
func TestMigrationMergesInstructions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	path := filepath.Join(dir, "astral.db")

	old, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE characters (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT    NOT NULL,
			description   TEXT    NOT NULL DEFAULT '',
			personality   TEXT    NOT NULL DEFAULT '',
			scenario      TEXT    NOT NULL DEFAULT '',
			first_mes     TEXT    NOT NULL DEFAULT '',
			mes_example   TEXT    NOT NULL DEFAULT '',
			system_prompt TEXT    NOT NULL DEFAULT '',
			post_history  TEXT    NOT NULL DEFAULT '',
			alt_greetings TEXT    NOT NULL DEFAULT '',
			creator       TEXT    NOT NULL DEFAULT '',
			notes         TEXT    NOT NULL DEFAULT '',
			version       TEXT    NOT NULL DEFAULT '',
			tags          TEXT    NOT NULL DEFAULT '',
			avatar_path   TEXT    NOT NULL DEFAULT '',
			accent        INTEGER NOT NULL DEFAULT 0,
			created_at    INTEGER NOT NULL DEFAULT 0,
			updated_at    INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO characters (name, description, system_prompt, post_history)
			VALUES ('Both', 'd', 'front matter', 'trailing nudge');
		INSERT INTO characters (name, system_prompt) VALUES ('OnlySystem', 'just this');
		INSERT INTO characters (name, post_history) VALUES ('OnlyPost', 'just that');
		INSERT INTO characters (name) VALUES ('Neither');`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, recovered, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a pre-merge database: %v", err)
	}
	defer s.Close()
	if recovered {
		t.Fatal("a migratable database was quarantined instead of migrated")
	}

	got := map[string]string{}
	list, err := s.Characters()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		got[c.Name] = c.Instructions
	}
	want := map[string]string{
		"Both":       "front matter\n\ntrailing nudge",
		"OnlySystem": "just this",
		"OnlyPost":   "just that",
		"Neither":    "",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s instructions = %q, want %q", name, got[name], w)
		}
	}
	// The rest of the character must be untouched by the merge.
	for _, c := range list {
		if c.Name == "Both" && c.Description != "d" {
			t.Errorf("migration disturbed other columns: %+v", c)
		}
	}
}

func TestStylesAlwaysOfferTheDefaultFirst(t *testing.T) {
	c := DefaultConfig()
	if got := c.Styles(); len(got) != 1 || got[0].Name != chars.DefaultStyleName {
		t.Fatalf("a fresh config offers %v, want just the default", got)
	}
	c.WritingStyles = []chars.WritingStyle{{Name: "Sparse", Instructions: "Short sentences."}}
	got := c.Styles()
	if len(got) != 2 || got[0].Name != chars.DefaultStyleName || got[1].Name != "Sparse" {
		t.Errorf("Styles() = %v, want the default first", got)
	}
}

// A user style must not be able to shadow the built-in one, or the fallback
// every character relies on could be edited into something broken.
func TestStyleCannotShadowTheDefault(t *testing.T) {
	c := DefaultConfig()
	c.SetStyle(chars.WritingStyle{Name: chars.DefaultStyleName, Instructions: "hijacked"})
	if len(c.WritingStyles) != 0 {
		t.Errorf("a style named Default was stored: %v", c.WritingStyles)
	}
	c.WritingStyles = []chars.WritingStyle{{Name: chars.DefaultStyleName, Instructions: "hijacked"}}
	for _, s := range c.Styles() {
		if s.Name == chars.DefaultStyleName && strings.Contains(s.Instructions, "hijacked") {
			t.Error("a stored Default overrode the built-in one")
		}
	}
}

func TestSetStyleAddsThenReplaces(t *testing.T) {
	c := DefaultConfig()
	c.SetStyle(chars.WritingStyle{Name: "Noir", Instructions: "Clipped."})
	if len(c.WritingStyles) != 1 || c.ActiveStyle != "Noir" {
		t.Fatalf("after add: %v active=%q", c.WritingStyles, c.ActiveStyle)
	}
	c.SetStyle(chars.WritingStyle{Name: "Noir", Instructions: "Even more clipped."})
	if len(c.WritingStyles) != 1 {
		t.Errorf("replacing by name added a duplicate: %v", c.WritingStyles)
	}
	if c.Style().Instructions != "Even more clipped." {
		t.Errorf("Style() = %q", c.Style().Instructions)
	}
}

// Deleting the style in use must not leave the app pointing at nothing.
func TestDeletingTheActiveStyleFallsBack(t *testing.T) {
	c := DefaultConfig()
	c.SetStyle(chars.WritingStyle{Name: "Noir", Instructions: "Clipped."})
	c.DeleteStyle("Noir")
	if c.ActiveStyle != chars.DefaultStyleName {
		t.Errorf("ActiveStyle = %q after deleting it", c.ActiveStyle)
	}
	if c.Style().Name != chars.DefaultStyleName {
		t.Errorf("Style() = %q", c.Style().Name)
	}
}

// An active style naming something that no longer exists — a config carried
// between machines, say — must resolve rather than fail.
func TestUnknownActiveStyleResolvesToDefault(t *testing.T) {
	c := DefaultConfig()
	c.ActiveStyle = "Something Else"
	if got := c.Style(); got.Name != chars.DefaultStyleName {
		t.Errorf("Style() = %q, want the default", got.Name)
	}
}

func TestStylesSurviveARoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c := DefaultConfig()
	c.SetStyle(chars.WritingStyle{Name: "Noir", Instructions: "Clipped.\nNo adverbs."})
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	back, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if back.ActiveStyle != "Noir" || back.Style().Instructions != "Clipped.\nNo adverbs." {
		t.Errorf("round trip lost the style: %+v", back.WritingStyles)
	}
}

func TestChatSummaryRoundTrip(t *testing.T) {
	s := openTest(t)
	ch, _ := s.NewChat(0, "Scene", "m", KindRoleplay)

	var ids []int64
	for i := 0; i < 5; i++ {
		id, _ := s.AddMessage(Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: fmt.Sprintf("turn %d", i)})
		ids = append(ids, id)
	}

	if err := s.SetChatSummary(ch.ID, "Vesper met Wren.", ids[2]); err != nil {
		t.Fatal(err)
	}
	got, err := s.Chat(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "Vesper met Wren." || got.SummaryUpto != ids[2] {
		t.Errorf("summary = %q upto=%d", got.Summary, got.SummaryUpto)
	}

	// Only the turns the recap does not cover should come back.
	rest, err := s.MessagesAfter(ch.ID, got.SummaryUpto)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 {
		t.Fatalf("MessagesAfter returned %d turns, want 2", len(rest))
	}
	if rest[0].Content != "turn 3" {
		t.Errorf("first uncovered turn = %q, want \"turn 3\"", rest[0].Content)
	}
	// And the full read must still return everything.
	if all, _ := s.Messages(ch.ID); len(all) != 5 {
		t.Errorf("Messages returned %d, want all 5", len(all))
	}
}

// A database written before the recap existed must gain the columns without
// disturbing the chats already in it.
func TestMigrationAddsSummaryColumns(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	path := filepath.Join(dir, "astral.db")

	old, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE chats (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			character_id INTEGER NOT NULL DEFAULT 0,
			title        TEXT    NOT NULL DEFAULT '',
			model        TEXT    NOT NULL DEFAULT '',
			kind         TEXT    NOT NULL DEFAULT 'roleplay',
			created_at   INTEGER NOT NULL DEFAULT 0,
			updated_at   INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO chats (title, model, created_at, updated_at)
		VALUES ('An older scene', 'qwen3:8b', 1, 1);`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, recovered, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a pre-recap database: %v", err)
	}
	defer s.Close()
	if recovered {
		t.Fatal("a migratable database was quarantined instead of migrated")
	}

	chats, err := s.Chats()
	if err != nil || len(chats) != 1 {
		t.Fatalf("chats=%v err=%v", chats, err)
	}
	got, err := s.Chat(chats[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "An older scene" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Summary != "" || got.SummaryUpto != 0 {
		t.Errorf("existing chat gained a recap: %q upto=%d", got.Summary, got.SummaryUpto)
	}
	if err := s.SetChatSummary(got.ID, "works", 1); err != nil {
		t.Errorf("writing a recap to a migrated database: %v", err)
	}
}
