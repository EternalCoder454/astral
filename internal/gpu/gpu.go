// Package gpu reads how much video memory is free, where the system says.
//
// Astral does not run models itself, Ollama does, but it decides which models
// get loaded and when, and on Linux that decision can take the desktop down. The
// AMD driver does not spill video memory into system RAM the way Windows does:
// when a model fills the card, the driver refuses the compositor's next command
// submission and every window drawing to the screen crashes at once. Loading a
// second model beside a large one is exactly how that happens, so Astral asks
// how much room there is before it does anything that would load one.
//
// Knowing is best effort. An unrecognised card reports nothing, and callers
// treat "unknown" as "go ahead as before", because refusing to work on hardware
// this cannot read would be worse than the problem it guards against.
package gpu

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Memory is one card's video memory, in bytes.
type Memory struct {
	Total uint64
	Used  uint64
}

// Free is what is left.
func (m Memory) Free() uint64 {
	if m.Used >= m.Total {
		return 0
	}
	return m.Total - m.Used
}

// sysfsRoot is where the DRM devices are, swapped out by the tests.
var sysfsRoot = "/sys/class/drm"

// Read returns the video memory of the largest card, and false when it cannot
// be read.
//
// The largest, because that is the one Ollama puts a model on; an integrated
// GPU beside it has a few hundred megabytes of carve-out and is not where the
// question is being asked.
func Read() (Memory, bool) {
	if m, ok := readAMD(); ok {
		return m, true
	}
	return readNVIDIA()
}

// readAMD reads the amdgpu driver's counters, which it publishes per device.
func readAMD() (Memory, bool) {
	cards, _ := filepath.Glob(filepath.Join(sysfsRoot, "card*", "device", "mem_info_vram_total"))
	var best Memory
	found := false
	for _, totalPath := range cards {
		dir := filepath.Dir(totalPath)
		total, ok1 := readUint(totalPath)
		used, ok2 := readUint(filepath.Join(dir, "mem_info_vram_used"))
		if !ok1 || !ok2 || total == 0 {
			continue
		}
		if total > best.Total {
			best, found = Memory{Total: total, Used: used}, true
		}
	}
	return best, found
}

func readUint(path string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n, err == nil
}

// nvidiaSMI runs the query, swapped out by the tests. A short timeout, because
// this is asked on the way to a reply and a driver that hangs must not hold it.
var nvidiaSMI = func() ([]byte, error) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path,
		"--query-gpu=memory.total,memory.used", "--format=csv,noheader,nounits").Output()
}

// readNVIDIA asks nvidia-smi, which reports mebibytes, one card per line.
func readNVIDIA() (Memory, bool) {
	out, err := nvidiaSMI()
	if err != nil {
		return Memory{}, false
	}
	var best Memory
	found := false
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		parts := strings.Split(string(line), ",")
		if len(parts) != 2 {
			continue
		}
		total, err1 := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
		used, err2 := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		if err1 != nil || err2 != nil || total == 0 {
			continue
		}
		m := Memory{Total: total << 20, Used: used << 20}
		if m.Total > best.Total {
			best, found = m, true
		}
	}
	return best, found
}

// DesktopReserve is the video memory left alone for everything that is not a
// model: the compositor, the browser, this window. Three gigabytes is what the
// crash that prompted this needed and did not have.
const DesktopReserve = 3 << 30

// Fits reports whether something of this size can be loaded now without eating
// into the reserve. Unknown hardware fits, for the reason in the package
// comment.
func Fits(size uint64) bool {
	m, ok := Read()
	if !ok {
		return true
	}
	return m.Free() >= size+DesktopReserve
}
