package scene

import (
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/store"
	"astral/internal/websearch"
)

// TestOnlyTheRightKindsCanSearch is the boundary the feature is defined by. A
// scene must never be offered the tool: a roleplay does not want facts from
// outside it, a search would fire on names that exist only in the story, and a
// scene cannot survive the model breaking off to report what it found online.
func TestOnlyTheRightKindsCanSearch(t *testing.T) {
	for _, kind := range []string{
		store.KindAssistant, store.KindDesigner, store.KindStyleDesigner, store.KindWorldDesigner,
	} {
		if !CanSearch(kind) {
			t.Errorf("%s should be able to search", kind)
		}
	}
	if CanSearch(store.KindRoleplay) {
		t.Error("a roleplay must never be able to search")
	}
	if CanSearch("") {
		t.Error("an unknown kind defaults to roleplay, so it must not be able to search")
	}
}

// Search needs only the switch now: with no SearXNG to use, the automatic
// provider falls back to DuckDuckGo, so an empty address is not a reason to
// have no search at all.
func TestSearchableFollowsTheSwitch(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  store.Config
		want bool
	}{
		{"off", store.Config{}, false},
		{"on with no address", store.Config{WebSearch: true}, true},
		{"address with the switch off", store.Config{SearXNGURL: "http://localhost:8080"}, false},
		{"both", store.Config{WebSearch: true, SearXNGURL: "http://localhost:8080"}, true},
	} {
		if got := Searchable(tc.cfg); got != tc.want {
			t.Errorf("%s: Searchable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSearchProviderFollowsTheSetting(t *testing.T) {
	for provider, want := range map[string]string{
		store.SearchSearXNG:    "SearXNG",
		store.SearchDuckDuckGo: "DuckDuckGo",
		store.SearchAuto:       "SearXNG", // tried first, while it has not failed
		"":                     "SearXNG",
	} {
		cfg := store.Config{WebSearch: true, SearchProvider: provider, SearXNGURL: "http://localhost:8080"}
		if got := SearchProvider(cfg).Name(); got != want {
			t.Errorf("%q: got %s, want %s", provider, got, want)
		}
	}
	// Automatic with no address goes straight to the fallback.
	cfg := store.Config{WebSearch: true, SearchProvider: store.SearchAuto}
	if got := SearchProvider(cfg).Name(); got != "DuckDuckGo" {
		t.Errorf("automatic with no address: %s", got)
	}
}

// The guidance reaches every conversation that can search, and no scene.
func TestSearchGuidanceReachesTheRightPrompts(t *testing.T) {
	on := store.Config{WebSearch: true, SearXNGURL: "http://localhost:8080", NumCtx: 8192}
	marker := strings.SplitN(websearch.Guidance, "\n", 2)[0]

	for _, kind := range []string{
		store.KindAssistant, store.KindDesigner, store.KindStyleDesigner, store.KindWorldDesigner,
	} {
		msgs := Build(nil, on, store.Chat{ID: 1, Kind: kind}, chars0(), nil)
		if !strings.Contains(msgs[0].Content, marker) {
			t.Errorf("%s never hears that it can search", kind)
		}
	}

	// And a scene does not, however the setting is left.
	scene := Build(nil, on, store.Chat{ID: 1, Kind: store.KindRoleplay}, oneCharacter(), nil)
	for _, m := range scene {
		if strings.Contains(m.Content, marker) {
			t.Errorf("a scene was told it can search, in a %s message", m.Role)
		}
	}
}

func TestNoSearchGuidanceWhenItIsOff(t *testing.T) {
	off := store.Config{NumCtx: 8192}
	marker := strings.SplitN(websearch.Guidance, "\n", 2)[0]
	msgs := Build(nil, off, store.Chat{ID: 1, Kind: store.KindAssistant}, chars0(), nil)
	if strings.Contains(msgs[0].Content, marker) {
		t.Error("a chat was told it can search while search is switched off")
	}
}

// oneCharacter is a scene with somebody in it, so the roleplay path is taken.
func oneCharacter() chars.Character {
	return chars.Character{Name: "Vesper", Description: "A cartographer."}
}
