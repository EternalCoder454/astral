package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/knowledge"
	"astral/internal/scene"
	"astral/internal/store"
	"astral/internal/ui"
)

// The knowledge base, as a place you can see and change.
//
// Everything in it is used without being asked for, by General Chat and the
// designers, so it has to be somewhere you can check what is there, correct
// it, and add to it. A store the model draws from that nobody can read is a
// store nobody can trust.

// maxImportBytes bounds a text file brought in whole. Two megabytes of text is a
// book; anything bigger is not something to put in front of a model a chunk at
// a time anyway.
const maxImportBytes = 2 << 20

// originLabel is how each way into the knowledge base is shown on a card.
func originLabel(origin string) string {
	switch origin {
	case store.OriginWeb:
		return "From the Web"
	case store.OriginChat:
		return "From a Chat"
	case store.OriginFile:
		return "Imported"
	case store.OriginStudy:
		return "Study Notes"
	}
	return "Written"
}

// showKnowledge is the list, with search, and the ways to add to it.
func (a *App) showKnowledge() {
	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle("Knowledge")
	d.SetContentWidth(620)
	d.SetContentHeight(680)

	header := adw.NewHeaderBar()
	importBtn := gtk.NewButtonFromIconName(ui.IconFolder)
	importBtn.SetTooltipText("Import text or Markdown files")
	importBtn.ConnectClicked(func() {
		d.Close()
		a.importKnowledgeFiles()
	})
	header.PackStart(importBtn)

	newBtn := gtk.NewButtonFromIconName(ui.IconAdd)
	newBtn.SetTooltipText("Write an entry")
	newBtn.ConnectClicked(func() {
		d.Close()
		a.editKnowledge(store.KnowledgeEntry{})
	})
	header.PackEnd(newBtn)

	studyBtn := gtk.NewButtonFromIconName(ui.IconSearch)
	studyBtn.SetTooltipText("Study a topic on the web and write notes")
	studyBtn.ConnectClicked(func() {
		d.Close()
		a.studyTopic("")
	})
	header.PackEnd(studyBtn)

	page := gtk.NewBox(gtk.OrientationVertical, 10)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)

	about := wrappingLabel("")
	about.AddCSSClass("settings-hint")
	page.Append(about)
	a.describeKnowledge(about)

	search := gtk.NewSearchEntry()
	search.SetPlaceholderText("Search by what an entry is about")
	page.Append(search)

	list := gtk.NewBox(gtk.OrientationVertical, 8)
	page.Append(list)

	entries, err := a.store.KnowledgeEntries()
	if err != nil {
		a.toast("Could not read the knowledge base: " + err.Error())
	}
	byID := make(map[int64]store.KnowledgeEntry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}

	fill := func(shown []store.KnowledgeEntry, query string) {
		for child := list.FirstChild(); child != nil; child = list.FirstChild() {
			list.Remove(child)
		}
		if len(shown) == 0 {
			var msg string
			if query == "" {
				msg = "Nothing here yet, but chats outside scenes use what you add."
			} else {
				msg = "Nothing matches that."
			}
			empty := gtk.NewLabel(msg)
			empty.SetWrap(true)
			empty.SetJustify(gtk.JustifyCenter)
			empty.SetMarginTop(24)
			empty.AddCSSClass("dim-label")
			list.Append(empty)
			if query == "" {
				list.Append(addRow("Write an Entry", func() {
					d.Close()
					a.editKnowledge(store.KnowledgeEntry{})
				}))
				list.Append(addRow("Study a Topic", func() {
					d.Close()
					a.studyTopic("")
				}))
			}
			return
		}
		for _, e := range shown {
			list.Append(a.knowledgeCard(e, d))
		}
		if query == "" {
			list.Append(addRow("Write an Entry", func() {
				d.Close()
				a.editKnowledge(store.KnowledgeEntry{})
			}))
		}
	}
	fill(entries, "")

	// Searched the way a conversation searches it, by what an entry is about
	// rather than by its title, so what you see here is what the model would
	// be shown. Titles are matched as well, for the one-word query the text
	// search ignores as too short.
	search.ConnectSearchChanged(func() {
		q := strings.TrimSpace(search.Text())
		if q == "" {
			fill(entries, "")
			return
		}
		seen := map[int64]bool{}
		var shown []store.KnowledgeEntry
		if hits, err := a.store.SearchKnowledgeText(q, 60); err == nil {
			for _, h := range hits {
				if e, ok := byID[h.EntryID]; ok && !seen[h.EntryID] {
					seen[h.EntryID] = true
					shown = append(shown, e)
				}
			}
		}
		low := strings.ToLower(q)
		for _, e := range entries {
			if !seen[e.ID] && strings.Contains(strings.ToLower(e.Title), low) {
				seen[e.ID] = true
				shown = append(shown, e)
			}
		}
		fill(shown, q)
	})

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
	search.GrabFocus()
}

