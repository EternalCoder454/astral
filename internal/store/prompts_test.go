package store

import (
	"path/filepath"
	"testing"
)

func TestPromptOverrides(t *testing.T) {
	s, _, err := Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetPromptOverride("scene.framing", "first"); err != nil {
		t.Fatal(err)
	}
	s.SetPromptOverride("scene.framing", "second")
	s.SetPromptOverride("chat.assistant", "mine")
	got, err := s.PromptOverrides()
	if err != nil || len(got) != 2 || got["scene.framing"] != "second" {
		t.Fatalf("got %v %v", got, err)
	}
	s.DeletePromptOverride("scene.framing")
	got, _ = s.PromptOverrides()
	if _, ok := got["scene.framing"]; ok || got["chat.assistant"] != "mine" {
		t.Errorf("after putting one back: %v", got)
	}
}
