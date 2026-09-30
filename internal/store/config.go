// Package store owns everything Astral persists: the JSON config file and the
// SQLite database holding characters, chats and messages.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"astral/internal/chars"
	"astral/internal/theme"
)

// AppName is the XDG application directory name.
const AppName = "astral"

// Colour themes. The setting is a theme's ID from internal/theme, or ThemeSystem
// to follow the desktop's own light and dark preference. Settings files written
// before there were themes say "dark" or "light", which loading carries over to
// Ink and Paper.
const (
	ThemeDefault = theme.Default
	ThemeSystem  = theme.Follow
)

// Font-rendering modes: "crisp" hints glyphs onto the pixel grid, which is what
// a 1x display needs; "smooth" leaves GTK's unhinted defaults, which suit a
// HiDPI screen; "auto" picks per display.
const (
	FontRenderingAuto   = "auto"
	FontRenderingCrisp  = "crisp"
	FontRenderingSmooth = "smooth"
)

// Sampling defaults. These are deliberately not the model's own: roleplay wants
// more variety than the assistant-style defaults most models ship with, or
// every scene reads the same way. They stay conservative enough not to produce
// incoherence on a small model.
const (
	DefaultTemperature   = 0.85
	DefaultTopP          = 0.92
	DefaultRepeatPenalty = 1.08
	// DefaultRepeatLastN covers a cycle long enough to be pathological while
	// staying short of the whole scene. Wider would start penalising a
	// character's own name, which recurs legitimately on every turn.
	DefaultRepeatLastN = 384
	DefaultNumCtx      = 8192
	// DefaultKeepAlive is empty: the server decides how long a model stays
	// loaded, and Astral says nothing.
	//
	// It was thirty minutes, sent with every request, which overrides
	// whatever the server was configured with. That is how a machine set up
	// to unload after five idle minutes, precisely so models do not pile up
	// in video memory, kept Astral's resident for half an hour anyway. The
	// reload that a short keep-alive costs is hidden instead by loading the
	// model as soon as someone starts typing (see ollama.Preload).
	DefaultKeepAlive = ""
	// DefaultSearchResults is how many hits one web search asks for. Five is
	// enough to answer a question and few enough not to become the prompt.
	DefaultSearchResults = 5
	// DefaultSearXNGURL is the port SearXNG's own instructions use. Filling it in
	// costs nothing, because search stays off until it is switched on, and it
	// saves everyone typing the one address the documentation already told them.
	DefaultSearXNGURL = "http://localhost:8080"
)

