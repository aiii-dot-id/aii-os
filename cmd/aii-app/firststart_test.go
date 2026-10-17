//go:build (darwin && !ios) || (linux && !android) || windows

package main

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// FIRST STARTS AT ONCE MAKE ONE SLOT. Eight launches of an account that has
// never run AII OS: each used to look, find no slot and run aii init — as
// many slots as launches, or, for those that chose the same number, a
// refusal in place of a dashboard. Under the first-start lock one of them
// runs init, once, and every one of them is answered identity-0.
//
// create is what aii init does, in this process: the next free number,
// then the slot. The pause between the two is the window in which the
// others would have chosen the same number.
func TestFirstStartsAtOnceMakeExactlyOneSlot(t *testing.T) {
	root := filepath.Join(t.TempDir(), install.Dir) // not there yet
	var inits atomic.Int32
	create := func() error {
		inits.Add(1)
		n, err := install.NextSlot(root)
		if err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
		_, err = install.Create(root, n)
		return err
	}
	type answer struct {
		n       int
		created bool
		err     error
	}
	answers := make([]answer, 8)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			a := &answers[i]
			a.n, a.created, a.err = firstSlot(root, create)
		}()
	}
	close(start)
	wg.Wait()

	made := 0
	for i, a := range answers {
		if a.err != nil || a.n != 0 {
			t.Fatalf("launch %d was answered slot %d, %v; want %s", i, a.n, a.err, install.SlotName(0))
		}
		if a.created {
			made++
		}
	}
	if made != 1 || inits.Load() != 1 {
		t.Fatalf("%d launches say they made the slot and init ran %d times; want one and once", made, inits.Load())
	}
	if ns, err := install.Slots(root); err != nil || !slices.Equal(ns, []int{0}) {
		t.Fatalf("the account's slots are %v, %v; want exactly %s — the lock's own file is no slot", ns, err, install.SlotName(0))
	}
}

// A LAUNCH WAITS FOR THE ONE AHEAD OF IT, AND NOT FOR EVER. While another
// holds the first-start lock, a launch makes nothing and, its wait run out,
// says who it waited for. Once the lock is let go the next launch goes
// through.
func TestALaunchBehindAnotherWaitsThenSaysSoAndMakesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), install.Dir)
	release, err := lockFirstStart(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	saved := firstStartWait
	firstStartWait = 300 * time.Millisecond
	t.Cleanup(func() { firstStartWait = saved })

	_, _, err = firstSlot(root, func() error {
		t.Error("a launch behind another ran aii init")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "Another launch of AII OS is still setting up") || !strings.HasSuffix(err.Error(), "Nothing was changed.") {
		t.Fatalf("a launch behind another, its wait run out, said: %v", err)
	}
	if ns, err := install.Slots(root); err != nil || len(ns) != 0 {
		t.Fatalf("nothing was to be changed, and the account's slots are %v, %v", ns, err)
	}

	release()
	n, created, err := firstSlot(root, func() error {
		_, err := install.Create(root, 0)
		return err
	})
	if err != nil || n != 0 || !created {
		t.Fatalf("the lock let go, the next launch: slot %d, created %v, %v; want %s made", n, created, err, install.SlotName(0))
	}
}
