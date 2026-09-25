package app

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
)

// settingsForm holds the widgets Save reads back.
type settingsForm struct {
	baseURL *gtk.Entry
	model   *gtk.DropDown
	models  []string

	personaName  *gtk.Entry
	personaDesc  *gtk.TextView
	globalInstrs *gtk.TextView
	keepAlive    *gtk.Entry

	theme    *gtk.DropDown
	fontMode *gtk.DropDown
	showStat *gtk.CheckButton
	think    *gtk.CheckButton

	temperature *gtk.Scale
	topP        *gtk.Scale
	repeat      *gtk.Scale
	numCtx      *gtk.Entry
}

// showSettings opens the settings dialog.
//
// It applies on an explicit Save rather than instantly. Several of these
// settings change what the model does mid-scene, and having a stray scroll
// over a slider quietly alter the temperature of a conversation you are in the
// middle of is not a trade worth making for one fewer click.
func (a *App) showSettings() { a.showSettingsPage("") }

// showSettingsPage opens settings on a particular page, so a menu entry can
// land somewhere specific instead of wherever the dialog opens by default.
func (a *App) showSettingsPage(page string) {
	d := adw.NewDialog()
	d.SetTitle("Settings")
	d.SetContentWidth(660)
	d.SetContentHeight(640)

	f := &settingsForm{}
	stack := gtk.NewStack()
	stack.AddTitled(scrolled(a.buildModelPage(f)), "model", "Model")
	stack.AddTitled(scrolled(a.buildPersonaPage(f)), "persona", "You")
	stack.AddTitled(scrolled(a.buildAppearancePage(f)), "appearance", "Appearance")

	side := gtk.NewStackSidebar()
	side.SetStack(stack)
	side.SetSizeRequest(150, -1)

	body := gtk.NewBox(gtk.OrientationHorizontal, 0)
	body.Append(side)
	body.Append(gtk.NewSeparator(gtk.OrientationVertical))
	stack.SetHExpand(true)
	body.Append(stack)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)
	save := gtk.NewButtonWithLabel("Save")
	save.AddCSSClass("suggested-action")
	save.ConnectClicked(func() {
		a.applySettings(f)
		d.Close()
	})
	header.PackEnd(save)

	if page != "" {
		stack.SetVisibleChildName(page)
	}

	// A sidebar of pages beside a single Save leaves a fair question open:
	// does Save mean this page or all of them? It means all of them, so it
	// says so where the button is rather than leaving it to be discovered.
	scope := gtk.NewLabel("Changes on every page are saved together.")
	scope.AddCSSClass("settings-hint")
	scope.SetMarginTop(6)
	scope.SetMarginBottom(6)
	scope.SetMarginStart(14)
	scope.SetMarginEnd(14)
	scope.SetXAlign(0)
	save.SetTooltipText("Save the changes on all three pages")

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.AddBottomBar(scope)
	tv.SetContent(body)
	d.SetChild(tv)
	d.Present(a.win)
}

