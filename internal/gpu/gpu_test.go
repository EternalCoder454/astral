package gpu

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fakeCard(t *testing.T, root, name string, total, used string) {
	t.Helper()
	dir := filepath.Join(root, name, "device")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "mem_info_vram_total"), []byte(total+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "mem_info_vram_used"), []byte(used+"\n"), 0o644)
}

func withRoot(t *testing.T, root string) {
	t.Helper()
	old := sysfsRoot
	sysfsRoot = root
	t.Cleanup(func() { sysfsRoot = old })
}

func noNVIDIA(t *testing.T) {
	t.Helper()
	old := nvidiaSMI
	nvidiaSMI = func() ([]byte, error) { return nil, errors.New("absent") }
	t.Cleanup(func() { nvidiaSMI = old })
}

func TestReadPicksTheLargestAMDCard(t *testing.T) {
	root := t.TempDir()
	withRoot(t, root)
	noNVIDIA(t)
	// An integrated GPU's carve-out beside a discrete card.
	fakeCard(t, root, "card0", "536870912", "100000000")
	fakeCard(t, root, "card1", "25753026560", "6617329664")

	m, ok := Read()
	if !ok {
		t.Fatal("could not read the cards")
	}
	if m.Total != 25753026560 || m.Used != 6617329664 {
		t.Errorf("read the wrong card: %+v", m)
	}
	if m.Free() != 25753026560-6617329664 {
		t.Errorf("free is %d", m.Free())
	}
}

func TestReadNVIDIAWhenThereIsNoAMD(t *testing.T) {
	withRoot(t, t.TempDir())
	old := nvidiaSMI
	nvidiaSMI = func() ([]byte, error) { return []byte("24576, 2048\n8192, 100\n"), nil }
	t.Cleanup(func() { nvidiaSMI = old })

	m, ok := Read()
	if !ok || m.Total != 24576<<20 || m.Used != 2048<<20 {
		t.Errorf("got %+v, %v", m, ok)
	}
}

func TestUnknownHardwareFits(t *testing.T) {
	// Refusing to load a model on a machine this cannot read would break the
	// app for everyone it does not know about.
	withRoot(t, t.TempDir())
	noNVIDIA(t)
	if !Fits(100 << 30) {
		t.Error("unknown hardware was treated as full")
	}
}

func TestFitsKeepsTheDesktopReserve(t *testing.T) {
	root := t.TempDir()
	withRoot(t, root)
	noNVIDIA(t)
	// 24 GiB card with 18 GiB in use: 6 GiB free.
	fakeCard(t, root, "card1", "25769803776", "19327352832")
	if !Fits(2 << 30) {
		t.Error("2 GiB should fit in 6 GiB free with a 3 GiB reserve")
	}
	if Fits(4 << 30) {
		t.Error("4 GiB fitted by eating into the desktop's reserve")
	}
}

func TestUsedAboveTotalIsNotNegative(t *testing.T) {
	if (Memory{Total: 10, Used: 20}).Free() != 0 {
		t.Error("free underflowed")
	}
}