// Config holds user settings persisted to ~/.config/astral/config.json.
type Config struct {
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	Theme    string `json:"theme"`
	LastChat int64  `json:"last_chat"`

	WindowWidth  int `json:"window_width"`
	WindowHeight int `json:"window_height"`
	// WindowMaximized opens the window maximized, as it was closed. The
	// width and height stay the size it goes back to.
	WindowMaximized bool `json:"window_maximized"`
	SidebarWidth    int  `json:"sidebar_width"`
	SidebarOpen     bool `json:"sidebar_open"`
	// GroupChatsByCharacter sorts the sidebar's chats under their characters
	// rather than under the day they were last written in.
	GroupChatsByCharacter bool `json:"group_chats_by_character"`
	// PortraitOpen remembers whether the character portrait panel was showing.
	PortraitOpen  bool   `json:"portrait_open"`
	FontRendering string `json:"font_rendering"`

	// PersonaName / PersonaDescription are who the user plays as. Empty is
	// fine and common, plenty of scenes work with an unnamed protagonist.
	//
	// Since there can be several personas these are the one in use by
	// default, written out: ActivePersona says which, and the app writes its
	// name and description here whenever it changes, so everything that only
	// wants "who am I" reads it from one place.
	PersonaName        string `json:"persona_name"`
	PersonaDescription string `json:"persona_description"`
	ActivePersona      int64  `json:"active_persona"`

	// GlobalInstructions is the freeform instruction block the rulebook
	// replaced. It is emptied into Rulebook the first time a config written
	// before rules existed is loaded, and is kept in the struct only so that
	// migration can find it. See rules.go.
	GlobalInstructions string `json:"global_instructions"`

	// WebSearch lets the conversations that are not roleplay look things up.
	//
	// On by default. It was off, on the grounds that it is the one feature that
	// sends anything off this machine, and the result was a general chat that
	// answered every question about the present from weights a year out of
	// date. What leaves is the words searched for and the pages opened, never
	// the conversation, and every reply that searched says what it looked up.
	// See internal/websearch.
	WebSearch bool `json:"web_search"`
	// SearchProvider is where searches go: SearchAuto uses SearXNG when it is
	// running and DuckDuckGo when it is not, the other two use only that one.
	SearchProvider string `json:"search_provider"`
	// SearXNGURL is the instance to search through, which you run yourself.
	SearXNGURL string `json:"searxng_url"`
	// KeepReading saves the pages a search opens into the knowledge base, so a
	// subject looked up once is known the next time without searching again.
	KeepReading bool `json:"keep_reading"`
	// EmbeddingModel makes the vectors the knowledge base searches by meaning.
	// Empty means the first embedding model installed, or none, in which case
	// it searches by words alone, which works everywhere.
	EmbeddingModel string `json:"embedding_model"`
	// Revision is which of the one-time settings changes below this file has
	// been through. See migrate.
	Revision int `json:"revision"`
	// SearchResults is how many hits one search asks for.
	SearchResults int `json:"search_results"`

	// Rulebook is the standing instructions every scene is under, each one
	// switchable on its own. See rules.go.
	Rulebook []Rule `json:"rulebook"`

	// WritingStyles are the user's own styles. The built-in default is not
	// stored here, it is always available and cannot be edited away, so
	// keeping it out of the file means a config that loses these keys still
	// has a usable style rather than none.
	WritingStyles []chars.WritingStyle `json:"writing_styles"`
	// ActiveStyle is the name of the style in use. An unknown name falls back
	// to the default rather than failing, so deleting the active style is a
	// safe thing to do.
	ActiveStyle string `json:"active_style"`

	// PhoneAccess lets another device on this network use this machine's
	// Astral. Off unless asked for: it opens a port, and nothing that opens a
	// port should do it because a default said so.
	PhoneAccess bool `json:"phone_access"`
	// PhonePort is where it listens.
	PhonePort int `json:"phone_port"`

	// UpdateChannel is the branch update checks follow: release or beta.
	UpdateChannel string `json:"update_channel"`
	// CheckUpdates asks GitHub on launch whether a newer version has been
	// published. It reads one text file and sends nothing about this machine
	// or anything in it.
	CheckUpdates bool `json:"check_updates"`

	// KeepAlive is how long Ollama holds the model in memory between turns.
	// Ollama's own default is five minutes, which a thinking pause routinely
	// exceeds, and the next message then pays a full model reload.
	KeepAlive string `json:"keep_alive"`

	// HousekeepingModel writes the recap and reads the scene for lore. Empty
	// means use whichever model is playing the scene.
	//
	// Bookkeeping, not prose: measured on a nine-fact scene a 4B matched a 27B
	// on both and ran in half the time. They run after a reply, so whatever
	// they use is what the next message queues behind.
	//
	// It must be small enough to sit in memory beside the scene's model, since
	// Ollama keeps both loaded rather than swapping. See ollama.Running.
	HousekeepingModel string `json:"housekeeping_model"`

	// NotifyReplies says when a reply finishes while Astral is in the
	// background, so a slow model can be left to write while you do something
	// else.
	NotifyReplies bool `json:"notify_replies"`

	// VisionModel looks at the pictures sent into a chat. Empty means choose:
	// the chat's own model when it can see, and otherwise the largest model
	// that can and that fits on the card by itself. Named, it reads every
	// picture, even for a chat whose model could have looked for itself.
	VisionModel string `json:"vision_model"`

	Temperature   float64 `json:"temperature"`
	TopP          float64 `json:"top_p"`
	TopK          int     `json:"top_k"`
	RepeatPenalty float64 `json:"repeat_penalty"`
	// RepeatLastN is the window the repetition penalty looks back over, in
	// tokens. See ollama.Options.RepeatLastN for why the server's own default
	// of 64 is too short to catch a collapse.
	RepeatLastN int `json:"repeat_last_n"`
	NumCtx      int `json:"num_ctx"`
	NumPredict  int `json:"num_predict"`

	// Think enables a reasoning model's scratchpad. Off by default: in
	// roleplay it mostly buys a long visible deliberation before a reply that
	// would have been the same without it.
	Think bool `json:"think"`

	// ShowStats puts tok/s and a token count under each reply.
	//
	// Off by default. It is a developer's measurement sitting directly under
	// the prose, and "34.8 tok/s · 112 tokens" tells someone who came here to
	// write nothing they can act on, a reader cannot tell whether 34.8 is
	// good, and does not know what a token is. The timestamp stays either way.
	ShowStats bool `json:"show_stats"`
}

