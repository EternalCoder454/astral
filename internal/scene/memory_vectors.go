package scene

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"

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
	// meaningMoments is how many moments the vectors put forward, as many
	// as the word search does.
	meaningMoments = 6
	// meaningFloor is how near a moment has to be to be recalled at all:
	// without one, the nearest six are put forward however far they are.
	// Unmeasured, since no embedding model was installed to measure with;
	// sentence embedding models commonly score related passages above 0.6
	// and unrelated ones below 0.5.
	meaningFloor = 0.5
	// meaningSpread is how far below the nearest moment another may be and
	// still be put forward. A model that scores everything in a scene above
	// the floor, as some do for text in one voice, would otherwise send the
	// six nearest of a crowd of equals.
	meaningSpread = 0.1
)

// queryVec is a chat's latest exchange, embedded after the reply that ended
// it, with the model that made it and the newest message it holds.
type queryVec struct {
	model string
	vec   []float32
	upto  int64
}

// queries holds each chat's latest queryVec. Recall reads it rather than ask
// the embedding model while a reply is being prepared: on the desktop that is
// the UI thread, and the model, unloaded between turns, can take seconds to
// come back. It is one message behind, the reply before your newest message
// rather than both, which is close enough to what the scene is about.
var queries sync.Map // chat id -> queryVec

// indexing holds the chats being embedded, so the window and a phone playing
// the same scene do not both embed the same batch, and again the chats asked
// for while that was happening, so the pass going on goes round once more for
// the exchange it did not see.
var indexing, again sync.Map // chat id -> struct{}

// IndexMessages makes vectors for up to a batch of a chat's messages that
// lack one, oldest first, and says how many it made, and embeds the chat's
// latest exchange for the next recall to search with, in the same request. It
// does nothing when no embedding model is installed, or for a chat that is
// never recalled from.
//
// Call it off the UI thread, after a reply. The embedding runs on the CPU, so
// it takes nothing from the scene's model in video memory.
func IndexMessages(ctx context.Context, st *store.Store, client *ollama.Client, cfg store.Config, chatID int64) (int, error) {
	if st == nil || client == nil || chatID == 0 {
		return 0, nil
	}
	if ch, err := st.Chat(chatID); err != nil || !Recalls(ch.Kind) {
		return 0, err
	}
	model := EmbedModel(ctx, client, cfg)
	if model == "" {
		// Nothing to search with, and a query kept from a model since removed
		// is not left for recall to use.
		queries.Delete(chatID)
		return 0, nil
	}
	again.Store(chatID, struct{}{})
	if _, busy := indexing.LoadOrStore(chatID, struct{}{}); busy {
		return 0, nil // the pass going on goes round again for this
	}
	total := 0
	for {
		again.Delete(chatID)
		n, err := indexPass(ctx, st, client, model, chatID)
		total += n
		indexing.Delete(chatID)
		if err != nil {
			queries.Delete(chatID)
			return total, err
		}
		if _, asked := again.Load(chatID); !asked {
			return total, nil
		}
		if _, busy := indexing.LoadOrStore(chatID, struct{}{}); busy {
			return total, nil // another has started, and will see it
		}
	}
}

// indexPass is one request of IndexMessages.
func indexPass(ctx context.Context, st *store.Store, client *ollama.Client, model string, chatID int64) (int, error) {
	pending, err := st.MessagesWithoutVector(chatID, model, indexBatch)
	if err != nil {
		return 0, err
	}
	// The same query recall's word search makes: your newest message and
	// the reply before it, newest first.
	recent, err := st.LastMessages(chatID, 2)
	if err != nil {
		return 0, err
	}
	var query []string
	for i := len(recent) - 1; i >= 0; i-- {
		if c := strings.TrimSpace(recent[i].Content); c != "" {
			query = append(query, c)
		}
	}
	inputs := make([]string, 0, len(pending)+1)
	for _, p := range pending {
		inputs = append(inputs, excerpt(p.Content, embedChars))
	}
	if len(query) > 0 {
		inputs = append(inputs, excerpt(strings.Join(query, "\n"), queryChars))
	} else {
		queries.Delete(chatID)
	}
	if len(inputs) == 0 {
		return 0, nil
	}
	vecs, err := client.EmbedOnCPU(ctx, model, inputs)
	if err != nil {
		return 0, err
	}
	if len(query) > 0 {
		queries.Store(chatID, queryVec{model: model, vec: vecs[len(vecs)-1], upto: recent[len(recent)-1].ID})
		vecs = vecs[:len(vecs)-1]
	}
	if len(pending) == 0 {
		return 0, nil
	}
	if err := st.SaveMessageVectors(model, pending, vecs); err != nil {
		return 0, err
	}
	return len(pending), nil
}

// Recalls reports whether a kind of chat has a memory to recall from: the
// scenes and the assistant, which are compacted, and not the designers,
// whose conversations are short and about the thing being made.
func Recalls(kind string) bool {
	switch kind {
	case store.KindDesigner, store.KindStyleDesigner, store.KindWorldDesigner, store.KindPromptOptimizer,
		store.KindPersonaDesigner:
		return false
	}
	return true
}

// byMeaning finds the moments before the scene's bookmark nearest to its
// latest exchange, best first, from the query IndexMessages last embedded. It
// never asks the embedding model anything, so it costs a reply nothing.
func byMeaning(st *store.Store, ch store.Chat) []store.Moment {
	q, ok := queries.Load(ch.ID)
	if !ok {
		return nil
	}
	qv := q.(queryVec)
	// The exchange it was made from is gone, taken back or written again, and
	// what it was about is not what the scene is about now.
	if ok, err := st.MessageExists(ch.ID, qv.upto); err != nil || !ok {
		return nil
	}
	stored, err := st.MessageVectors(ch.ID, ch.SummaryUpto, qv.model)
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
		// A vector from a model of another size scores -1 and falls below
		// the floor with everything else that is not near.
		if sim := store.Cosine(qv.vec, v.Vec); sim >= meaningFloor {
			all = append(all, scored{v.ID, sim})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].sim != all[j].sim {
			return all[i].sim > all[j].sim
		}
		return all[i].id > all[j].id
	})
	for n, sc := range all {
		if n == meaningMoments || sc.sim < all[0].sim-meaningSpread {
			all = all[:n]
			break
		}
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
