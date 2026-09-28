package scene

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"astral/internal/chars"
	"astral/internal/gpu"
	"astral/internal/ollama"
)

// Looking at a picture for a model that cannot.
//
// Most of the models people run for roleplay are text only, including
// fine-tunes of models that could see before the vision half was left out of
// the download. A design chat on one of those used to refuse every picture,
// which to someone holding a photograph of the character they want looks like
// the feature is broken. So when the model being talked to cannot see, one that
// can looks at the picture first and writes down what it shows, and the
// conversation carries on in words.
//
// The two are never in video memory together unless they fit together. The
// models already loaded are set aside while the other one looks, and the one
// that looked is released as soon as it has finished, so the reply loads into
// the room the picture was looked at in.

// readVRAM is gpu.Read, swapped out by the tests so they do not depend on the
// card in the machine running them.
var readVRAM = gpu.Read

// ErrNobodyCanSee means no installed model accepts images.
var ErrNobodyCanSee = errors.New("none of the installed models can see images")

// Seer returns the model that should look at a picture sent into a chat played
// with replyModel. direct is true when that is replyModel itself, and the
// picture goes to it as it is.
//
// chosen is the Image Model setting; see store.Config.VisionModel. A choice
// that is not installed, or cannot see, falls back to choosing automatically
// rather than failing: the setting is a preference, and a picture nobody looks
// at is worse than one looked at by a different model.
func Seer(ctx context.Context, client *ollama.Client, chosen, replyModel string) (seer string, direct bool, err error) {
	chosen = strings.TrimSpace(chosen)
	if chosen != "" && !sameName(chosen, replyModel) {
		if ok, err := client.CanSee(ctx, chosen); err == nil && ok {
			return chosen, false, nil
		}
	}
	if ok, err := client.CanSee(ctx, replyModel); err == nil && ok {
		return replyModel, true, nil
	}
	seers, err := client.VisionModels(ctx)
	if err != nil {
		return "", false, err
	}
	if len(seers) == 0 {
		return "", false, ErrNobodyCanSee
	}
	return pickSeer(ctx, client, seers), false, nil
}

// CanTakePictures reports whether a picture sent into a chat played with model
// would be looked at by anything.
func CanTakePictures(ctx context.Context, client *ollama.Client, chosen, model string) bool {
	_, _, err := Seer(ctx, client, chosen, model)
	return err == nil
}

// pickSeer chooses among models that can see: the largest that fits on the
// card by itself, since within a family a larger model reads a face and what
// someone is wearing more closely. When the card cannot be read, the smallest,
// which is the one least likely to be a problem.
func pickSeer(ctx context.Context, client *ollama.Client, seers []ollama.Model) string {
	sort.Slice(seers, func(i, j int) bool { return seers[i].Size < seers[j].Size })
	m, ok := readVRAM()
	if !ok {
		return seers[0].Name
	}
	// The card as it would be with no model on it: what is in use now, less
	// what the loaded models account for. Everything loaded is set aside
	// before a picture is looked at when it would not otherwise fit, so that
	// is the room there is.
	baseline := m.Used
	if loaded, err := client.Running(ctx); err == nil {
		for _, l := range loaded {
			if v := uint64(max(l.SizeVRAM, 0)); v < baseline {
				baseline -= v
			} else {
				baseline = 0
			}
		}
	}
	room := float64(m.Total) - float64(baseline) - float64(gpu.DesktopReserve)
	best := seers[0].Name
	for _, s := range seers {
		if float64(s.Size)*contextHeadroom <= room {
			best = s.Name
		}
	}
	return best
}

// seeingOptions are the sampler settings for a description. Cool, because
// this is observation and a description that invents is worse than a plain
// one; a context big enough for a large picture's image tokens and the answer.
var seeingOptions = ollama.Options{
	Temperature:   0.3,
	TopP:          0.9,
	RepeatPenalty: 1.05,
	NumCtx:        8192,
	NumPredict:    1200,
}

// Describe has seer look at a picture and write down what it shows, for a chat
// played with replyModel. said is what the person wrote with it, and design
// says the chat is building a character, which is when a person in the picture
// matters most.
func Describe(ctx context.Context, client *ollama.Client, seer, replyModel, image64, said string, design bool) (string, error) {
	resident := false
	if loaded, err := client.Running(ctx); err == nil {
		_, resident = ollama.FindLoaded(loaded, seer)
		if !resident && len(loaded) > 0 && !fitsBeside(ctx, client, seer) {
			// Set aside everything, not only the chat's model: a background
			// model sitting beside it is still in the way.
			for _, l := range loaded {
				_ = client.Unload(ctx, l.Name)
			}
		}
	}

	msgs := []ollama.Message{
		{Role: ollama.RoleSystem, Content: chars.SeeingPrompt(design)},
		{Role: ollama.RoleUser, Content: chars.SeeingRequest(said), Images: []string{image64}},
	}
	think := false
	msg, _, err := client.Chat(ctx, seer, msgs, seeingOptions, &think, nil)

	if !resident && !sameName(seer, replyModel) {
		// Released even when the person pressed Stop, which is why this has
		// a context of its own: the reply's context is already cancelled.
		uctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = client.Unload(uctx, seer)
		cancel()
	}
	if err != nil {
		return "", err
	}
	_, text := ollama.SplitThinking(msg.Content)
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("the model that looked at it said nothing")
	}
	return text, nil
}

// fitsBeside reports whether seer fits on the card as it is now.
func fitsBeside(ctx context.Context, client *ollama.Client, seer string) bool {
	m, ok := readVRAM()
	if !ok {
		return true // unknown hardware; see the gpu package
	}
	size := int64(0)
	if models, err := client.Models(ctx); err == nil {
		for _, mo := range models {
			if sameName(mo.Name, seer) {
				size = mo.Size
				break
			}
		}
	}
	if size <= 0 {
		return false // unmeasured, so make room rather than hope
	}
	return float64(m.Free()) >= float64(size)*contextHeadroom+float64(gpu.DesktopReserve)
}

func sameName(a, b string) bool {
	return strings.TrimSuffix(a, ":latest") == strings.TrimSuffix(b, ":latest")
}
