package app

import (
	"context"
	"fmt"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
	"astral/internal/websearch"
)

// The web search settings.
//
// This is the one feature that sends anything off this machine, so the card says
// so plainly, it is off until switched on, and it needs an address before it will
// do anything at all. The address is somewhere you run yourself, which is the only
// arrangement that fits the rest of the app.

// buildWebSearch is the search card.
func (a *App) buildWebSearch(f *settingsForm) *gtk.Box {
	outer, card := groupCard("Web Search")

	f.webSearch = gtk.NewCheckButton()
	f.webSearch.SetChild(wrappingLabel("Let the model look things up on the web"))
	f.webSearch.SetActive(a.cfg.WebSearch)
	card.Append(f.webSearch)

	hint := wrappingLabel("For General Chat and the three designers only, never in a scene. " +
		"The model decides when it needs to search. Only the words it searches for leave this " +
		"machine: no part of your conversation, your characters or your worlds.")
	hint.AddCSSClass("settings-hint")
	card.Append(hint)

	f.searxngURL = gtk.NewEntry()
	f.searxngURL.SetText(a.cfg.SearXNGURL)
	f.searxngURL.SetPlaceholderText("http://localhost:8080")
	card.Append(labelledField("SearXNG Address",
		"SearXNG is a search engine you run yourself, with no account and no API key. "+
			"Run one with: docker run -d -p 8080:8080 searxng/searxng, then add \"json\" to "+
			"search.formats in its settings.yml and restart it.",
		f.searxngURL))

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
		addr := strings.TrimSpace(f.searxngURL.Text())
		if addr == "" {
			status.SetText("Put the address of a SearXNG instance in first.")
			return
		}
		test.SetSensitive(false)
		test.SetLabel("Searching…")
		status.SetText("")
		go func() {
			n, err := websearch.Probe(context.Background(), websearch.NewSearXNG(addr))
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
					status.SetText(fmt.Sprintf("Working: %d results came back.", n))
				}
				return false
			})
		}()
	})
	card.Append(test)
	card.Append(status)
	return outer
}
