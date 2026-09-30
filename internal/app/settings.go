package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/chars"
	"astral/internal/store"
	"astral/internal/ui"
	"astral/internal/websearch"
)

// settingsForm holds the widgets Save reads back.
type settingsForm struct {
	baseURL *gtk.Entry
	model   *gtk.DropDown
	models  []string

	keepAlive *gtk.Entry

	fontMode *gtk.DropDown
	showStat *gtk.CheckButton
	updates  *gtk.CheckButton
	channel  *gtk.DropDown
	phone    *gtk.CheckButton
	think    *gtk.CheckButton

	webSearch   *gtk.CheckButton
	provider    *gtk.DropDown
	searxngURL  *gtk.Entry
	searchN     *gtk.Entry
	keepReading *gtk.CheckButton
	embedModel  *gtk.Entry

	temperature *gtk.Scale
	topP        *gtk.Scale
	repeat      *gtk.Scale
	numCtx      *gtk.Entry
	numPredict  *gtk.Entry

	housekeeping *gtk.DropDown
	vision       *gtk.DropDown
	notify       *gtk.CheckButton
}

// showSettings opens the settings dialog.
//
// It applies on an explicit Save rather than instantly. Several of these
// settings change what the model does mid-scene, and having a stray scroll
// over a slider quietly alter the temperature of a conversation you are in the
// middle of is not a trade worth making for one fewer click. The colour theme is
// the exception: it cannot be judged without seeing it, so it applies and saves
// the moment it is chosen, and Save and Cancel leave it alone.
func (a *App) showSettings() { a.showSettingsPage("") }