// Where Astral keeps things.
//
// XDG is honoured first, because the tests set those variables to redirect a
// run into a temporary directory and because it is what a Linux user expects.
// Past that it defers to the standard library, which already knows the right
// answer on each platform: %AppData% on Windows, ~/Library/Application Support
// on macOS, ~/.config elsewhere. Hard-coding ~/.local/share worked on the one
// platform it was written for and put a dotted directory in the middle of a
// Windows home folder on another.

func dataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	if runtime.GOOS == "linux" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", AppName)
	}
	// No UserDataDir in the standard library. The config directory is the
	// right neighbourhood on the platforms that have no separate one.
	base, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		base = home
	}
	return filepath.Join(base, AppName, "data")
}

// DataDir is the per-user data directory. The database and character images
// live under it.
func DataDir() string { return dataDir() }

// AvatarDir is where imported character portraits are kept.
func AvatarDir() string { return filepath.Join(dataDir(), "avatars") }

func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", AppName)
	}
	return filepath.Join(base, AppName)
}

// ConfigPath is the absolute path of config.json.
func ConfigPath() string { return filepath.Join(configDir(), "config.json") }

// DefaultDBPath is the database inside DataDir.
func DefaultDBPath() string { return filepath.Join(dataDir(), "astral.db") }

// The update channels, which are the repository's two branches. Release is
// what a normal install follows; beta is ahead of it and may be rough.
const (
	ChannelRelease = "release"
	ChannelBeta    = "beta"
)

// DefaultConfig returns a Config populated with sensible defaults.
// DefaultPhonePort is where phone access listens. Written down here rather
// than in the server so that the settings page and the server cannot disagree
// about what to tell you to type into a phone.
const DefaultPhonePort = 8765

func DefaultConfig() Config {
	return Config{
		BaseURL:        "http://localhost:11434",
		Theme:          ThemeDefault,
		WindowWidth:    1180,
		WindowHeight:   780,
		SidebarWidth:   270,
		SidebarOpen:    true,
		PortraitOpen:   true,
		FontRendering:  FontRenderingAuto,
		KeepAlive:      DefaultKeepAlive,
		SearchResults:  DefaultSearchResults,
		SearXNGURL:     DefaultSearXNGURL,
		WebSearch:      true,
		SearchProvider: SearchAuto,
		KeepReading:    true,
		Revision:       currentRevision,
		PhonePort:      DefaultPhonePort,
		UpdateChannel:  ChannelRelease,
		CheckUpdates:   true,
		ActiveStyle:    chars.DefaultStyleName,
		Temperature:    DefaultTemperature,
		TopP:           DefaultTopP,
		RepeatPenalty:  DefaultRepeatPenalty,
		RepeatLastN:    DefaultRepeatLastN,
		NumCtx:         DefaultNumCtx,
		ShowStats:      false,
		NotifyReplies:  true,
	}
}

// LoadConfig reads config.json, writing and returning defaults when it is
// missing. Unmarshalling happens *over* a defaults struct, so a setting added
// in a later version backfills itself instead of arriving as a zero value.
func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, SaveConfig(cfg)
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), err
	}
	cfg.normalize()
	return cfg, nil
}

// normalize repairs values that are missing or out of range, so a hand-edited
// or truncated config cannot produce an unusable window.
// The search providers.
const (
	SearchAuto       = "auto"
	SearchSearXNG    = "searxng"
	SearchDuckDuckGo = "duckduckgo"
)

// currentRevision is the newest one-time settings change.
const currentRevision = 1

// migrate applies the one-time changes a config has not been through yet.
//
// A config file holds every setting, including the ones nobody ever touched,
// because it is written out whole. So an old default is indistinguishable from
// a choice, and changing a default reaches nobody who has launched the app
// before. A revision number is how a new default reaches them once, and only
// once: after this runs, whatever they set is kept.
func (c *Config) migrate() {
	if c.Revision < 1 {
		// Search on, which was asked for, and the pages it reads kept.
		c.WebSearch = true
		c.KeepReading = true
		if c.SearchProvider == "" {
			c.SearchProvider = SearchAuto
		}
		// "30m" was the old keep-alive default and so is in every config
		// written before this, chosen or not. Cleared once, so the server's
		// own setting applies; anyone who wants it can type it back in.
		if strings.TrimSpace(c.KeepAlive) == "30m" {
			c.KeepAlive = DefaultKeepAlive
		}
	}
	c.Revision = currentRevision
}

