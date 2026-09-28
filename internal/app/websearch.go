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

	hint := wrappingLabel("For General Chat and the three designers only, never in a scene. " +
		"The model decides when it needs to search, and may open a result to read it. What leaves " +
		"this machine is the words it searches for and the pages it opens: no part of your " +
		"conversation, your characters or your worlds.")
	hint.AddCSSClass("settings-hint")
	card.Append(hint)

	f.provider = gtk.NewDropDownFromStrings([]string{
		"Automatic: SearXNG When It Is Running, DuckDuckGo Otherwise",
		"SearXNG Only",
		"DuckDuckGo Only",
	})
	f.provider.SetSelected(uint(providerRow(a.cfg.SearchProvider)))
	card.Append(labelledField("Search With",
		"SearXNG is a search engine you run yourself and asks several engines at once. "+
			"DuckDuckGo needs nothing set up.",
		f.provider))

	f.searxngURL = gtk.NewEntry()
	f.searxngURL.SetText(a.cfg.SearXNGURL)
	f.searxngURL.SetPlaceholderText("http://localhost:8080")
	card.Append(labelledField("SearXNG Address",
		"Optional. Start one with "+
			"docker run -d -p 8080:8080 searxng/searxng, then add \"json\" to search.formats "+
			"in its settings.yml.",
		f.searxngURL))

	f.keepReading = gtk.NewCheckButton()
	f.keepReading.SetChild(wrappingLabel("Keep the pages it reads in Knowledge, so a subject looked up once is known next time"))
	f.keepReading.SetActive(a.cfg.KeepReading)
	card.Append(f.keepReading)

	f.searchN = gtk.NewEntry()
	f.searchN.SetText(fmt.Sprintf("%d", a.cfg.SearchResults))
	card.Append(labelledField("Results per Search",
		fmt.Sprintf("Higher gives the model more to read and leaves less room for the "+
			"conversation, lower is faster and may miss the answer. Default is %d.",
			store.DefaultSearchResults),
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
					status.SetText("It answered, but found nothing. That usually means its " +
						"engines are all failing; check the instance's own page.")
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
		"Lets Knowledge be searched by meaning as well as by words. Leave empty to use the first "+
			"embedding model installed; with none, it searches by words, which works everywhere. "+
			"A small one is enough: ollama pull embeddinggemma.",
		f.embedModel))
	return outer
}
