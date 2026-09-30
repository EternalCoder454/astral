package app

import (
	"testing"

	"astral/internal/store"
)

// A settings change from a phone carries a copy of the settings read before
// it, so what only the window sets is taken from the window: a theme chosen
// on the desktop a moment before must not come back as the old one.
func TestAPhoneKeepsTheWindowsOwnSettings(t *testing.T) {
	window := store.DefaultConfig()
	window.Theme = "nord"
	window.WindowWidth, window.SidebarOpen, window.WindowMaximized = 1400, false, true

	phone := store.DefaultConfig()
	phone.Theme = "ink"
	phone.Model = "from-the-phone"

	keepWindowOwn(&phone, window)
	if phone.Theme != "nord" || phone.WindowWidth != 1400 || phone.SidebarOpen || !phone.WindowMaximized {
		t.Errorf("the window's own settings were not kept: %+v", phone)
	}
	if phone.Model != "from-the-phone" {
		t.Errorf("the phone's change was lost: %q", phone.Model)
	}
}
