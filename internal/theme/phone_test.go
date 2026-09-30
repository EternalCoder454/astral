package theme

import "testing"

var phoneRoleNames = []string{
	"primary", "on-primary", "primary-container", "on-primary-container",
	"surface", "surface-container-lowest", "surface-container-low", "surface-container",
	"surface-container-high", "surface-container-highest",
	"on-surface", "on-surface-variant", "outline", "outline-variant", "error",
}

func TestPhoneRolesAreCompleteAndReadable(t *testing.T) {
	for _, th := range Themes {
		p := PhoneRoles(th)
		if p.Dark != th.Dark {
			t.Errorf("%s: dark = %v", th.ID, p.Dark)
		}
		for _, r := range phoneRoleNames {
			if _, err := parseHex(p.Roles[r]); err != nil {
				t.Errorf("%s: role %s = %q", th.ID, r, p.Roles[r])
			}
		}
		for _, bg := range []string{"surface", "surface-container-lowest", "surface-container-low",
			"surface-container", "surface-container-high", "surface-container-highest"} {
			if c := contrastHex(p.Roles["on-surface"], p.Roles[bg]); c < 7 {
				t.Errorf("%s: on-surface on %s is %.2f:1", th.ID, bg, c)
			}
		}
		if c := contrastHex(p.Roles["on-primary"], p.Roles["primary"]); c < 4.5 {
			t.Errorf("%s: on-primary on primary is %.2f:1", th.ID, c)
		}
		t.Logf("%s: worst text %.2f, button %.2f", th.ID,
			contrastHex(p.Roles["on-surface"], p.Roles["surface-container-highest"]),
			contrastHex(p.Roles["on-primary"], p.Roles["primary"]))
	}
}
