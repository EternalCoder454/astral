package ui

import (
	"testing"
	"time"
)

// The lane that compaction and the lorebook pass share. They had a flag each
// once, which meant both could be generating at the same time against one
// model, on top of whatever the user did next.
func TestBgWorkHoldsOneAtATime(t *testing.T) {
	var b bgWork

	ctx, ok := b.take("recap", time.Minute)
	if !ok || ctx == nil {
		t.Fatal("could not take a free lane")
	}
	if _, ok := b.take("lorebook", time.Minute); ok {
		t.Error("a second pass took a lane that was already held")
	}
	if !b.running {
		t.Error("the lane does not report itself as running")
	}

	b.done()
	if b.running {
		t.Error("the lane is still held after done")
	}
	if ctx.Err() == nil {
		t.Error("done did not cancel the work's context")
	}
	if _, ok := b.take("lorebook", time.Minute); !ok {
		t.Error("the lane could not be taken again after being released")
	}
}

// The user's turn takes the lane from whatever housekeeping is in it. The
// interrupted pass has to see that, so it can stop rather than write a result
// nobody is waiting for any more.
func TestBgWorkYieldsToTheUser(t *testing.T) {
	var b bgWork
	ctx, _ := b.take("recap", time.Minute)

	b.yield()
	if b.running {
		t.Error("the lane is still held after yielding")
	}
	if ctx.Err() == nil {
		t.Error("yield did not cancel the work in flight")
	}
	// Yielding an empty lane is what happens on most turns, and must be
	// harmless rather than a panic on a nil cancel.
	b.yield()
}

// A pass that finishes normally releases the lane exactly once, and a second
// release must not panic on the nil cancel it left behind.
func TestBgWorkDoneIsIdempotent(t *testing.T) {
	var b bgWork
	b.take("recap", time.Minute)
	b.done()
	b.done()
	if b.running {
		t.Error("the lane is held after two releases")
	}
}

// The timeout is the work's own, not the lane's: a pass that hangs has to give
// the lane back rather than holding it until the app closes.
func TestBgWorkContextCarriesTheTimeout(t *testing.T) {
	var b bgWork
	ctx, _ := b.take("recap", 40*time.Millisecond)
	defer b.done()

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Error("the work's context never expired")
	}
}
