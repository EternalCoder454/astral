package prompts

import "testing"

func TestTextPrefersTheRewrite(t *testing.T) {
	id := Register(Prompt{ID: "test.one", Name: "One", Group: "Scenes", Default: "astral's"})
	if Text(id) != "astral's" || Overridden(id) {
		t.Fatal("the default is not what is sent")
	}
	SetOverrides(map[string]string{id: "mine", "gone.prompt": "kept"})
	if Text(id) != "mine" || !Overridden(id) {
		t.Error("the rewrite is not what is sent")
	}
	// An empty rewrite is no rewrite: sending nothing is never what was meant.
	SetOverrides(map[string]string{id: "  "})
	if Text(id) != "astral's" {
		t.Error("an empty rewrite replaced the prompt")
	}
	SetOverrides(nil)
}