// describeKnowledge says how many entries there are and how they are searched,
// filled in off the UI thread because finding an embedding model asks the
// server.
func (a *App) describeKnowledge(l *gtk.Label) {
	n := a.store.KnowledgeCount()
	base := fmt.Sprintf("%d %s, ", n, plural(n, "entry", "entries"))
	l.SetText(base + "searched by their words.")
	client, cfg := a.client, a.cfg
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		model := scene.EmbedModel(ctx, client, cfg)
		coreglib.IdleAdd(func() bool {
			if model != "" {
				l.SetText(base + "searched by words and by meaning with " + model + ".")
			} else {
				l.SetText(base + "searched by words until you install an embedding model.")
			}
			return false
		})
	}()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// knowledgeCard is one entry in the list.
func (a *App) knowledgeCard(e store.KnowledgeEntry, parent *adw.Dialog) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("character-card")

	col := gtk.NewBox(gtk.OrientationVertical, 3)
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	title := gtk.NewLabel(e.Title)
	title.SetXAlign(0)
	title.SetHExpand(true)
	title.SetEllipsize(pango.EllipsizeEnd)
	title.AddCSSClass("character-card-name")
	head.Append(title)
	origin := gtk.NewLabel(originLabel(e.Origin))
	origin.AddCSSClass("character-card-tag")
	head.Append(origin)
	col.Append(head)

	col.Append(cardDescription(plainSnippet(e.Body, 240)))

	var meta []string
	if e.Source != "" {
		meta = append(meta, e.Source)
	}
	if !e.UpdatedAt.IsZero() {
		meta = append(meta, agoText(e.UpdatedAt))
	}
	if len(e.Tags) > 0 {
		meta = append(meta, strings.Join(e.Tags, ", "))
	}
	if len(meta) > 0 {
		m := gtk.NewLabel(strings.Join(meta, " · "))
		m.SetXAlign(0)
		m.SetEllipsize(pango.EllipsizeEnd)
		m.AddCSSClass("settings-hint")
		col.Append(m)
	}
	btn.SetChild(col)
	btn.SetTooltipText("Open " + e.Title)
	entry := e
	btn.ConnectClicked(func() {
		parent.Close()
		a.editKnowledge(entry)
	})
	return btn
}

