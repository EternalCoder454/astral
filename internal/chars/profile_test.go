package chars

import "testing"

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
