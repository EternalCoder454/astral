package app

import (
	"context"
	"fmt"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/scene"
	"astral/internal/store"
	"astral/internal/websearch"
)

// The web search settings.
//
// On by default, because a local model's knowledge stops where its weights
// stop and General Chat is where that shows. The card says plainly what leaves
// this machine: the words searched for and the pages opened, never the
// conversation. With no SearXNG of your own, searches go to DuckDuckGo.

// providerRows are the provider choices, in the order the dropdown shows them.
var providerRows = []string{store.SearchAuto, store.SearchSearXNG, store.SearchDuckDuckGo}

func providerRow(p string) int {
	for i, v := range providerRows {
		if v == p {
			return i
		}
	}
	return 0
}

func providerFromRow(i int) string {
	if i < 0 || i >= len(providerRows) {
		return store.SearchAuto
	}
	return providerRows[i]
}

// buildWebSearch is the search card.
func (a *App) buildWebSearch(f *settingsForm) *gtk.Box {
	outer, card := groupCard("Web Search")

	f.webSearch = gtk.NewCheckButton()
	f.webSearch.SetChild(wrappingLabel("Let the model look things up on the web"))
	f.webSearch.SetActive(a.cfg.WebSearch)
	card.Append(f.webSearch)

	hint := wrappingLabel("Used outside scenes, and only the searches themselves leave this machine.")
	hint.AddCSSClass("settings-hint")
	card.Append(hint)

	f.provider = gtk.NewDropDownFromStrings([]string{
		"Automatic: SearXNG When It Is Running, DuckDuckGo Otherwise",
		"SearXNG Only",
		"DuckDuckGo Only",
	})
	f.provider.SetSelected(uint(providerRow(a.cfg.SearchProvider)))
	card.Append(labelledField("Search With",
		"SearXNG is one you host yourself; DuckDuckGo needs no setup.",
		f.provider))

	f.searxngURL = gtk.NewEntry()
	f.searxngURL.SetText(a.cfg.SearXNGURL)
	f.searxngURL.SetPlaceholderText("http://localhost:8080")
	card.Append(labelledField("SearXNG Address",
		"Optional, and it needs \"json\" added to search.formats in settings.yml.",
		f.searxngURL))

	f.keepReading = gtk.NewCheckButton()
	f.keepReading.SetChild(wrappingLabel("Save the pages it reads to Knowledge"))
	f.keepReading.SetActive(a.cfg.KeepReading)
	card.Append(f.keepReading)

	f.searchN = gtk.NewEntry()
	f.searchN.SetText(fmt.Sprintf("%d", a.cfg.SearchResults))
	card.Append(labelledField("Results per Search",
		fmt.Sprintf("More to read, but less room for the chat (default %d).", store.DefaultSearchResults),
		f.searchN))

	status := wrappingLabel("")
	status.AddCSSClass("settings-hint")

	test := gtk.NewButtonWithLabel("Test It")
	test.SetHAlign(gtk.AlignStart)
	test.ConnectClicked(func() {
		probe := a.cfg
		probe.SearXNGURL = strings.TrimSpace(f.searxngURL.Text())
		probe.SearchProvider = providerFromRow(int(f.provider.Selected()))
		if probe.SearchProvider == store.SearchSearXNG && probe.SearXNGURL == "" {
			status.SetText("Put the address of a SearXNG instance in first.")
			return
		}
		provider := scene.SearchProvider(probe)
		test.SetSensitive(false)
		test.SetLabel("Searching…")
		status.SetText("")
		go func() {
			n, err := websearch.Probe(context.Background(), provider)
			coreglib.IdleAdd(func() bool {
				test.SetSensitive(true)
				test.SetLabel("Test It")
				switch {
				case err != nil:
					status.SetText(err.Error())
				case n == 0:
					status.SetText("It answered but found nothing, so check the instance's engines.")
				default:
					status.SetText(fmt.Sprintf("Working: %d results came back from %s.", n, provider.Name()))
				}
				return false
			})
		}()
	})
	card.Append(test)
	card.Append(status)

	// The embedding model belongs with the knowledge it indexes, which is
	// filled by the searching above as much as by anything written by hand.
	f.embedModel = gtk.NewEntry()
	f.embedModel.SetText(a.cfg.EmbeddingModel)
	f.embedModel.SetPlaceholderText("Automatic")
	card.Append(labelledField("Embedding Model for Knowledge",
		"Lets Knowledge search by meaning as well as by words.",
		f.embedModel))
	return outer
}
