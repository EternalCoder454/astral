//go:build windows

package app

import "errors"

// On Windows the installer registers an uninstaller with the system, and
// Settings, Apps is where people look for it, so Astral does not offer one of
// its own.

func canUninstall() bool { return false }

func programFiles() []string { return nil }

func removeProgramFiles() error { return errors.New("remove Astral from Settings, Apps") }
