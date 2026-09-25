// Package store owns everything Astral persists: the JSON config file and the
// SQLite database holding characters, chats and messages.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"astral/internal/chars"
)

// AppName is the XDG application directory name.
const AppName = "astral"

// Colour schemes. System follows the desktop's own light/dark preference.
const (
	ThemeDark   = "dark"
	ThemeLight  = "light"
	ThemeSystem = "system"
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
)

// Config holds user settings persisted to ~/.config/astral/config.json.
type Config struct {
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	Theme    string `json:"theme"`
	LastChat int64  `json:"last_chat"`

	WindowWidth  int  `json:"window_width"`
	WindowHeight int  `json:"window_height"`
	SidebarWidth int  `json:"sidebar_width"`
	SidebarOpen  bool `json:"sidebar_open"`
	// PortraitOpen remembers whether the character portrait panel was showing.
	PortraitOpen  bool   `json:"portrait_open"`
	FontRendering string `json:"font_rendering"`

	// PersonaName / PersonaDescription are who the user plays as. Empty is
	// fine and common — plenty of scenes work with an unnamed protagonist.
	PersonaName        string `json:"persona_name"`
	PersonaDescription string `json:"persona_description"`

	// GlobalInstructions apply to every character, layered underneath that
	// character's own. Useful for the rules that are about how *you* want to
	// read a scene rather than about any one character.
	GlobalInstructions string `json:"global_instructions"`

	// WritingStyles are the user's own styles. The built-in default is not
	// stored here — it is always available and cannot be edited away, so
	// keeping it out of the file means a config that loses these keys still
	// has a usable style rather than none.
	WritingStyles []chars.WritingStyle `json:"writing_styles"`
	// ActiveStyle is the name of the style in use. An unknown name falls back
	// to the default rather than failing, so deleting the active style is a
	// safe thing to do.
	ActiveStyle string `json:"active_style"`

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
	// Those two jobs are bookkeeping, not prose, and a much smaller model does
	// them about as well. Measured on a nine-fact scene, a 4B matched a 27B on
	// both and ran them in roughly half the time. The point is not only the
	// time: they run in the background after a reply, so whatever they use is
	// the thing the next message queues behind.
	//
	// It has to be small enough to sit in memory beside the scene's model.
	// Ollama keeps both loaded rather than swapping, which is what makes this
	// worth doing at all — but only while both fit. See ollama.Running.
	HousekeepingModel string `json:"housekeeping_model"`

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
	// write nothing they can act on — a reader cannot tell whether 34.8 is
	// good, and does not know what a token is. The timestamp stays either way.
	ShowStats bool `json:"show_stats"`
}

func dataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", AppName)
}

// DataDir is the per-user data directory ($XDG_DATA_HOME/astral or
// ~/.local/share/astral). The database and character avatars live under it.
func DataDir() string { return dataDir() }

// AvatarDir is where imported character portraits are kept.
func AvatarDir() string { return filepath.Join(dataDir(), "avatars") }

func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", AppName)
}

// ConfigPath is the absolute path of config.json.
func ConfigPath() string { return filepath.Join(configDir(), "config.json") }

// DefaultDBPath is ~/.local/share/astral/astral.db.
func DefaultDBPath() string { return filepath.Join(dataDir(), "astral.db") }

// The update channels, which are the repository's two branches. Release is
// what a normal install follows; beta is ahead of it and may be rough.
const (
	ChannelRelease = "release"
	ChannelBeta    = "beta"
)

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() Config {
	return Config{
		BaseURL:       "http://localhost:11434",
		Theme:         ThemeDark,
		WindowWidth:   1180,
		WindowHeight:  780,
		SidebarWidth:  270,
		SidebarOpen:   true,
		PortraitOpen:  true,
		FontRendering: FontRenderingAuto,
		KeepAlive:     "30m",
		UpdateChannel: ChannelRelease,
		CheckUpdates:  true,
		ActiveStyle:   chars.DefaultStyleName,
		Temperature:   DefaultTemperature,
		TopP:          DefaultTopP,
		RepeatPenalty: DefaultRepeatPenalty,
		RepeatLastN:   DefaultRepeatLastN,
		NumCtx:        DefaultNumCtx,
		ShowStats:     false,
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
func (c *Config) normalize() {
	if c.BaseURL == "" {
		c.BaseURL = "http://localhost:11434"
	}
	switch c.Theme {
	case ThemeDark, ThemeLight, ThemeSystem:
	default:
		c.Theme = ThemeDark
	}
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
	if c.SidebarWidth < 180 || c.SidebarWidth > 600 {
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
	if c.UpdateChannel != ChannelBeta && c.UpdateChannel != ChannelRelease {
		c.UpdateChannel = ChannelRelease
	}
	if c.NumCtx < 512 {
		c.NumCtx = DefaultNumCtx
	}
	if strings.TrimSpace(c.KeepAlive) == "" {
		c.KeepAlive = "30m"
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
// renames over the target — so an interrupted write leaves the old file intact
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
