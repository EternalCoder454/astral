package chars

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

var (
	// Referring to the user in the third person, which is the failure.
	thirdPerson  = regexp.MustCompile(`(?i)\b(his|her|their) (sister|brother)\b`)
	secondPerson = regexp.MustCompile(`(?i)\byour (sister|brother)\b`)
)

// TestLivePersonDriftStaysInSecondPerson is the rule that breaks the illusion
// fastest when it goes: a character who calls the person they are speaking to
// "her sister" has stopped speaking to them and started speaking about them.
//
// The recap below is the cause, not the scenery. A scene of any length carries
// one in every prompt, it is written in the third person about everyone in it,
// and the user is one of those people, so by turn thirty the model has read
// "Wren has not told her sister" forty times and writes the same way.
func TestLivePersonDriftStaysInSecondPerson(t *testing.T) {
	client, model := liveModel(t)

	c := Character{
		Name:        "Mira",
		Description: "{{user}}'s older sister. Shares the flat with them and always has.",
		Personality: "blunt, fond, never apologises first",
		Scenario:    "The kitchen, late. {{user}} has just come in.",
		FirstMes:    `*She did not turn round.* "You're back late."`,
	}
	p := Persona{Name: "Wren", Description: "Works nights at the depot."}

	// A recap, which a scene of any length carries in every prompt and which is
	// written in the third person about everyone including the user. If anything
	// teaches the model to call the user "she", this is the candidate.
	recap := `Wren came home late from the depot for the third night this week. ` +
		`Mira waited up for her, as she has every night since their father was moved to the ward. ` +
		`Wren has not told her sister that she is being moved to days. ` +
		`Mira asked her about it once and Wren changed the subject.`

	prompts := []string{
		`*I drop my bag by the door.* "Long shift."`,
		`"Did you eat?"`,
		`*I sit down across from her.* "You waited up."`,
		`"Tell me about the thing with Dad."`,
	}
	var hist []ollama.Message
	hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: Greeting(c, p)})

	third, second, named := 0, 0, 0
	for _, q := range prompts {
		hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: q})
		sc := Scene{Persona: p, History: hist, Recap: recap,
			Budget: Plan(8192, 400, len(BuildSystem(c, p)))}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		no := false
		reply, _, err := client.Chat(ctx, model, BuildMessages(c, sc),
			ollama.Options{NumCtx: 8192, Temperature: 0.8, NumPredict: 400}, &no, nil)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		_, body := ollama.SplitThinking(reply.Content)
		body = strings.TrimSpace(body)
		hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: body})
		third += len(thirdPerson.FindAllString(body, -1))
		second += len(secondPerson.FindAllString(body, -1))
		named += strings.Count(body, "Wren")
		t.Logf("--- reply ---\n%s", body)
	}
	t.Logf("third-person refs %d | second-person refs %d | user named %d", third, second, named)
	if third > 0 {
		t.Errorf("the model referred to the user in the third person %d times across %d turns, "+
			"where it should be writing to them as you", third, len(prompts))
	}
}
