//go:build !linux

package app

import "errors"

// Flatpak is Linux only; see flatpak_linux.go.

func inFlatpak() bool { return false }

func (a *App) flatpakUpdate(string, func(string, bool)) error {
	return errors.New("not a Flatpak")
}

func (a *App) restartFlatpak() error { return errors.New("not a Flatpak") }

func uninstallFlatpak(bool) error { return errors.New("not a Flatpak") }
