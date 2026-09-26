package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"astral/internal/ollama"
)

// The tool the model calls, and the loop that runs it.
//
// The model decides when to search, rather than the app searching on every
// message. That is the whole difference between a feature and a tax: most
// questions do not need the web, a search costs a round trip and a chunk of the
// context window, and a model asked "what is 12 times 8" should not be consulting
// anybody.
//
// A model without the tool-calling capability ignores the tool and answers from
// what it knows, which is what it did before this existed.

// ToolName is what the model calls it. Short, and a verb, because the name is
// most of what a model reads when deciding whether a tool applies.
const ToolName = "web_search"

// MaxRounds is how many times one turn may search.
//
// Three, and the limit is there because a model that has been handed results it
// cannot use will ask again with a slightly different query, indefinitely, while
// the person waits. Three is enough for a question that genuinely needs two
// lookups and a correction.
const MaxRounds = 3

// Tool is the description the model is given.
//
// A tool description is read while the model is deciding whether the tool
// applies, so it says when to call it and when not to, in that order, and gives
// the shape of a query. A description that only says what the tool does gets
// called on "what is 12 times 8".
var Tool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name: ToolName,
		Description: "Search the web and read the results. Call this when the answer depends on " +
			"information that changes, or that you may be out of date about: current versions, " +
			"release dates, prices, recent events, who holds a position, whether something still " +
			"exists, documentation for a specific library or product. Pass keywords as you would " +
			"type them into a search engine, not a question. Do not call this for arithmetic, for " +
			"writing, for reasoning about something already in front of you, or for anything you " +
			"know reliably and that does not change.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Keywords to search for, as typed into a search engine. Include the term that distinguishes what you want, and a year when recency matters. Not a sentence and not a question."
    }
  },
  "required": ["query"]
}`),
	},
}

// Guidance is the block added to the system prompt when search is available.
//
// The tool description above is read in the moment of deciding whether to call
// the tool, and has room for about a paragraph. This has room to teach the parts
// that actually separate a useful search from a wasted one: how to phrase a
// query, when to try again rather than guess, how much an extract supports, and
// what to say when the search did not settle it.
//
// The last of those is the one worth the tokens. A model that has searched and
// found nothing will otherwise answer as though it had found something, and an
// answer that looks sourced and is not is worse than no search at all.
const Guidance = `WEB SEARCH

You can search the web with the ` + ToolName + ` tool.

WHEN TO SEARCH
Search when the answer depends on something that changes, or that you may be out of date about: current versions, release dates, prices, who holds a position, whether a library or a service still exists, recent events, anything where being a year behind would make you wrong.
Search when the question names something specific you are not certain about: a package, a product, a person, an error message, a standard.
Do not search for arithmetic, for writing, for reasoning about something already in front of you, or for anything you know reliably and that does not change.
If you are unsure whether what you know is still current, that is a reason to search rather than a reason to guess.

HOW TO SEARCH
Write queries the way you would type them into a search engine: keywords, no filler. "go 1.26 release date", not "when was Go 1.26 released?"
Include the term that distinguishes what you want. Where a subject has a common name and a specific one, use the specific one.
One search per distinct fact. A question with two parts gets two searches rather than one query trying to cover both.
If the results do not answer it, search again with different words: narrower when you got noise, broader when you got nothing, and the exact phrase or error text when you have one. Do not fall back on a guess while another query would settle it.
Stop once you have the answer. One good search is usually enough.

READING WHAT COMES BACK
Results are titles, addresses and short extracts, not whole pages. Do not claim more than an extract supports.
Prefer the primary source: a project's own documentation or release notes over an article about them, and an official page over an aggregator.
Watch the dates. An extract that does not say when it was written may be years old.
Where results disagree, say so, say which one you are going with, and say why. Do not quietly pick one.