func (c *Config) normalize() {
	// A config written before the rulebook existed keeps its instructions in
	// one freeform block. They become rules here, once.
	c.adoptGlobalInstructions()
	c.SetRules(c.Rulebook)
	c.migrate()
	switch c.SearchProvider {
	case SearchAuto, SearchSearXNG, SearchDuckDuckGo:
	default:
		c.SearchProvider = SearchAuto
	}
	if c.BaseURL == "" {
		c.BaseURL = "http://localhost:11434"
	}
	c.Theme = theme.Normalise(c.Theme)
	switch c.FontRendering {
	case FontRenderingCrisp, FontRenderingSmooth:
	default:
		c.FontRendering = FontRenderingAuto
	}
	if c.WindowWidth < 640 {
		c.WindowWidth = 1180
	}
	if c.WindowHeight < 480 {
		c.WindowHeight = 780
	}
	if c.SidebarWidth < 150 || c.SidebarWidth > 600 {
		c.SidebarWidth = 270
	}
	// Sampling values are clamped rather than reset: someone who typed 3.0 for
	// temperature wanted "high", and the nearest usable value honours that
	// better than silently reverting to the default.
	if c.Temperature < 0 || c.Temperature > 2 {
		c.Temperature = DefaultTemperature
	}
	if c.TopP < 0 || c.TopP > 1 {
		c.TopP = DefaultTopP
	}
	if c.RepeatPenalty < 0 || c.RepeatPenalty > 3 {
		c.RepeatPenalty = DefaultRepeatPenalty
	}
	if c.RepeatLastN <= 0 {
		c.RepeatLastN = DefaultRepeatLastN
	}
	if c.SearchResults <= 0 || c.SearchResults > 10 {
		c.SearchResults = DefaultSearchResults
	}
	// "SearXNG only" with no SearXNG to use would offer the model a tool that
	// fails on every call. Automatic has DuckDuckGo behind it, so it is what
	// that choice becomes, rather than search being quietly switched off.
	if c.SearchProvider == SearchSearXNG && strings.TrimSpace(c.SearXNGURL) == "" {
		c.SearchProvider = SearchAuto
	}
	if c.PhonePort <= 0 || c.PhonePort > 65535 {
		c.PhonePort = DefaultPhonePort
	}
	if c.UpdateChannel != ChannelBeta && c.UpdateChannel != ChannelRelease {
		c.UpdateChannel = ChannelRelease
	}
	if c.NumCtx < 512 {
		c.NumCtx = DefaultNumCtx
	}
	if strings.TrimSpace(c.ActiveStyle) == "" {
		c.ActiveStyle = chars.DefaultStyleName
	}
}

// Styles returns every style available, the built-in default first.
func (c Config) Styles() []chars.WritingStyle {
	out := make([]chars.WritingStyle, 0, len(c.WritingStyles)+1)
	out = append(out, chars.DefaultStyle())
	for _, s := range c.WritingStyles {
		if strings.TrimSpace(s.Name) == "" || s.Name == chars.DefaultStyleName {
			continue // a user style cannot shadow the default
		}
		out = append(out, s)
	}
	return out
}

// Style returns the active style, or the default when the active one has been
// deleted or never existed.
func (c Config) Style() chars.WritingStyle {
	for _, s := range c.Styles() {
		if s.Name == c.ActiveStyle {
			return s
		}
	}
	return chars.DefaultStyle()
}

// SetStyle adds or replaces a style by name and makes it active. The default
// cannot be overwritten.
func (c *Config) SetStyle(s chars.WritingStyle) {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || s.Name == chars.DefaultStyleName {
		return
	}
	for i := range c.WritingStyles {
		if c.WritingStyles[i].Name == s.Name {
			c.WritingStyles[i] = s
			c.ActiveStyle = s.Name
			return
		}
	}
	c.WritingStyles = append(c.WritingStyles, s)
	c.ActiveStyle = s.Name
}

// DeleteStyle removes a style by name. Deleting the active one falls back to
// the default.
func (c *Config) DeleteStyle(name string) {
	out := c.WritingStyles[:0]
	for _, s := range c.WritingStyles {
		if s.Name != name {
			out = append(out, s)
		}
	}
	c.WritingStyles = out
	if c.ActiveStyle == name {
		c.ActiveStyle = chars.DefaultStyleName
	}
}

// SaveConfig atomically writes cfg to config.json.
func SaveConfig(cfg Config) error {
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(ConfigPath(), data)
}

// atomicWrite writes to a temp file in the same directory, fsyncs it, then
// renames over the target, so an interrupted write leaves the old file intact
// rather than a half-written one.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