// showSettingsPage opens settings on a particular page, so a menu entry can
// land somewhere specific instead of wherever the dialog opens by default.
func (a *App) showSettingsPage(page string) {
	// Asked again on the way in. The list was only ever fetched at launch and
	// after a save, so a model pulled while Astral was open did not exist as
	// far as the window was concerned until it was restarted, which looks
	// exactly like the model not being installed.
	a.probeModels()

	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle("Settings")
	// Wider than the pages need. The dialog folds under 500sp, and sp scales with
	// the text: at a text scale of 1.5, which is what a HiDPI desktop commonly
	// runs, 500sp is 750px, and a dialog of 700px would open already folded.
	d.SetContentWidth(800)
	d.SetContentHeight(760)
	// A dialog with a breakpoint does not work out its own minimum size from what
	// is in it, because the breakpoint changes what that is, so it has to be told.
	// This is the narrowest and shortest the pages can be at, folded.
	d.SetSizeRequest(300, 300)

	f := &settingsForm{}
	picker := a.newThemePicker()
	stack := gtk.NewStack()
	stack.SetHExpand(true)
	// A stack is as wide as its widest page by default, hidden ones included, so
	// the narrowest the dialog could be was set by whichever page happened to be
	// widest. Each page takes its own width instead.
	stack.SetHhomogeneous(false)
	// Four pages. Appearance is first because it is about the window rather than
	// the model, and Model stays the one the dialog opens on. Phone and the
	// display options live on You, which is about you and this machine.
	stack.AddTitled(scrolled(a.buildAppearancePage(picker)), "appearance", "Appearance")
	stack.AddTitled(scrolled(a.buildModelPage(f)), "model", "Model")
	stack.AddTitled(scrolled(a.buildYouPage(f)), "you", "You")
	stack.AddTitled(scrolled(a.buildAboutPage(f)), "about", "About")

	// The list of pages and the page itself, as a split view that folds.
	//
	// It was a fixed 150px column beside the page in a plain box. On a window
	// narrower than the dialog the column kept its width and the page was cut off
	// down its right-hand side, the end of every hint and value. Folded, the list
	// is one screen and each page another, with a back button between them, which
	// is how libadwaita's own preferences behave.
	list := gtk.NewListBox()
	list.AddCSSClass("navigation-sidebar")
	list.AddCSSClass("settings-nav")
	titles := map[string]string{}
	for _, id := range []string{"appearance", "model", "you", "about"} {
		title := stack.Page(stack.ChildByName(id)).Title()
		titles[id] = title
		row := gtk.NewListBoxRow()
		row.SetName(id)
		label := gtk.NewLabel(title)
		label.SetXAlign(0)
		row.SetChild(label)
		list.Append(row)
	}

	sideScroll := gtk.NewScrolledWindow()
	sideScroll.SetChild(list)
	sideScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)

	// The page's own bar exists for the back button, so it is only there while
	// folded. Save and Cancel sit above both screens in the dialog's own bar, and
	// are reachable from the list as well as from a page.
	pageBar := adw.NewHeaderBar()
	pageBar.AddCSSClass("flat")
	pageBar.SetShowStartTitleButtons(false)
	pageBar.SetShowEndTitleButtons(false)
	pageBar.SetVisible(false)
	pageView := adw.NewToolbarView()
	pageView.AddTopBar(pageBar)
	pageView.SetContent(stack)
	contentPage := adw.NewNavigationPage(pageView, titles["model"])

	split := adw.NewNavigationSplitView()
	split.SetSidebar(adw.NewNavigationPage(sideScroll, "Settings"))
	split.SetContent(contentPage)
	// In pixels, whose default unit scales with the text: on a desktop with large
	// fonts that takes the width from the page, which is the part with something
	// to read.
	split.SetSidebarWidthUnit(adw.LengthUnitPx)
	split.SetMinSidebarWidth(180)
	split.SetMaxSidebarWidth(220)

	// Fold below 500sp. The unit matters: sp scales with the text, so the pages
	// fold when there is no longer room to read them, whatever the pixel width.
	bp := adw.NewBreakpoint(adw.NewBreakpointConditionLength(
		adw.BreakpointConditionMaxWidth, 500, adw.LengthUnitSp))
	d.AddBreakpoint(bp)

	// The breakpoint folds the split view and brings up the page's bar with its
	// back button. As setters rather than as handlers: they hold no Go closure, so
	// there is nothing to cut when the dialog closes, and they put the two back on
	// their own when the dialog widens again.
	bp.AddSetter(split, "collapsed", true)
	bp.AddSetter(pageBar, "visible", true)

	// Every handler below reaches something above the widget it is on, so each is
	// cut when the dialog closes; see themePicker.release for why.
	var conns []connection
	track := func(obj coreglib.Objector, h coreglib.SignalHandle) {
		conns = append(conns, connection{obj, h})
	}
	track(list, list.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row != nil {
			stack.SetVisibleChildName(row.Name())
			contentPage.SetTitle(titles[row.Name()])
		}
	}))
	// Selecting a row switches the page; activating one, by a click or Enter,
	// also moves to it when folded. They are separate because opening the dialog
	// selects a row, and on a narrow window that should land on the list, not
	// jump straight past it.
	track(list, list.ConnectRowActivated(func(*gtk.ListBoxRow) { split.SetShowContent(true) }))
	d.ConnectClosed(func() {
		for _, c := range conns {
			c.cut()
		}
		picker.release()
		// The dialog holds the breakpoint, and Go's hold on it is not one the
		// collector can see the end of, so it is let go here: measured, it was the
		// one thing a Settings dialog kept after closing.
		coreglib.Destroy(bp)
	})

	// The page asked for, or Model, which is what the dialog opens on: it is the
	// one most visits are for, and it is not first in the list.
	selected := -1
	for i := 0; ; i++ {
		row := list.RowAtIndex(i)
		if row == nil {
			break
		}
		if row.Name() == page || (selected < 0 && row.Name() == "model") {
			selected = i
		}
	}
	list.SelectRow(list.RowAtIndex(selected))
	if page != "" {
		split.SetShowContent(true) // asked for a page, so show it even when folded
	}

	header := saveHeader(d, "Save the changes on every page", func() bool {
		a.applySettings(f)
		return true
	})

	// A list of pages beside a single Save leaves a fair question open: does Save
	// mean this page or all of them? It means all of them, so it says so where the
	// button is rather than leaving it to be discovered. It wraps, because a
	// sentence this long would otherwise set a minimum width wider than a phone.
	scope := gtk.NewLabel("Changes on every page are saved together, " +
		"except the theme, which applies as soon as you choose it.")
	scope.AddCSSClass("settings-hint")
	scope.SetWrap(true)
	scope.SetMarginTop(6)
	scope.SetMarginBottom(6)
	scope.SetMarginStart(14)
	scope.SetMarginEnd(14)
	scope.SetXAlign(0)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.AddBottomBar(scope)
	tv.SetContent(split)
	d.SetChild(tv)
	d.Present(a.win)
}

