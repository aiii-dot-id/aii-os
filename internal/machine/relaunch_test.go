package machine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TWO IDENTITY HOSTS THAT FIND THE LINK GONE START EXACTLY ONE SERVICE.
// On Windows nothing restarts the service, so a host whose link has been
// gone a few seconds starts it; two hosts doing so at once start two
// processes, and the lock lets exactly one hold the link, while the other
// ends with ErrHeld. Both hosts then link to the one. Each host's start
// here waits for the other's, so the race is certain (the shared budget's
// spacing is shortened to let both through), and starts a service in this
// process through the same Relaunch seam the Windows build fills with the
// account's task.
func TestHostsThatFindTheLinkGoneStartExactlyOneService(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	// The registration made the service's directory (no directory, no
	// registration: no host starts a service that has none).
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	a, b := slotDir(t, root, "identity-0"), slotDir(t, root, "identity-1")
	opts := Options{Dir: dir, StartupSlots: startupSlots(a, b)}

	var mu sync.Mutex
	var started []*running
	var barrier sync.WaitGroup
	barrier.Add(2)
	first := map[string]bool{}
	relaunch := func(host string) func() error {
		return func() error {
			mu.Lock()
			isFirst := !first[host]
			first[host] = true
			mu.Unlock()
			if isFirst {
				barrier.Done()
				barrier.Wait()
			}
			r := startAsync(t, opts)
			mu.Lock()
			started = append(started, r)
			mu.Unlock()
			return nil
		}
	}
	ca := runClient(t, ClientOptions{Dir: dir, Slot: a, Key: newKey(t), Relaunch: relaunch("a"), RelaunchAfter: 100 * time.Millisecond, budget: fastBudget(dir)})
	cb := runClient(t, ClientOptions{Dir: dir, Slot: b, Key: newKey(t), Relaunch: relaunch("b"), RelaunchAfter: 100 * time.Millisecond, budget: fastBudget(dir)})
	waitState(t, ca, StateLinked)
	waitState(t, cb, StateLinked)
	waitFor(t, "both hosts see both linked", func() bool { return ca.Snapshot().Linked == 2 && cb.Snapshot().Linked == 2 })

	mu.Lock()
	defer mu.Unlock()
	if len(started) < 2 {
		t.Fatalf("the race did not happen: %d starts", len(started))
	}
	holding := 0
	for _, r := range started {
		if !r.done() {
			holding++
			continue
		}
		if !errors.Is(r.err, ErrHeld) {
			t.Errorf("a start that lost the race ended with %v, want ErrHeld", r.err)
		}
	}
	if holding != 1 {
		t.Fatalf("%d services hold the link after %d starts; want exactly one", holding, len(started))
	}
}

// A HOST WHOSE MANAGER RESTARTS THE SERVICE STARTS NOTHING (Linux, macOS):
// without Relaunch the link stays down and says so.
func TestAHostWithoutRelaunchStartsNothing(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-0")
	c := runClient(t, ClientOptions{Dir: dir, Slot: slot, Key: newKey(t), RelaunchAfter: 50 * time.Millisecond})
	s := waitState(t, c, StateDown)
	if s.Error == "" {
		t.Fatalf("a down link must say what it met: %+v", s)
	}
	time.Sleep(300 * time.Millisecond)
	if c.Snapshot().State != StateDown {
		t.Fatalf("a link with nothing to start came up: %+v", c.Snapshot())
	}
}

// fastBudget is the hosts' shared budget with its spacing in milliseconds.
func fastBudget(dir string) *budget {
	b := newBudget(dir)
	b.first, b.max = time.Millisecond, 4*time.Millisecond
	return &b
}

// THE HOSTS SHARE ONE START BUDGET. Two hosts whose service never comes up
// start it, between them, no more than the cap within the window — not
// each, and not forever — and then both say the service keeps failing,
// with the failure the service recorded. A host that links clears the
// count.
func TestTheHostsShareOneStartBudget(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := RecordFailure(dir, errors.New("state.json does not read")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	starts := 0
	never := func() error { mu.Lock(); starts++; mu.Unlock(); return nil }
	opts := func(slot string) ClientOptions {
		b := fastBudget(dir)
		b.limit, b.window = 3, time.Minute
		return ClientOptions{Dir: dir, Slot: slotDir(t, root, slot), Key: newKey(t), Relaunch: never, RelaunchAfter: 10 * time.Millisecond, budget: b}
	}
	ca, cb := runClient(t, opts("identity-0")), runClient(t, opts("identity-1"))
	for _, c := range []*Client{ca, cb} {
		waitFor(t, "the host says the service keeps failing", func() bool { return c.Snapshot().Failing })
		s := c.Snapshot()
		if !strings.Contains(s.Error, "keeps failing") || !strings.Contains(s.Error, "state.json does not read") || !strings.Contains(s.Error, "3 times") {
			t.Fatalf("the failing line must say how often and why: %q", s.Error)
		}
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	n := starts
	mu.Unlock()
	if n != 3 {
		t.Fatalf("the two hosts started the service %d times; the shared cap is 3", n)
	}

	// The service comes up: a host links and clears the count. The past
	// failure is read once a host has linked, not when the link first
	// takes a connection: the socket listens before the service has
	// opened its store and removed the record, and it answers no host
	// until it has.
	slot := slotDir(t, root, "identity-2")
	serve(t, Options{Dir: dir, StartupSlots: startupSlots(slot)})
	c := runClient(t, ClientOptions{Dir: dir, Slot: slot, Key: newKey(t), Relaunch: never, budget: fastBudget(dir)})
	waitState(t, c, StateLinked)
	if _, err := os.Stat(filepath.Join(dir, failureFile)); !os.IsNotExist(err) {
		t.Fatalf("a service that holds its link keeps a past failure: %v", err)
	}
	waitFor(t, "a linked host clears the count", func() bool {
		r, err := newBudget(dir).load()
		return err == nil && len(r.Starts) == 0
	})
}

// A START IS SPREAD OVER ITS WAIT'S LAST QUARTER: hosts that met the same
// failure at once do not ask again at once.
func TestAStartWaitIsSpreadOverItsLastQuarter(t *testing.T) {
	d := 8 * time.Second
	seen := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		got := spread(d)
		if got < 6*time.Second || got > d {
			t.Fatalf("spread(%s) = %s, outside its last quarter", d, got)
		}
		seen[got] = true
	}
	if len(seen) < 10 {
		t.Fatalf("spread is not spread: %d distinct waits in 200", len(seen))
	}
}
