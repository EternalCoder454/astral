package store

import "testing"

func TestAnOldConfigIsMigratedOnce(t *testing.T) {
	// A config written before revisions existed, with the old defaults in it.
	c := Config{WebSearch: false, KeepAlive: "30m"}
	c.normalize()
	if !c.WebSearch || !c.KeepReading || c.SearchProvider != SearchAuto {
		t.Errorf("search was not switched on: %+v", c)
	}
	if c.KeepAlive != "" {
		t.Errorf("the old keep-alive default was kept: %q", c.KeepAlive)
	}
	if c.Revision != currentRevision {
		t.Errorf("revision %d", c.Revision)
	}

	// Afterwards, what the person chooses is what they get.
	c.WebSearch = false
	c.KeepAlive = "30m"
	c.normalize()
	if c.WebSearch {
		t.Error("search was switched back on after it was turned off")
	}
	if c.KeepAlive != "30m" {
		t.Errorf("a chosen keep-alive was cleared: %q", c.KeepAlive)
	}
}

func TestANewConfigStartsWithSearchOn(t *testing.T) {
	c := DefaultConfig()
	c.normalize()
	if !c.WebSearch || c.SearchProvider != SearchAuto || c.KeepAlive != "" {
		t.Errorf("defaults: search %v, provider %q, keep-alive %q", c.WebSearch, c.SearchProvider, c.KeepAlive)
	}
}

func TestAnUnknownProviderFallsBackToAutomatic(t *testing.T) {
	c := DefaultConfig()
	c.SearchProvider = "bing"
	c.normalize()
	if c.SearchProvider != SearchAuto {
		t.Errorf("got %q", c.SearchProvider)
	}
}
