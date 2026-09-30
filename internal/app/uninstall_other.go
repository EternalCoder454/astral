//go:build !linux

package app

import "errors"

// On Windows the installer registers an uninstaller with the system, and
// Settings, Apps is where people look for it, so Astral does not offer one of
// its own. Elsewhere Astral is not installed by anything it knows how to undo.

func canUninstall() bool { return false }

func programFiles() []string { return nil }

func runningInstalled() bool { return false }

func removeProgramFiles() error { return errors.New("remove Astral from Settings, Apps") }