// buildAppearancePage is how Astral looks. Its one card applies as you choose,
// unlike the rest of the dialog.
func (a *App) buildAppearancePage(p *themePicker) *gtk.Box {
	page := settingsPage()
	page.Append(p.box)
	return page
}

func (a *App) buildModelPage(f *settingsForm) *gtk.Box {
	page := settingsPage()

	outer, card := groupCard("Model")
	// A dropdown of what is actually installed, rather than a text field.
	// "Run any local model" should be a menu; making you type an exact tag
	// from memory turns a feature into a spelling test.
	f.models = a.modelNames()
	f.model = gtk.NewDropDownFromStrings(f.modelLabels(a))
	shrinkable(f.model)
	f.model.SetSelected(uint(indexOf(f.models, a.cfg.Model)))
	f.model.SetHExpand(true)
	// A dropdown of installed models needs no caption saying it lists installed
	// models. The only thing worth saying here is what to do when it is empty.
	hint := ""
	if len(f.models) == 0 {
		hint = "Run `ollama pull qwen3:8b`, then press Check Again."
	}

	// The refresh sits on the same row as the thing it refreshes. Under it, on
	// its own line, it read as an instruction belonging to neither the field
	// above nor the field below.
	modelRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	modelRow.Append(f.model)
	refresh := gtk.NewButtonWithLabel("Check Again")
	refresh.SetTooltipText("Ask Ollama again which models it has")
	refresh.ConnectClicked(func() {
		a.probeModels()
		a.toast("Checking Ollama…")
	})
	modelRow.Append(refresh)
	card.Append(labelledField("Default Model", hint, modelRow))

	// The recap and the lorebook pass are bookkeeping, not prose, and a much
	// smaller model does them about as well in a fraction of the time. They
	// also run in the background after a reply, so whatever they use is what
	// the next message has to queue behind.
	f.housekeeping = gtk.NewDropDownFromStrings(f.housekeepingLabels(a))
	shrinkable(f.housekeeping)
	// Not indexOf: it answers 0 for a miss, which is the right fallback for the
	// scene's model (use the first installed one) and the wrong one here, where
	// row 0 already means something and an unset value would silently select a
	// background model nobody chose.
	f.housekeeping.SetSelected(uint(housekeepingRow(f.models, a.cfg.HousekeepingModel)))
	card.Append(labelledField("Background Model",
		"Writes recaps and lore when it fits beside your main model.",
		f.housekeeping))

	// Same shape as the background model: row 0 is the automatic choice,
	// which is what an empty setting means.
	f.vision = gtk.NewDropDownFromStrings(f.visionLabels(a))
	shrinkable(f.vision)
	f.vision.SetSelected(uint(housekeepingRow(f.models, a.cfg.VisionModel)))
	card.Append(labelledField("Image Model",
		"Looks at the pictures you add to a chat.",
		f.vision))

	f.baseURL = gtk.NewEntry()
	f.baseURL.SetText(a.cfg.BaseURL)
	card.Append(labelledField("Ollama Address",
		"Change this only if Ollama is not on its default port.",
		f.baseURL))
	page.Append(outer)

	// Sampling.
	sOuter, sCard := groupCard("Sampling")
	f.temperature = newSlider(0, 2, 0.05, a.cfg.Temperature)
	sCard.Append(labelledField("Temperature",
		fmt.Sprintf("Higher is wilder, lower is steadier (default %.2f).", store.DefaultTemperature),
		f.temperature))

	f.topP = newSlider(0.1, 1, 0.01, a.cfg.TopP)
	sCard.Append(labelledField("Top-p",
		fmt.Sprintf("Lower keeps word choices nearer the obvious (default %.2f).", store.DefaultTopP),
		f.topP))

	f.repeat = newSlider(1, 1.5, 0.01, a.cfg.RepeatPenalty)
	sCard.Append(labelledField("Repetition Penalty",
		fmt.Sprintf("Higher avoids repeated phrases but can sound forced (default %.2f).",
			store.DefaultRepeatPenalty),
		f.repeat))

	f.numCtx = gtk.NewEntry()
	f.numCtx.SetText(fmt.Sprintf("%d", a.cfg.NumCtx))
	sCard.Append(labelledField("Context Size (tokens)",
		fmt.Sprintf("Higher remembers more of the scene but uses more memory (default %d).",
			store.DefaultNumCtx),
		f.numCtx))

	// The reply limit was already referenced by the "the model spent its whole
	// limit thinking, raise it in Settings" message, and by the budget that
	// reserves room for a reply, but there was nowhere in Settings to raise it.
	f.numPredict = gtk.NewEntry()
	f.numPredict.SetText(fmt.Sprintf("%d", a.cfg.NumPredict))
	f.numPredict.SetPlaceholderText(fmt.Sprintf("%d", chars.DefaultReplyTokens))
	sCard.Append(labelledField("Reply Limit (tokens)",
		fmt.Sprintf("The longest reply allowed, or 0 for the default of %d.", chars.DefaultReplyTokens),
		f.numPredict))

	f.keepAlive = gtk.NewEntry()
	f.keepAlive.SetText(a.cfg.KeepAlive)
	f.keepAlive.SetPlaceholderText("Ollama's setting")
	sCard.Append(labelledField("Keep the Model Loaded For",
		"For example \"30m\" or \"2h\", or \"-1\" to never unload.",
		f.keepAlive))

	f.think = gtk.NewCheckButton()
	f.think.SetChild(wrappingLabel("Let reasoning models think before they reply"))
	f.think.SetActive(a.cfg.Think)
	sCard.Append(f.think)
	page.Append(sOuter)

	// On this page rather than on yours: what the model can reach is a fact
	// about the model.
	page.Append(a.buildWebSearch(f))

	return page
}

