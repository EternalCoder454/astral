// Command astral is a local-first desktop app for roleplaying with characters
// through any model served by Ollama. It is built on GTK4 + libadwaita.
package main

import (
	_ "embed"
	"os"

	"astral/internal/app"
)

//go:embed assets/style.css
var styleCSS string

//go:embed assets/dark.css
var darkCSS string

//go:embed assets/light.css
var lightCSS string

func main() {
	os.Exit(app.New(app.Assets{
		Style: styleCSS,
		Dark:  darkCSS,
		Light: lightCSS,
	}).Run(os.Args))
}