// editKnowledge is the editor for one entry, new when its ID is zero.
func (a *App) editKnowledge(e store.KnowledgeEntry) {
	d := adw.NewDialog()
	ui.FreeOnClose(d)
	if e.ID == 0 {
		d.SetTitle("New Entry")
	} else {
		d.SetTitle("Edit Entry")
	}
	d.SetContentWidth(640)
	d.SetContentHeight(720)

	title := gtk.NewEntry()
	title.SetText(e.Title)
	title.SetPlaceholderText("What this is about")
	tags := gtk.NewEntry()
	tags.SetText(strings.Join(e.Tags, ", "))
	tags.SetPlaceholderText("tides, kestrel bay")
	source := gtk.NewEntry()
	source.SetText(e.Source)
	source.SetPlaceholderText("https://")
	frame, body := multilineField(e.Body, 16)
	frame.SetVExpand(true)

	header := saveHeader(d, "Save the entry", func() bool {
		next := e
		next.Title = strings.TrimSpace(title.Text())
		next.Body = textOf(body)
		next.Source = strings.TrimSpace(source.Text())
		next.Tags = strings.Split(tags.Text(), ",")
		if next.Origin == "" {
			next.Origin = store.OriginWritten
		}
		if _, err := a.store.SaveKnowledge(next); err != nil {
			a.toast("Could not save: " + err.Error())
			return false
		}
		a.indexKnowledge()
		a.showKnowledge()
		return true
	})

	page := gtk.NewBox(gtk.OrientationVertical, 14)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)
	page.Append(labelledField("Title", "", title))
	page.Append(labelledField("Text",
		"Specific facts, names and numbers; long entries are fine.", frame))
	page.Append(labelledField("Tags", "Optional, separated by commas.", tags))
	page.Append(labelledField("Source", "Optional, where it came from.", source))

	if e.ID != 0 {
		del := gtk.NewButtonWithLabel("Delete Entry")
		del.AddCSSClass("destructive-action")
		del.SetHAlign(gtk.AlignStart)
		del.ConnectClicked(func() {
			a.confirm("Delete "+e.Title, "It will no longer be used by any conversation.", "Delete", func() {
				if err := a.store.DeleteKnowledge(e.ID); err != nil {
					a.toast("Could not delete: " + err.Error())
					return
				}
				d.Close()
				a.showKnowledge()
			})
		})
		page.Append(del)
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
	if e.ID == 0 {
		title.GrabFocus()
	}
}

// studyTopic asks for a subject and studies it, showing each step.
func (a *App) studyTopic(initial string) {
	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle("Study a Topic")
	d.SetContentWidth(520)

	topic := gtk.NewEntry()
	topic.SetText(initial)
	topic.SetPlaceholderText("Ollama keep_alive, the history of lighthouses, …")
	topic.SetActivatesDefault(true)

	status := wrappingLabel("")
	status.AddCSSClass("settings-hint")

	start := gtk.NewButtonWithLabel("Study It")
	start.AddCSSClass("suggested-action")
	start.SetHAlign(gtk.AlignEnd)

	open := gtk.NewButtonWithLabel("Open the Notes")
	open.SetHAlign(gtk.AlignEnd)
	open.SetVisible(false)

	var cancel context.CancelFunc
	d.ConnectClosed(func() {
		if cancel != nil {
			cancel()
		}
	})

	run := func() {
		subject := strings.TrimSpace(topic.Text())
		if subject == "" {
			status.SetText("Say what to study first.")
			return
		}
		model := a.cfg.Model
		if model == "" {
			status.SetText("Choose a model in Settings first: it writes the notes.")
			return
		}
		start.SetSensitive(false)
		topic.SetSensitive(false)
		status.SetText("Starting…")

		var ctx context.Context
		ctx, cancel = context.WithTimeout(context.Background(), 6*time.Minute)
		client, st, cfg := a.client, a.store, a.cfg
		provider, fetcher := scene.SearchProvider(cfg), scene.Fetcher()
		go func() {
			model := scene.FitHousekeeping(ctx, client, cfg.HousekeepingModel, model)
			result, err := knowledge.Study(ctx, st, client, model, provider, fetcher, subject, func(s string) {
				coreglib.IdleAdd(func() bool { status.SetText(s); return false })
			})
			coreglib.IdleAdd(func() bool {
				start.SetSensitive(true)
				topic.SetSensitive(true)
				switch {
				case err != nil && len(result.Pages) > 0:
					status.SetText(fmt.Sprintf("Kept %d %s: %s", len(result.Pages),
						plural(len(result.Pages), "page", "pages"), err.Error()))
				case err != nil:
					status.SetText(err.Error())
				default:
					status.SetText(fmt.Sprintf("Done: read %d %s and wrote notes from them.",
						len(result.Pages), plural(len(result.Pages), "page", "pages")))
					noteID := result.NoteID
					open.SetVisible(true)
					open.ConnectClicked(func() {
						d.Close()
						if e, err := a.store.Knowledge(noteID); err == nil {
							a.editKnowledge(e)
						}
					})
				}
				a.indexKnowledge()
				return false
			})
		}()
	}
	start.ConnectClicked(run)
	topic.ConnectActivate(run)

	page := gtk.NewBox(gtk.OrientationVertical, 12)
	page.SetMarginTop(14)
	page.SetMarginBottom(18)
	page.SetMarginStart(18)
	page.SetMarginEnd(18)
	intro := wrappingLabel("Astral reads the best pages on it and writes notes to keep.")
	intro.AddCSSClass("settings-hint")
	page.Append(intro)
	page.Append(labelledField("Topic", "", topic))
	buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
	buttons.SetHAlign(gtk.AlignEnd)
	buttons.Append(open)
	buttons.Append(start)
	page.Append(buttons)
	page.Append(status)

	tv := adw.NewToolbarView()
	tv.AddTopBar(adw.NewHeaderBar())
	tv.SetContent(scrolledToFit(page))
	d.SetChild(tv)
	d.Present(a.win)
	topic.GrabFocus()
}