func (a *App) buildModelPage(f *settingsForm) *gtk.Box {
	page := settingsPage()

	outer, card := groupCard("Model")
	// A dropdown of what is actually installed, rather than a text field.
	// "Run any local model" should be a menu; making you type an exact tag
	// from memory turns a feature into a spelling test.
	f.models = a.modelNames()
	f.model = gtk.NewDropDownFromStrings(f.modelLabels(a))
	f.model.SetSelected(uint(indexOf(f.models, a.cfg.Model)))
	hint := "Every model Ollama has on this machine."
	if len(f.models) == 0 {
		hint = "Nothing installed yet. Run `ollama pull qwen3:8b`, then reopen this."
	}
	card.Append(labelledField("Default model", hint, f.model))

	refresh := gtk.NewButtonWithLabel("Check again")
	refresh.SetHAlign(gtk.AlignStart)
	refresh.ConnectClicked(func() {
		a.probeModels()
		a.toast("Checking Ollama…")
	})
	card.Append(refresh)

	f.baseURL = gtk.NewEntry()
	f.baseURL.SetText(a.cfg.BaseURL)
	card.Append(labelledField("Ollama address",
		"Change this only if Ollama runs somewhere other than this machine's default port.",
		f.baseURL))
	page.Append(outer)

	// Sampling.
	sOuter, sCard := groupCard("How it writes")
	f.temperature = newSlider(0, 2, 0.05, a.cfg.Temperature)
	sCard.Append(labelledField("Temperature",
		"Higher wanders further and surprises more; lower stays safe and can get repetitive. Around 0.85 suits roleplay.",
		f.temperature))

	f.topP = newSlider(0.1, 1, 0.01, a.cfg.TopP)
	sCard.Append(labelledField("Top-p",
		"Trims the least likely words before choosing. 0.9–0.95 is the usual range.",
		f.topP))

	f.repeat = newSlider(1, 1.5, 0.01, a.cfg.RepeatPenalty)
	sCard.Append(labelledField("Repetition penalty",
		"Pushes back when a model starts reusing the same phrases, a common failure in long scenes.",
		f.repeat))

	f.numCtx = gtk.NewEntry()
	f.numCtx.SetText(fmt.Sprintf("%d", a.cfg.NumCtx))
	sCard.Append(labelledField("Context size (tokens)",
		"How much of the scene the model can see at once. Larger remembers more and uses more memory.",
		f.numCtx))

	f.keepAlive = gtk.NewEntry()
	f.keepAlive.SetText(a.cfg.KeepAlive)
	sCard.Append(labelledField("Keep the model loaded for",
		"How long Ollama holds the model in memory after a reply, \"30m\", \"2h\", or \"-1\" to never unload. Ollama's own default of five minutes means a thinking pause costs you a full model reload on the next message.",
		f.keepAlive))

	f.think = gtk.NewCheckButton()
	f.think.SetChild(wrappingLabel("Let reasoning models think first (shown collapsed above each reply)"))
	f.think.SetActive(a.cfg.Think)
	sCard.Append(f.think)
	page.Append(sOuter)

	return page
}

func (a *App) buildPersonaPage(f *settingsForm) *gtk.Box {
	page := settingsPage()
	outer, card := groupCard("Who you play as")

	f.personaName = gtk.NewEntry()
	f.personaName.SetText(a.cfg.PersonaName)
	f.personaName.SetPlaceholderText("Leave empty to stay unnamed")
	card.Append(labelledField("Your name",
		"Characters address you by this, and it replaces {{user}} in their cards.",
		f.personaName))

	frame, view := multilineField(a.cfg.PersonaDescription, 5)
	f.personaDesc = view
	card.Append(labelledField("About you",
		"Optional. Who you are in the scene, appearance, role, anything the character should already know. Left empty, the model will invent it as it goes.\n\n"+
			"{{user}} becomes your name and {{char}} becomes whichever character you are playing with.",
		frame))
	page.Append(outer)

	styleOuter, styleCard := groupCard("Writing style")
	cur := gtk.NewLabel(a.cfg.Style().Name)
	cur.SetXAlign(0)
	cur.AddCSSClass("field-label")
	styleCard.Append(labelledField("In use",
		"Controls how the prose sounds, without touching the formatting the transcript is rendered from.",
		cur))
	manage := gtk.NewButtonWithLabel("Manage writing styles…")
	manage.SetHAlign(gtk.AlignStart)
	manage.ConnectClicked(func() { a.showStyles() })
	styleCard.Append(manage)
	page.Append(styleOuter)

	insOuter, insCard := groupCard("Instructions for every character")
	giFrame, giView := multilineField(a.cfg.GlobalInstructions, 5)
	f.globalInstrs = giView
	insCard.Append(labelledField("Always apply these",
		"Rules that hold for every scene, whoever you are playing with, \"keep replies under three paragraphs\", \"never fade to black\", \"British spelling\".\n\n"+
			"Write {{char}} and {{user}} rather than names: these apply to every character.\n\n"+
			"A character's own instructions are applied after these, so a specific one wins where the two disagree. One instruction per line works best.",
		giFrame))
	page.Append(insOuter)
	return page
}