// buildAboutPage is which version this is and how it gets the next one.
//
// Its own page rather than a card on Appearance, where it first landed: an update
// is not a matter of how the app looks, and the version number is the thing
// anyone reporting a problem is asked for, so it should be somewhere you would
// think to look for it. Called About rather than Updates because that is where
// people look for a version number.
func (a *App) buildAboutPage(f *settingsForm) *gtk.Box {
	page := settingsPage()
	upOuter, upCard := groupCard("Version")

	ver := gtk.NewLabel("Astral " + version)
	ver.SetXAlign(0)
	ver.SetSelectable(true) // so it can be copied into a bug report
	ver.AddCSSClass("field-label")
	upCard.Append(ver)

	f.updates = gtk.NewCheckButton()
	f.updates.SetChild(wrappingLabel("Check for a new version when Astral starts"))
	f.updates.SetActive(a.cfg.CheckUpdates)
	upCard.Append(f.updates)

	f.channel = gtk.NewDropDownFromStrings([]string{"Release", "Beta"})
	if a.cfg.UpdateChannel == store.ChannelBeta {
		f.channel.SetSelected(1)
	}
	upCard.Append(labelledField("Channel",
		"Beta gets changes sooner but may be rough.",
		f.channel))

	check := gtk.NewButtonWithLabel("Check Now")
	check.SetHAlign(gtk.AlignStart)
	check.ConnectClicked(func() {
		a.applySettings(f)
		a.checkForUpdateNow()
	})
	upCard.Append(check)
	page.Append(upOuter)

	backOuter, backCard := groupCard("Backups")
	backHint := wrappingLabel("A copy of your library is kept for each of the last seven days.")
	backHint.AddCSSClass("settings-hint")
	backCard.Append(backHint)
	restore := gtk.NewButtonWithLabel("Restore a Backup…")
	restore.ConnectClicked(a.showRestoreBackup)
	open := gtk.NewButtonWithLabel("Open Backups Folder")
	open.ConnectClicked(func() {
		dir := store.BackupDir()
		_ = os.MkdirAll(dir, 0o755)
		gtk.NewFileLauncher(gio.NewFileForPath(dir)).Launch(context.Background(), &a.win.Window, nil)
	})
	backCard.Append(buttonRow(restore, open))
	page.Append(backOuter)
	return page
}