ANSWERING
Name the source for anything you took from a search, so it can be checked.
Say plainly when a search found nothing, failed, or did not settle the question, and then answer from what you know while saying that is what you are doing.
Never present a guess as something you looked up.
Do not narrate your searching. Answer the question.`

// Round is one search a turn made, for showing the person what was looked up.
type Round struct {
	Query   string
	Results []Result
	Err     error
}

// Notes renders the searches a turn made, for the fold above the reply.
//
// Worth showing plainly rather than hiding: a reply that quietly went to the
// internet is a reply you cannot judge. What it looked for and what came back is
// the difference between trusting the answer and having to check it.
func Notes(rounds []Round) string {
	if len(rounds) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range rounds {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Searched for ")
		b.WriteString(quoted(r.Query))
		if r.Err != nil {
			b.WriteString("\n  failed: ")
			b.WriteString(r.Err.Error())
			b.WriteString("\n")
			continue
		}
		if len(r.Results) == 0 {
			b.WriteString("\n  nothing found\n")
			continue
		}
		b.WriteString("\n")
		for _, res := range r.Results {
			b.WriteString("  ")
			b.WriteString(oneLine(res.Title))
			b.WriteString("\n    ")
			b.WriteString(res.URL)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Runner asks a model to answer, letting it search as it goes.
type Runner struct {
	Client   *ollama.Client
	Provider Provider
	Model    string
	Options  ollama.Options
	Think    *bool
	// Results is how many hits one search asks for. Zero means DefaultResults.
	Results int
	// OnRound is called after each search, on the goroutine the run is on, so a
	// caller can say what is happening while the model waits for the answer.
	OnRound func(Round)
}

// Run answers the conversation, searching when the model asks to.
//
// msgs is the request as it would have been sent without any of this, and comes
// back unchanged: the tool exchange is appended to a copy, because what gets
// stored in the transcript is the question and the answer, not the model's
// working.
//
// onDelta streams only the final answer. A round that ends in a tool call has
// produced no answer to stream, and showing the person a paragraph that is about
// to be replaced by a real one is worse than showing them nothing.
func (r *Runner) Run(ctx context.Context, msgs []ollama.Message, onDelta func(ollama.Delta)) (ollama.Message, ollama.Stats, []Round, error) {
	if r.Provider == nil {
		msg, stats, err := r.Client.Chat(ctx, r.Model, msgs, r.Options, r.Think, onDelta)
		return msg, stats, nil, err
	}

	conv := make([]ollama.Message, len(msgs))
	copy(conv, msgs)

	var rounds []Round
	for i := 0; i <= MaxRounds; i++ {
		// The last round is asked without the tool, so a model that would keep
		// searching has to answer with what it has instead of being cut off
		// mid-loop with nothing to show.
		tools := []ollama.Tool{Tool}
		if i == MaxRounds {
			tools = nil
		}

		// Nothing is streamed while a tool might still be called: the caller's
		// row would fill with a preamble that the real answer then replaces.
		stream := onDelta
		if tools != nil {
			stream = nil
		}

		msg, stats, err := r.Client.ChatTools(ctx, r.Model, conv, r.Options, r.Think, tools, stream)
		if err != nil {
			return msg, stats, rounds, err
		}
		calls := searchCalls(msg)
		if len(calls) == 0 {
			// The answer. When it was produced without streaming, because the
			// tool was still on the table, it is handed to the caller in one
			// piece so nothing is lost.
			if stream == nil && onDelta != nil && msg.Content != "" {
				onDelta(ollama.Delta{Content: msg.Content})
			}
			return msg, stats, rounds, nil
		}

		conv = append(conv, msg)
		for _, q := range calls {
			round := r.search(ctx, q)
			rounds = append(rounds, round)
			if r.OnRound != nil {
				r.OnRound(round)
			}
			conv = append(conv, ollama.Message{
				Role:     ollama.RoleTool,
				ToolName: ToolName,
				Content:  r.render(round),
			})
		}
		if ctx.Err() != nil {
			return ollama.Message{}, ollama.Stats{}, rounds, ctx.Err()
		}
	}
	return ollama.Message{}, ollama.Stats{}, rounds,
		fmt.Errorf("the model kept searching without answering")
}

// search runs one query.
func (r *Runner) search(ctx context.Context, query string) Round {
	n := r.Results
	if n <= 0 {
		n = DefaultResults
	}
	sctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	results, err := r.Provider.Search(sctx, query, n)
	if err != nil {
		log.Printf("astral: web search for %q: %v", query, err)
	}
	return Round{Query: query, Results: results, Err: err}
}

// render is what goes back to the model, including when the search failed.
//
// A failure is reported rather than hidden, because the alternative is a model
// that waits for results that are never coming and then invents some. Told the
// search failed, it answers from what it knows and says that is what it did.
func (r *Runner) render(round Round) string {
	if round.Err != nil {
		return "The search could not be run: " + round.Err.Error() +
			"\n\nAnswer from what you already know, and say that you could not check."
	}
	return Render(round.Query, round.Results)
}

// searchCalls pulls the queries out of a reply's tool calls.
//
// Calls for anything other than this tool are ignored rather than refused: a
// model that invented a tool nobody offered is not going to be argued out of it,
// and the round it wasted is already spent.
func searchCalls(msg ollama.Message) []string {
	var out []string
	for _, c := range msg.ToolCalls {
		if c.Function.Name != ToolName {
			continue
		}
		var args struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(c.Function.Arguments, &args); err != nil {
			// Some models answer with the arguments as a JSON string rather than
			// an object, which is worth one attempt to unwrap before giving up.
			var inner string
			if json.Unmarshal(c.Function.Arguments, &inner) == nil {
				_ = json.Unmarshal([]byte(inner), &args)
			}
		}
		if q := strings.TrimSpace(args.Query); q != "" {
			out = append(out, q)
		}
	}
	return out
}