func (a *App) buildAppearancePage(f *settingsForm) *gtk.Box {
	page := settingsPage()
	outer, card := groupCard("Appearance")

	f.theme = gtk.NewDropDownFromStrings([]string{"Dark", "Light", "Follow the system"})
	f.theme.SetSelected(uint(themeIndex(a.cfg.Theme)))
	card.Append(labelledField("Theme", "", f.theme))

	f.fontMode = gtk.NewDropDownFromStrings([]string{"Automatic", "Crisp (1080p screens)", "Smooth (HiDPI screens)"})
	f.fontMode.SetSelected(uint(fontIndex(a.cfg.FontRendering)))
	card.Append(labelledField("Text rendering",
		"Automatic picks per screen. Change it if text looks soft or unevenly spaced.",
		f.fontMode))

	f.showStat = gtk.NewCheckButton()
	f.showStat.SetChild(wrappingLabel("Show speed and token count under each reply"))
	f.showStat.SetActive(a.cfg.ShowStats)
	card.Append(f.showStat)
	page.Append(outer)
	return page
}

// applySettings reads the form into the config, saves it, and pushes the
// changes into the live objects that already exist.
func (a *App) applySettings(f *settingsForm) {
	if i := int(f.model.Selected()); i >= 0 && i < len(f.models) {
		a.cfg.Model = f.models[i]
	}
	if u := strings.TrimSpace(f.baseURL.Text()); u != "" {
		a.cfg.BaseURL = u
	}
	a.cfg.PersonaName = strings.TrimSpace(f.personaName.Text())
	a.cfg.PersonaDescription = textOf(f.personaDesc)
	a.cfg.GlobalInstructions = textOf(f.globalInstrs)
	if k := strings.TrimSpace(f.keepAlive.Text()); k != "" {
		a.cfg.KeepAlive = k
	}
	a.cfg.Theme = themeFromIndex(int(f.theme.Selected()))
	a.cfg.FontRendering = fontFromIndex(int(f.fontMode.Selected()))
	a.cfg.ShowStats = f.showStat.Active()
	a.cfg.Think = f.think.Active()
	a.cfg.Temperature = f.temperature.Value()
	a.cfg.TopP = f.topP.Value()
	a.cfg.RepeatPenalty = f.repeat.Value()
	if n := atoiOr(f.numCtx.Text(), a.cfg.NumCtx); n > 0 {
		a.cfg.NumCtx = n
	}

	if err := store.SaveConfig(a.cfg); err != nil {
		a.toast("Could not save settings: " + err.Error())
	}

	// Push into the live objects. The client is rebuilt rather than mutated
	// because its base URL is baked into an http.Client at construction.
	a.client = ollamaClientFor(a.cfg.BaseURL)
	a.client.KeepAlive = a.cfg.KeepAlive
	a.theme.apply(a.cfg.Theme)
	a.applyFontRendering()
	if a.chat != nil {
		a.chat.SetClient(a.client)
		a.chat.SetConfig(a.cfg)
	}
	if a.sidebar != nil {
		a.sidebar.SetProfile(a.cfg.PersonaName, a.cfg.PersonaDescription)
	}
	a.probeModels()
}

// showModelPicker is the quick switcher reached from the composer and Ctrl+M.
func (a *App) showModelPicker() {
	if len(a.models) == 0 {
		a.probeModels()
		if a.probeErr != nil {
			a.toast("Ollama isn't running. Start it with `ollama serve`.")
		} else {
			a.toast("No models installed. Pull one with `ollama pull qwen3:8b`.")
		}
		return
	}

	d := adw.NewAlertDialog("Choose a model", "This is what Ollama has on this machine.")
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
	d.AddResponse("ok", "Use this model")
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
		a.sidebar.SetProfile(a.cfg.PersonaName, a.cfg.PersonaDescription)
		a.refreshWelcome()
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
// explanatory row when nothing is installed — an empty dropdown looks broken.
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

func themeIndex(t string) int {
	switch t {
	case store.ThemeLight:
		return 1
	case store.ThemeSystem:
		return 2
	default:
		return 0
	}
}

func themeFromIndex(i int) string {
	switch i {
	case 1:
		return store.ThemeLight
	case 2:
		return store.ThemeSystem
	default:
		return store.ThemeDark
	}
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
