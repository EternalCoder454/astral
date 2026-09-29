package chars

import (
	"strings"
	"testing"
)

func TestStatePartsCountsTheFields(t *testing.T) {
	if len(StateFields) != stateParts {
		t.Errorf("stateParts is %d but there are %d StateFields: the budget counts the wrong number", stateParts, len(StateFields))
	}
}

// The NOW block carries where and when and whatever of the state is written,
// labelled, and nothing when there is nothing.
func TestSettingBlockCarriesTheState(t *testing.T) {
	if got := SettingBlock("", SceneState{}, "Vesper", "Wren"); got != "" {
		t.Errorf("an empty setting and state sent %q", got)
	}
	got := SettingBlock("The ferry cabin, near dawn", SceneState{Wearing: "{{char}}: oilskin coat", Between: "wary"}, "Vesper", "Wren")
	for _, want := range []string{"Where and when: The ferry cabin, near dawn", "Wearing: Vesper: oilskin coat",
		"Between Vesper and Wren: wary", "not a list of things to mention"} {
		if !strings.Contains(got, want) {
			t.Errorf("the block does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Holding") {
		t.Errorf("an empty part was sent:\n%s", got)
	}
}

// An answer changes only the parts it gives, cleaned, and says whether it
// changed anything; stand-ins for nothing change nothing.
func TestApplyStateTakesOnlyWhatChanged(t *testing.T) {
	was := SceneState{Wearing: "She: oilskin coat", Holding: "You: the brass key"}
	raw := []byte(`{"where":"The ferry cabin, near dawn","wearing":"She: shirtsleeves, coat on the hook","holding":"same","unresolved":"  \"Meet at the bell at dawn\"  "}`)
	setting, state, changed := ApplyState(raw, "The dock", was)
	if !changed || setting != "The ferry cabin, near dawn" {
		t.Errorf("setting %q, changed %v", setting, changed)
	}
	if state.Wearing != "She: shirtsleeves, coat on the hook" || state.Holding != "You: the brass key" ||
		state.Unresolved != "Meet at the bell at dawn" || state.Between != "" {
		t.Errorf("state %+v", state)
	}
	if _, _, changed := ApplyState([]byte(`{}`), "The dock", was); changed {
		t.Error("an empty answer changed the state")
	}
	if _, _, changed := ApplyState([]byte(`{"wearing":"She: oilskin coat"}`), "The dock", was); changed {
		t.Error("the same wearing counted as a change")
	}
}

// Every part is required and bounded, and "same" changes nothing.
func TestStateSchema(t *testing.T) {
	s := string(StateSchema(false))
	if !strings.Contains(s, `"required":["where","wearing","holding","relationship","unresolved"]`) ||
		!strings.Contains(s, `"maxLength":160`) {
		t.Errorf("schema: %s", s)
	}
	if _, _, changed := ApplyState([]byte(`{"where":"same","wearing":"Same.","holding":"same","relationship":"same","unresolved":"same"}`),
		"The dock", SceneState{Wearing: "a coat"}); changed {
		t.Error("an answer of all same changed the state")
	}
}

// "none" clears a part that no longer holds; "same" and silence keep it; and
// where the scene is is never cleared.
func TestApplyStateClearsWhatNoLongerHolds(t *testing.T) {
	was := SceneState{Holding: "You: the brass key", Unresolved: "The quarrel"}
	setting, state, changed := ApplyState([]byte(`{"where":"none","holding":"none","unresolved":"same"}`), "The dock", was)
	if !changed || state.Holding != "" || state.Unresolved != "The quarrel" || setting != "The dock" {
		t.Errorf("changed %v, setting %q, state %+v", changed, setting, state)
	}
	if _, _, changed := ApplyState([]byte(`{"wearing":"none"}`), "The dock", SceneState{}); changed {
		t.Error("clearing an empty part counted as a change")
	}
}
