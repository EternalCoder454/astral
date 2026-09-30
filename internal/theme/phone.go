package theme

// Phone is a theme as the phone's Material design wants it: the same colours,
// under the role names style.css is written in. Only colour crosses over. The
// shapes, spacing and components stay the phone's own.
type Phone struct {
	// Dark says whether the bars around the page need light or dark icons.
	Dark bool `json:"dark"`
	// Roles is the hex colour of each Material role, keyed by the role's name
	// without the --md- prefix that style.css gives it.
	Roles map[string]string `json:"roles"`
}

// inkPhone is what style.css has always said. Ink is the default, so most
// phones are on it, and those values were tuned by eye against the page. Deriving
// them from the desktop palette instead would move every colour a little for
// people who chose nothing, so Ink keeps its own.
var inkPhone = map[string]string{
	"primary":                   "#e3a0a6",
	"on-primary":                "#3b1a20",
	"primary-container":         "#6d4048",
	"on-primary-container":      "#ffd9dd",
	"surface":                   "#14162a",
	"surface-container-lowest":  "#0d0f1f",
	"surface-container-low":     "#1b1e32",
	"surface-container":         "#22263c",
	"surface-container-high":    "#2c3049",
	"surface-container-highest": "#373c58",
	"on-surface":                "#e6e1ef",
	"on-surface-variant":        "#c8c3d6",
	"outline":                   "#8e88a0",
	"outline-variant":           "#453f57",
	"error":                     "#ffb4ab",
}

// PhoneRoles maps a theme onto the phone's Material roles.
//
// The desktop's levels are a ladder, and Material's surface containers are one
// too, so they line up rung for rung: the sidebar is the lowest, the canvas is
// low, and the desktop's surface and elevated levels are high and highest. The
// two between (plain surface, and container) are mixes, because Material has
// five rungs where the desktop has four.
func PhoneRoles(t Theme) Phone {
	if t.ID == Default {
		roles := make(map[string]string, len(inkPhone))
		for k, v := range inkPhone {
			roles[k] = v
		}
		return Phone{Dark: t.Dark, Roles: roles}
	}
	sidebar, canvas := mustHex(t.Color("astral_sidebar")), mustHex(t.Color("astral_canvas"))
	surface := mustHex(t.Color("astral_surface"))
	// Material's containers step up from the page: the page darkest in a
	// dark scheme, lightest in a light one, each container a step further.
	// On the desktop a light theme's sidebar is its darkest level, so the
	// dark scheme's order put a light theme's page below its cards, and the
	// phone's chat list lost its cards to the page behind them.
	levels := []string{
		sidebar.hex(), mix(sidebar, canvas, 0.6).hex(), canvas.hex(),
		mix(canvas, surface, 0.5).hex(), surface.hex(), t.Color("astral_elevated"),
	}
	if !t.Dark {
		levels = []string{
			canvas.hex(), canvas.hex(), mix(canvas, surface, 0.5).hex(),
			surface.hex(), mix(surface, sidebar, 0.5).hex(), sidebar.hex(),
		}
	}
	return Phone{Dark: t.Dark, Roles: map[string]string{
		"surface-container-lowest":  levels[0],
		"surface":                   levels[1],
		"surface-container-low":     levels[2],
		"surface-container":         levels[3],
		"surface-container-high":    levels[4],
		"surface-container-highest": levels[5],
		"on-surface":                t.Color("astral_text"),
		"on-surface-variant":        t.Color("astral_muted"),
		"outline":                   t.Color("astral_faint"),
		"outline-variant":           t.Color("astral_border"),
		"primary":                   t.Color("accent_color"),
		"on-primary":                t.Color("accent_fg_color"),
		"primary-container":         t.Color("astral_user_bubble"),
		"on-primary-container":      t.Color("astral_text"),
		"error":                     t.Color("error_color"),
	}}
}
