//go:build linux

package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The phone app carries its own version string, because Gradle has no way to
// read a Go constant. Two places holding the same number is two places to
// forget, and the way it would show up is an app that calls itself 0.2.0 on its
// own settings screen while sitting in a file named 0.3.1.
//
// The build names the file from version.go, so that is the one that is right
// and this holds the other to it.
func TestTheAndroidVersionMatches(t *testing.T) {
	gradle := filepath.Join("..", "..", "packaging", "android", "app", "build.gradle.kts")
	b, err := os.ReadFile(gradle)
	if err != nil {
		t.Skipf("no android project here: %v", err)
	}
	src := string(b)

	m := regexp.MustCompile(`versionName\s*=\s*"([^"]+)"`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("build.gradle.kts has no versionName")
	}
	if m[1] != version {
		t.Errorf("the phone app says %q and Astral is %q", m[1], version)
	}

	// versionCode is what Android upgrades by, and it only ever goes up. A
	// build that forgets it installs as the same version and the old one stays.
	c := regexp.MustCompile(`versionCode\s*=\s*(\d+)`).FindStringSubmatch(src)
	if c == nil {
		t.Fatal("build.gradle.kts has no versionCode")
	}
	code, err := strconv.Atoi(c[1])
	if err != nil || code < 1 {
		t.Fatalf("versionCode is %q", c[1])
	}
	// Three numbers, so it can be compared with the one before it.
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		t.Fatalf("version %q is not three parts, so this cannot check the code", version)
	}
}