// importKnowledgeFiles brings text files in as entries, one each.
func (a *App) importKnowledgeFiles() {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Import Notes")
	text := gtk.NewFileFilter()
	text.SetName("Text and Markdown")
	for _, p := range []string{"*.txt", "*.md", "*.markdown", "*.text"} {
		text.AddPattern(p)
	}
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(text.Object)
	dialog.SetFilters(filters)

	dialog.OpenMultiple(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		files, err := dialog.OpenMultipleFinish(res)
		if err != nil || files == nil {
			return // cancelled
		}
		added, failed := 0, 0
		for i := uint(0); i < files.NItems(); i++ {
			obj := files.Item(i)
			f, ok := obj.Cast().(*gio.File)
			if !ok {
				failed++
				continue
			}
			if err := a.importKnowledgeFile(f.Path()); err != nil {
				failed++
				continue
			}
			added++
		}
		switch {
		case failed > 0:
			a.toast(fmt.Sprintf("Imported %d, and %d could not be read.", added, failed))
		default:
			a.toast(fmt.Sprintf("Imported %d %s.", added, plural(added, "file", "files")))
		}
		a.indexKnowledge()
		a.showKnowledge()
	})
}

func (a *App) importKnowledgeFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > maxImportBytes {
		return fmt.Errorf("%s is too large", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	_, err = a.store.SaveKnowledgeBySource(store.KnowledgeEntry{
		Title:  name,
		Body:   string(data),
		Source: path,
		Origin: store.OriginFile,
	})
	return err
}

// SaveToKnowledge keeps a reply from a conversation. Wired to the message
// action on the replies of General Chat and the designers.
func (a *App) saveReplyToKnowledge(text string, chatTitle string, chatID int64) {
	title := strings.TrimSpace(chatTitle)
	if title == "" {
		title = "Saved from a Chat"
	}
	id, err := a.store.SaveKnowledge(store.KnowledgeEntry{
		Title:  title,
		Body:   text,
		Source: fmt.Sprintf("chat %d", chatID),
		Origin: store.OriginChat,
	})
	if err != nil {
		a.toast("Could not save it: " + err.Error())
		return
	}
	a.indexKnowledge()
	a.toastAction("Saved to Knowledge.", "Open", func() {
		if e, err := a.store.Knowledge(id); err == nil {
			a.editKnowledge(e)
		}
	})
}

// indexKnowledge embeds what has not been embedded yet, in the background,
// when there is an embedding model to do it with. Nothing happens otherwise:
// the text index is kept up to date as entries are saved, and needs no pass.
func (a *App) indexKnowledge() {
	client, st, cfg := a.client, a.store, a.cfg
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		model := scene.EmbedModel(ctx, client, cfg)
		if model == "" {
			return
		}
		if _, err := knowledge.IndexPending(ctx, st, client, model); err != nil {
			fmt.Fprintf(os.Stderr, "astral: embedding the knowledge base: %v\n", err)
		}
	}()
}

// plainSnippet is the start of a text on one line, as written.
//
// Not ui.Snippet, which reads roleplay markup and strips the markers, so an
// entry about keep_alive previewed as "keepalive" and OLLAMA_KEEP_ALIVE as
// OLLAMAKEEPALIVE. Knowledge is as often technical as it is prose, and an
// underscore in it is part of the word.
func plainSnippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	if i := strings.LastIndexByte(cut, ' '); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