// buildYouPage is everything about you and this machine: who you play as, the
// rules you play under, how text is drawn, and which phone may use it.
//
// One page rather than three. Display was three switches and Phone was one
// switch with a device list, and a sidebar trip to reach either of them bought
// nothing; what the three have in common is that none of them is about the model.
// The colour theme is the exception, and has a page of its own.
func (a *App) buildYouPage(f *settingsForm) *gtk.Box {
	page := settingsPage()

	// Who you are lives in Personas now, where there can be several of you,
	// each with an age, a race, an appearance and the rest in fields of
	// their own. This says who is in use and goes there.
	outer, card := groupCard("Personas")
	inUse := "Nobody yet, so characters don't know who you are."
	if p, err := a.store.Persona(a.cfg.ActivePersona); err == nil {
		inUse = p.DisplayName()
		if f := p.Facts(); f != "" {
			inUse += ", " + f
		}
	}
	who := wrappingLabel(inUse)
	who.AddCSSClass("field-label")
	card.Append(labelledField("In Use",
		"New chats are played as this persona.",
		who))
	manageP := gtk.NewButtonWithLabel("Manage Personas…")
	manageP.ConnectClicked(func() { a.showPersonas() })
	createP := gtk.NewButtonWithLabel("Create a Persona…")
	createP.ConnectClicked(func() { a.newPersonaDesignerChat() })
	card.Append(buttonRow(manageP, createP))
	page.Append(outer)

	styleOuter, styleCard := groupCard("Writing Style")
	cur := gtk.NewLabel(a.cfg.Style().Name)
	cur.SetXAlign(0)
	cur.AddCSSClass("field-label")
	styleCard.Append(labelledField("In Use",
		"Controls how the prose sounds, not how it is formatted.",
		cur))
	manage := gtk.NewButtonWithLabel("Manage Writing Styles…")
	manage.SetHAlign(gtk.AlignStart)
	manage.ConnectClicked(func() { a.showStyles() })
	styleCard.Append(manage)
	page.Append(styleOuter)

	page.Append(a.buildRulebook())

	// Not called Appearance: the theme has a page of that name of its own.
	appOuter, appCard := groupCard("Display")
	f.fontMode = gtk.NewDropDownFromStrings([]string{"Automatic", "Crisp (1080p Screens)", "Smooth (HiDPI Screens)"})
	f.fontMode.SetSelected(uint(fontIndex(a.cfg.FontRendering)))
	appCard.Append(labelledField("Text Rendering",
		"Change this if text looks soft or unevenly spaced.",
		f.fontMode))

	f.showStat = gtk.NewCheckButton()
	f.showStat.SetChild(wrappingLabel("Show speed and token count under each reply"))
	f.showStat.SetActive(a.cfg.ShowStats)
	appCard.Append(f.showStat)

	f.notify = gtk.NewCheckButton()
	f.notify.SetChild(wrappingLabel("Tell me when a reply finishes while Astral is in the background"))
	f.notify.SetActive(a.cfg.NotifyReplies)
	appCard.Append(f.notify)
	page.Append(appOuter)

	// The phone cards, built where they have always been built so the pairing
	// code and the device list keep their own file.
	for _, card := range a.phoneCards(f) {
		page.Append(card)
	}
	return page
}

