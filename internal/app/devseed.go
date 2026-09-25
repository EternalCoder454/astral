package app

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/ui"
	"astral/internal/world"
)

// ASTRAL_DEV_SEED fills an empty database with a world, a cast and a played
// scene, so the interface can be looked at as it appears in use rather than as
// a column of empty states. It refuses to touch a database that already has
// anything in it.
//
// It exists for screenshots and for looking at layout under real content:
// almost every spacing and truncation problem only appears once there is
// something to truncate.
var devSeed = os.Getenv("ASTRAL_DEV_SEED") != ""

func (a *App) runDevSeed() {
	if !devSeed || a.store == nil {
		return
	}
	if n, err := a.store.CountCharacters(); err != nil || n > 0 {
		return // never overwrite a real database
	}

	wid, err := a.store.SaveWorld(world.World{
		Name:        "The Drowned Coast",
		Description: "A shoreline that will not hold still. Maps here go out of date faster than they can be drawn.",
	})
	if err != nil {
		log.Printf("astral: seed: %v", err)
		return
	}

	lore := []world.Entry{
		{Name: "Kestrel Bay", Keys: []string{"Kestrel Bay", "the Bay"}, Enabled: true, Priority: 5,
			Content: "A port city three days north. Its ferries have never once run on time, and the delays have grown worse each month this year."},
		{Name: "Cartographers' Guild", Keys: []string{"the Guild", "Cartographers"}, Enabled: true, Auto: true, Confidence: 0.9, Priority: 3,
			Content: "Forbids charting anything east of the Sever, and has done for sixty years. Expels members who break the rule."},
		{Name: "The Sever", Keys: []string{"the Sever", "Sever River"}, Enabled: true, Auto: true, Confidence: 0.85,
			Content: "A river that marks the eastern boundary of what may legally be mapped."},
		{Name: "The tide table", Keys: []string{"tide table", "the tables"}, Enabled: false, Auto: true, Confidence: 0.45,
			Content: "Possibly kept under the map room floor. Vesper implied this but did not say it outright."},
	}
	for _, e := range lore {
		e.WorldID = wid
		if _, err := a.store.SaveLoreEntry(e); err != nil {
			log.Printf("astral: seed lore: %v", err)
		}
	}

	vesper := chars.Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet. Tall, ink to the elbows, and never without the brass dividers she was expelled with. She speaks as if every sentence costs her something.",
		Personality: "wry, guarded, precise, slow to trust",
		Scenario:    "The map room, past midnight. {{user}} has come in out of the rain, three hours later than agreed.",
		FirstMes:    "*She does not look up.* \"You're late, {{user}}.\" *A pin goes into the table rather than the map, a small and deliberate violence.*",
		Tags:        []string{"mystery", "slow-burn"},
		WorldID:     wid,
		Accent:      ui.AccentFor("Vesper Quill"),
	}
	if p := a.seedPortrait("vesper"); p != "" {
		vesper.PortraitPath, vesper.AvatarPath = p, p
	}
	vid, err := a.store.SaveCharacter(vesper)
	if err != nil {
		log.Printf("astral: seed character: %v", err)
		return
	}
	vesper.ID = vid

	a.store.SaveCharacter(chars.Character{
		Name:        "Odile Marchetti",
		Description: "A locksmith who has never once picked a lock she was paid to.",
		Personality: "dry, patient, quietly furious",
		Scenario:    "Her shop, twenty minutes after closing.",
		FirstMes:    "*She does not turn the sign around.* \"We're shut.\"",
		Tags:        []string{"noir"},
		WorldID:     wid,
		Accent:      ui.AccentFor("Odile Marchetti"),
	})
	a.store.SaveCharacter(chars.Character{
		Name:        "Marius",
		Description: "A lighthouse keeper writing letters to someone who stopped replying three years ago.",
		Personality: "gentle, persistent, self-aware",
		Scenario:    "The lighthouse, late afternoon.",
		FirstMes:    "*He sets the pen down, then picks it up again.* \"Well. I suppose I should finish this one.\"",
		Accent:      ui.AccentFor("Marius"),
	})

	chat, err := a.store.NewChat(vid, "The tide came in early", a.cfg.Model, store.KindRoleplay)
	if err != nil {
		log.Printf("astral: seed chat: %v", err)
		return
	}
	scene := []struct {
		role, text string
		tps        float64
		tokens     int
	}{
		{ollama.RoleAssistant, "*She does not look up.* \"You're late, " + a.cfg.PersonaName + ".\" *A pin goes into the table rather than the map, a small and deliberate violence.*", 36.1, 48},
		{ollama.RoleUser, "*I set the lantern down on the edge of the table, where the rain could not reach it.* \"The ferry from Kestrel Bay was held.\"", 0, 0},
		{ollama.RoleAssistant, "*Vesper stopped, and for a moment the only sound was water finding its way off my coat and onto her floor.* \"Kestrel Bay.\" *She said it the way other people say a date they would rather not remember.* \"That's the third delay this month.\"\n\n*She turned the map face down before I could see the coastline.* \"Sit. You're dripping on the Sever.\"", 34.8, 112},
		{ollama.RoleUser, "*I sat.* \"You've never been, have you. To any of it.\"", 0, 0},
		{ollama.RoleAssistant, "*The dividers stopped.*\n\n\"No.\" *A long pause, and then, as if it were nothing at all:* \"The Guild forbids charting east of the Sever. Sixty years, that rule. I was expelled for a coastline I have never stood on.\" *She rubbed at her wrist, where the mark had been struck through, and seemed annoyed to have been caught doing it.* \"Ask me again when the tide is out.\"", 35.4, 140},
	}
	for _, m := range scene {
		if _, err := a.store.AddMessage(store.Message{
			ChatID: chat.ID, Role: m.role, Content: m.text,
			TokPerSec: m.tps, EvalCount: m.tokens, CreatedAt: time.Now(),
		}); err != nil {
			log.Printf("astral: seed message: %v", err)
		}
	}
	a.cfg.LastChat = chat.ID
	log.Printf("astral: seed: world %d, 3 characters, chat %d", wid, chat.ID)
}

// seedPortrait draws a placeholder portrait so the panel can be seen holding
// something. It is a gradient, not a person: this is for layout.
func (a *App) seedPortrait(name string) string {
	dir := store.AvatarDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, name+"-seed.png")
	// A vertical gradient in the app's own clay, drawn as a tiny PNG by hand
	// so seeding needs no image tooling installed.
	const w, h = 64, 96
	img := make([]byte, 0, w*h*3)
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		r := byte(60 + 160*(1-t))
		g := byte(40 + 90*(1-t))
		b := byte(36 + 60*(1-t))
		for x := 0; x < w; x++ {
			img = append(img, r, g, b)
		}
	}
	if err := writePNG(path, w, h, img); err != nil {
		log.Printf("astral: seed portrait: %v", err)
		return ""
	}
	return path
}

// writePNG encodes raw RGB as a PNG using the standard library, so seeding
// needs no image tooling installed.
func writePNG(path string, w, h int, rgbPixels []byte) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 3
			img.Set(x, y, color.RGBA{rgbPixels[i], rgbPixels[i+1], rgbPixels[i+2], 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
