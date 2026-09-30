package app

import (
	"runtime"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// Handing memory back after switching chats.
//
// A chat left behind is freed, widgets and all, but the C allocator keeps
// what GTK freed for its next allocation rather than returning it, so
// resident memory climbed with every chat opened and stayed there. Measured
// by opening 150 chats four times over, with the memory handed back after
// each round: 381 MB at the end without that and about 320 MB with it.

// tidyAfter is how long after the last chat was opened the tidy runs: long
// enough that it never lands in the middle of looking through chats.
const tidyAfter = 20 * time.Second

// tidyPasses is how many collections the tidy makes, a second apart. A closed
// chat's widgets come apart over a few, since each lets GTK free what the one
// before let go of.
const tidyPasses = 4

// scheduleTidy asks for a tidy tidyAfter from now, moving any already asked
// for.
func (a *App) scheduleTidy() {
	a.tidyAt = time.Now().Add(tidyAfter)
	if a.tidyWaiting {
		return
	}
	a.tidyWaiting = true
	coreglib.TimeoutSecondsAdd(uint(tidyAfter/time.Second), a.tidyWhenDue)
}

// tidyWhenDue runs the tidy, or waits again when a chat was opened since it
// was asked for.
func (a *App) tidyWhenDue() bool {
	if wait := time.Until(a.tidyAt); wait > 0 {
		coreglib.TimeoutAdd(uint(wait/time.Millisecond)+1, a.tidyWhenDue)
		return false
	}
	a.tidyWaiting = false
	// Off the main thread, which the collections would otherwise hold up: GTK
	// frees what they let go of on the main loop in between, which is why
	// they are a second apart.
	go func() {
		for i := 0; i < tidyPasses; i++ {
			runtime.GC()
			time.Sleep(time.Second)
		}
		trimHeap()
	}()
	return false
}