// applySettings reads the form into the config, saves it, and pushes the
// changes into the live objects that already exist.
func (a *App) applySettings(f *settingsForm) {
	if i := int(f.model.Selected()); i >= 0 && i < len(f.models) {
		a.cfg.Model = f.models[i]
	}
	// Row zero is "same as the scene", which is what an empty setting means.
	if i := int(f.housekeeping.Selected()) - 1; i >= 0 && i < len(f.models) {
		a.cfg.HousekeepingModel = f.models[i]
	} else {
		a.cfg.HousekeepingModel = ""
	}
	if i := int(f.vision.Selected()) - 1; i >= 0 && i < len(f.models) {
		a.cfg.VisionModel = f.models[i]
	} else {
		a.cfg.VisionModel = ""
	}
	if u := strings.TrimSpace(f.baseURL.Text()); u != "" {
		a.cfg.BaseURL = u
	}
	// Empty is a real choice now, the server's setting, so it is saved rather
	// than ignored.
	a.cfg.KeepAlive = strings.TrimSpace(f.keepAlive.Text())
	a.cfg.FontRendering = fontFromIndex(int(f.fontMode.Selected()))
	a.cfg.ShowStats = f.showStat.Active()
	a.cfg.NotifyReplies = f.notify.Active()
	a.cfg.CheckUpdates = f.updates.Active()
	a.cfg.UpdateChannel = store.ChannelRelease
	if f.channel.Selected() == 1 {
		a.cfg.UpdateChannel = store.ChannelBeta
	}
	a.cfg.SearXNGURL = strings.TrimSpace(f.searxngURL.Text())
	// The switch alone. It used to need an address too, from before search had
	// anywhere to go without one, and that turned search off on every save for
	// anyone not running SearXNG.
	a.cfg.WebSearch = f.webSearch.Active()
	a.cfg.SearchProvider = providerFromRow(int(f.provider.Selected()))
	a.cfg.KeepReading = f.keepReading.Active()
	a.cfg.EmbeddingModel = strings.TrimSpace(f.embedModel.Text())
	a.cfg.SearchResults = websearch.ParseResultCount(f.searchN.Text())
	a.cfg.Think = f.think.Active()
	a.cfg.Temperature = f.temperature.Value()
	a.cfg.TopP = f.topP.Value()
	a.cfg.RepeatPenalty = f.repeat.Value()
	if n := atoiOr(f.numCtx.Text(), a.cfg.NumCtx); n > 0 {
		a.cfg.NumCtx = n
	}
	if n := atoiOr(f.numPredict.Text(), a.cfg.NumPredict); n >= 0 {
		a.cfg.NumPredict = n
	}

	if err := store.SaveConfig(a.cfg); err != nil {
		a.toast("Could not save settings: " + err.Error())
	}

	// Push into the live objects. The client is rebuilt rather than mutated
	// because its base URL is baked into an http.Client at construction.
	a.client = ollamaClientFor(a.cfg.BaseURL)
	a.client.KeepAlive = a.cfg.KeepAlive
	a.applyFontRendering()
	if a.chat != nil {
		a.chat.SetClient(a.client)
		a.chat.SetConfig(a.cfg)
		a.refreshAttachAvailability()
	}
	if a.sidebar != nil {
		a.refreshProfile()
	}
	a.probeModels()
}

// showModelPicker is the quick switcher reached from the composer and Ctrl+M.
func (a *App) showModelPicker() {
	if len(a.models) == 0 {
		a.probeModels()
		if a.probeErr != nil {
			a.toast("Ollama isn't running: start it with `ollama serve`.")
		} else {
			a.toast("No models installed: pull one with `ollama pull qwen3:8b`.")
		}
		return
	}

	d := adw.NewAlertDialog("Choose a Model", "This is what Ollama has on this machine.")
	ui.FreeOnClose(&d.Dialog)
	list := gtk.NewBox(gtk.OrientationVertical, 4)

	var group *gtk.CheckButton
	picked := a.cfg.Model
	for _, m := range a.models {
		radio := gtk.NewCheckButton()
		radio.SetChild(wrappingLabel(m.Label()))
		if group == nil {
			group = radio
		} else {
			radio.SetGroup(group)
		}
		radio.SetActive(m.Name == a.cfg.Model)
		name := m.Name
		radio.ConnectToggled(func() {
			if radio.Active() {
				picked = name
			}
		})
		list.Append(radio)
	}
	d.SetExtraChild(scrolled(list))
	d.AddResponse("cancel", "Cancel")
	d.AddResponse("ok", "Use This Model")
	d.SetResponseAppearance("ok", adw.ResponseSuggested)
	d.SetDefaultResponse("ok")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response != "ok" || picked == "" {
			return
		}
		a.cfg.Model = picked
		_ = store.SaveConfig(a.cfg)
		// The open chat switches too, so the choice applies to the scene you
		// are actually in rather than only to the next one you start.
		if ch := a.chat.Chat(); ch.ID != 0 {
			_ = a.store.SetChatModel(ch.ID, picked)
		}
		a.chat.SetModel(picked)
		a.chat.SetConfig(a.cfg)
		a.refreshProfile()
		a.refreshWelcome()
		// A model that can see, or one that cannot, changes whether the chat
		// takes pictures, and it used to go on answering for the old model
		// until the chat was reopened.
		a.refreshAttachAvailability()
	})
	d.Present(a.win)
}

// modelNames is the installed models, in picker order.
func (a *App) modelNames() []string {
	out := make([]string, 0, len(a.models))
	for _, m := range a.models {
		out = append(out, m.Name)
	}
	return out
}

// modelLabels renders the dropdown's rows, falling back to a single
// explanatory row when nothing is installed, an empty dropdown looks broken.
func (f *settingsForm) modelLabels(a *App) []string {
	if len(a.models) == 0 {
		return []string{"No models found"}
	}
	out := make([]string, 0, len(a.models))
	for _, m := range a.models {
		out = append(out, m.Label())
	}
	return out
}

