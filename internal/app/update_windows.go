//go:build windows

package app

// Astral updates itself on Linux by fetching the source and rebuilding, which
// is the right trade there: what you end up running was built against the GTK
// and libadwaita on the machine running it. None of that applies on Windows,
// where there is no toolchain to build with and the libraries ship inside the
// installer. An update there is a new installer, so the app says so rather
// than pretending it can do it.

func (a *App) installUpdate(branch string, onStatus func(text string, done bool)) {
	onStatus("Download the new version from "+projectURL+"/releases and run the installer.", true)
}
