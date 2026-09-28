package chars

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"astral/internal/ollama"
)

func TestProfileDescription(t *testing.T) {
	p := Profile{Name: "Wren", Age: "27", Race: "half-elf", Appearance: "Tall, a scar through one brow.",
		Details: "Owes the guild money."}
	want := "Age: 27\nRace: half-elf\nAppearance: Tall, a scar through one brow.\n\nOwes the guild money."
	if got := p.Description(); got != want {
		t.Errorf("Description =\n%q\nwant\n%q", got, want)
	}
	if got := p.Facts(); got != "27, half-elf" {
		t.Errorf("Facts = %q", got)
	}
	if got := (Profile{}).DisplayName(); got != DefaultPersonaName {
		t.Errorf("an unnamed persona is %q", got)
	}
}

func TestParsePersona(t *testing.T) {
	p, err := ParsePersona([]byte(`{"name":" Wren ","age":"27","gender":"woman","race":"elf",` +
		`"appearance":"Tall.\\nPale.","personality":"","background":"","details":""}`))
	if err != nil || p.Name != "Wren" || p.Appearance != "Tall.\nPale." {
		t.Errorf("got %+v, %v", p, err)
	}
	if _, err := ParsePersona([]byte(`{"name":""}`)); err == nil {
		t.Error("a persona with no name was accepted")
	}
}

// A persona built without its facts is asked for them alone, and keeps the
// ones it had.
func TestPersonaBuildAsksAgainForMissingFacts(t *testing.T) {
	var asked [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Format struct {
				Required []string `json:"required"`
			} `json:"format"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		asked = append(asked, req.Format.Required)
		content := `{"name": "Christian", "age": "", "gender": "man", "race": "", "appearance": "", ` +
			`"personality": "Wry.", "background": "A courier."}`
		if len(asked) > 1 {
			content = `{"age": "31", "race": "human", "appearance": "Lean, dark hair, a courier's satchel."}`
		}
		resp, _ := json.Marshal(map[string]any{
			"message": map[string]string{"role": "assistant", "content": content},
			"done":    true, "done_reason": "stop",
		})
		fmt.Fprintln(w, string(resp))
	}))
	defer srv.Close()

	p, err := BuildPersonaFromConversation(context.Background(), ollama.NewClient(srv.URL), "m",
		[]ollama.Message{{Role: ollama.RoleUser, Content: "a human courier called Christian, 31, lean"}}, ollama.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || strings.Join(asked[1], ",") != "age,race,appearance" {
		t.Fatalf("asked %v, want the build and then age, race and appearance alone", asked)
	}
	if p.Age != "31" || p.Race != "human" || p.Gender != "man" || !strings.Contains(p.Appearance, "satchel") {
		t.Errorf("persona after asking again: %+v", p)
	}
}