// housekeepingRow is which row the configured background model sits on, where
// row 0 is "same as the scene". An unset or uninstalled model lands there too:
// falling back to the scene's own model is always correct, where guessing at
// another one is not.
func housekeepingRow(models []string, want string) int {
	if strings.TrimSpace(want) == "" {
		return 0
	}
	for i, m := range models {
		if m == want {
			return i + 1
		}
	}
	return 0
}

// housekeepingLabels is the model list with a "same as the scene" row in
// front, which is both the default and what an empty setting means.
func (f *settingsForm) housekeepingLabels(a *App) []string {
	out := []string{"Same as the Scene's Model"}
	for _, m := range a.models {
		out = append(out, m.Label())
	}
	return out
}

// visionLabels is the model list with an automatic row in front. The choice
// of every model rather than only those that can see is deliberate: the list
// is built without asking the server about each one, and a choice that cannot
// see is passed over for the automatic one rather than failing.
func (f *settingsForm) visionLabels(a *App) []string {
	out := []string{"Automatic"}
	for _, m := range a.models {
		out = append(out, m.Label())
	}
	return out
}

// shrinkable lets a dropdown be narrower than its longest item.
//
// A dropdown built from strings is as wide as the widest of them, and its label
// does not shorten to fit. A model's label carries its size and quantisation, so
// the model pickers were 600px or more, which set the narrowest the whole dialog
// could be and cut it off on a narrow window. With the label ellipsised the
// dropdown takes the room there is, and the popup still lists every name in full.
func shrinkable(dd *gtk.DropDown) {
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		label := gtk.NewLabel("")
		label.SetXAlign(0)
		label.SetEllipsize(pango.EllipsizeEnd)
		obj.Cast().(*gtk.ListItem).SetChild(label)
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		item := obj.Cast().(*gtk.ListItem)
		if label, ok := item.Child().(*gtk.Label); ok {
			label.SetLabel(item.Item().Cast().(*gtk.StringObject).String())
		}
	})
	dd.SetFactory(&factory.ListItemFactory)
}

// buttonRow is a row of buttons that wraps onto further lines when it must.
//
// In a plain row their combined width was the narrowest the page could be, and so
// the narrowest the dialog could be. Their cells are not focusable, or each button
// would take two tab stops: the cell that does nothing, then the button.
func buttonRow(buttons ...*gtk.Button) *gtk.FlowBox {
	row := gtk.NewFlowBox()
	row.SetSelectionMode(gtk.SelectionNone)
	row.SetActivateOnSingleClick(false)
	row.SetMinChildrenPerLine(1)
	row.SetMaxChildrenPerLine(uint(len(buttons)))
	row.SetColumnSpacing(8)
	row.SetRowSpacing(8)
	for i, b := range buttons {
		row.Append(b)
		if cell := row.ChildAtIndex(i); cell != nil {
			cell.SetFocusable(false)
		}
	}
	return row
}

func settingsPage() *gtk.Box {
	page := gtk.NewBox(gtk.OrientationVertical, 16)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)
	return page
}

// newSlider is a labelled horizontal scale showing its own value.
func newSlider(min, max, step, value float64) *gtk.Scale {
	s := gtk.NewScaleWithRange(gtk.OrientationHorizontal, min, max, step)
	s.SetValue(value)
	s.SetDrawValue(true)
	s.SetValuePos(gtk.PosRight)
	s.SetHExpand(true)
	return s
}

// wrappingLabel is the child a check button needs when its text is a sentence:
// the stock label will not wrap, and a long one sets a page-wide minimum width.
func wrappingLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetWrap(true)
	l.SetXAlign(0)
	return l
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return 0
}

func atoiOr(s string, fallback int) int {
	n := 0
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int(r-'0')
		if n > 1<<22 { // far past any real context size; stop before overflow
			return fallback
		}
	}
	return n
}

func fontIndex(m string) int {
	switch m {
	case store.FontRenderingCrisp:
		return 1
	case store.FontRenderingSmooth:
		return 2
	default:
		return 0
	}
}

func fontFromIndex(i int) string {
	switch i {
	case 1:
		return store.FontRenderingCrisp
	case 2:
		return store.FontRenderingSmooth
	default:
		return store.FontRenderingAuto
	}
}
