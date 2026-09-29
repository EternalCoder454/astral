package scene

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"astral/internal/ollama"
	"astral/internal/store"
)

// Recalling a moment by what it is about, not only by its words.
//
// Each message worth recalling gets a vector from the knowledge base's
// embedding model, made in the background after a reply. Recall embeds the
// latest exchange, finds the messages whose vectors are nearest, and merges
// them with what the word search found. With no embedding model installed
// nothing here does anything, and recall is the word search it always was.

const (
	// indexBatch is how many messages one pass embeds. A scene adds two a
	// turn, so a pass keeps up with it and an old scene catches up over a
	// few replies.
	indexBatch = 32
	// embedChars bounds the text of one message sent to the embedding model.
	// The beginning of a reply says what it is about, and an embedding of
	// pages of it would cost more on the CPU than it tells.
	embedChars = 2000
	// queryChars bounds the query, newest words first, for the same reason.
	queryChars = 1500
	// queryTimeout is how long recall waits for the embedding model. It runs
	// while a reply is being prepared, so it can never hold one up: past this
	// the scene goes on with the word search alone.
	queryTimeout = 300 * time.Millisecond
	// meaningMoments is how many moments the vectors put forward, as many
	// as the word search does.
	meaningMoments = 6
)

// embedSource is the client and settings recall embeds with.
type embedSource struct {
	client *ollama.Client
	cfg    store.Config
}

// embedding is what recall was last told to use. Prompt assembly is deep
// inside recall's callers and has no client or settings to pass along, and the
// window and the phone are one process with one of each, so each says what it
// is about to build a prompt with.
var embedding atomic.Pointer[embedSource]

// UseEmbedding tells recall which client and settings to embed with. Call it
// before building a turn.
func UseEmbedding(client *ollama.Client, cfg store.Config) {
	embedding.Store(&embedSource{client: client, cfg: cfg})
}

// indexing holds the chats being embedded, so the window and a phone playing
// the same scene do not both embed the same batch.
var indexing sync.Map // chat id -> struct{}

// IndexMessages makes vectors for up to a batch of a chat's messages that
// lack one, oldest first, and says how many it made. It does nothing when no
// embedding model is installed.
//
// Call it off the UI thread, after a reply. The embedding runs on the CPU, so
// it takes nothing from the scene's model in video memory.
func IndexMessages(ctx context.Context, st *store.Store, client *ollama.Client, cfg store.Config, chatID int64) (int, error) {
	if st == nil || client == nil || chatID == 0 {
		return 0, nil
	}
	model := EmbedModel(ctx, client, cfg)
	if model == "" {
		return 0, nil
	}
	if _, busy := indexing.LoadOrStore(chatID, struct{}{}); busy {
		return 0, nil
	}
	defer indexing.Delete(chatID)
	pending, err := st.MessagesWithoutVector(chatID, model, indexBatch)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	inputs := make([]string, len(pending))
	for i, p := range pending {
		inputs[i] = excerpt(p.Content, embedChars)
	}
	vecs, err := client.EmbedOnCPU(ctx, model, inputs)
	if err != nil {
		return 0, err
	}
	if err := st.SaveMessageVectors(model, pending, vecs); err != nil {
		return 0, err
	}
	return len(pending), nil
}

// byMeaning finds the moments before the scene's bookmark whose vectors are
// nearest to the text, best first. It returns nothing, rather than waiting,
// whenever the embedding model is not there or not quick.
func byMeaning(st *store.Store, ch store.Chat, text string) []store.Moment {
	src := embedding.Load()
	if src == nil || src.client == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	// Before anything is asked of the server: a scene without vectors, which is
	// every scene while no embedding model is installed, costs one small query.
	if has, err := st.HasMessageVectors(ch.ID); err != nil || !has {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	model := embedModelWithin(ctx, src.client, src.cfg)
	if model == "" {
		return nil
	}
	vecs, err := src.client.EmbedOnCPU(ctx, model, []string{excerpt(text, queryChars)})
	if err != nil || len(vecs) != 1 {
		// A model that is still loading is expected and quiet.
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("astral: embedding the query for chat %d: %v", ch.ID, err)
		}
		return nil
	}
	stored, err := st.MessageVectors(ch.ID, ch.SummaryUpto, model)
	if err != nil {
		log.Printf("astral: reading vectors for chat %d: %v", ch.ID, err)
		return nil
	}
	type scored struct {
		id  int64
		sim float64
	}
	// A scan, like the knowledge base's: a long scene is a few thousand
	// messages, a few megabytes of vectors and some milliseconds of arithmetic.
	all := make([]scored, 0, len(stored))
	for _, v := range stored {
		// Not positive means unrelated, or a vector from a model of another
		// size, which Cosine reports as -1.
		if sim := store.Cosine(vecs[0], v.Vec); sim > 0 {
			all = append(all, scored{v.ID, sim})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].sim != all[j].sim {
			return all[i].sim > all[j].sim
		}
		return all[i].id > all[j].id
	})
	if len(all) > meaningMoments {
		all = all[:meaningMoments]
	}
	ids := make([]int64, len(all))
	for i, sc := range all {
		ids[i] = sc.id
	}
	moments, err := st.MomentsByID(ch.ID, ids)
	if err != nil {
		log.Printf("astral: reading moments found by meaning in chat %d: %v", ch.ID, err)
		return nil
	}
	return moments
}

// embedModelWithin is EmbedModel, given up on when ctx runs out.
//
// The lookup is not handed ctx: knowledge.Model remembers a lookup that failed
// for ten minutes, and one cut short by a deadline this tight would switch the
// knowledge base's vectors off with it. It carries on in the background and
// its answer is there for the next turn.
func embedModelWithin(ctx context.Context, client *ollama.Client, cfg store.Config) string {
	if m := strings.TrimSpace(cfg.EmbeddingModel); m != "" {
		return m
	}
	found := make(chan string, 1)
	go func() { found <- EmbedModel(context.Background(), client, cfg) }()
	select {
	case m := <-found:
		return m
	case <-ctx.Done():
		return ""
	}
}

// fuseMoments merges ranked lists of moments by reciprocal rank, as the
// knowledge base merges words and vectors: a moment's score is the sum, over
// the lists, of one over sixty plus its place. The two searches share no
// scale, and a moment both of them liked beats one that only one did.
func fuseMoments(lists ...[]store.Moment) []store.Moment {
	const k = 60.0
	score := map[int64]float64{}
	byID := map[int64]store.Moment{}
	for _, list := range lists {
		for rank, m := range list {
			score[m.ID] += 1 / (k + float64(rank) + 1)
			byID[m.ID] = m
		}
	}
	out := make([]store.Moment, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if score[out[i].ID] != score[out[j].ID] {
			return score[out[i].ID] > score[out[j].ID]
		}
		return out[i].ID > out[j].ID
	})
	return out
}
