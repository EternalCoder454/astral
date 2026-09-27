package update

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryVersionAppearsOnce is a small test for a mistake that is invisible
// where it matters. The parser reads the first heading and stops, so a second
// section for the same version passes every other check and ships half the
// release notes: the ones written in whichever commit happened to come last.
//
// It happened. Two commits each added a "## 0.4.8" block and the file went out
// with both, the app showing only the shorter one.
func TestEveryVersionAppearsOnce(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "WHATSNEW.md"))
	if err != nil {
		t.Fatalf("WHATSNEW.md: %v", err)
	}
	heading := regexp.MustCompile(`(?m)^##\s+(\S+)\s*$`)
	seen := map[string]int{}
	var order []string
	for _, m := range heading.FindAllStringSubmatch(string(data), -1) {
		v := strings.TrimSpace(m[1])
		if seen[v] == 0 {
			order = append(order, v)
		}
		seen[v]++
	}
	if len(order) == 0 {
		t.Fatal("no version headings at all")
	}
	for _, v := range order {
		if seen[v] > 1 {
			t.Errorf("version %s has %d sections; the app would show only the first", v, seen[v])
		}
	}
}
