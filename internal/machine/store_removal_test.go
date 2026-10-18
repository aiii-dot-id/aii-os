//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// settled is the tree once the running service has finished opening its
// store. The service binds its link, then opens its store, and only then
// accepts (Serve): a dial connects from the first of these, so serve
// returns while the store's writer may still be at work, and the hello is
// said only after the last. settled takes the hello, and then looks: every
// name the store's writer makes (storeOwnNames) is there, and its
// hard-link probe (.probe-* in b/ and v/), its own, made and removed as it
// opens, is not listed (Windows may list a removed file until its last
// handle closes). The listing alone cannot say the store is open. A
// snapshot taken with the writer between its directories read the .trash
// it made next as a refused removal's doing (the 0.1.16 native macOS
// suite); one taken with every directory there and the probe still to
// come read the probe so (the 0.1.17 native Windows suite).
func settled(t *testing.T, dir string) []string {
	t.Helper()
	dialRaw(t, dir)
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := tree(t, dir)
		whole := !slices.ContainsFunc(got, func(e string) bool { return strings.Contains(e, ".probe-") })
		for _, n := range storeOwnNames {
			whole = whole && slices.ContainsFunc(got, func(e string) bool { return strings.HasPrefix(e, filepath.Join("store", n)+" ") })
		}
		if whole {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the service's store is not whole after 10 s: %v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// tree is every path under dir with its kind, sorted: what a test compares
// before and after.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, rel+" "+d.Type().String())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// removalRig is a stopped service whose store holds two views and an
// archive, with a draining store holding one view: what an uninstall finds
// once step 2 is done.
type removalRig struct {
	dir, store, drain string
	works             []pluginhost.StoreWork
}

func newRemovalRig(t *testing.T) removalRig {
	t.Helper()
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	a := viewWork(f.model("a.bin", []byte("the first model")))
	b := viewWork(f.model("b.bin", []byte("the second model")))
	r.ensure(a)
	r.ensure(b)
	r.archiveIn([]byte("an archive"))
	store := r.storeDir()
	// A store placed elsewhere and moved away from, still draining.
	drain := filepath.Join(t.TempDir(), "old-store")
	dw, err := plugstore.OpenWriter(drain)
	if err != nil {
		t.Fatal(err)
	}
	c := viewWork(f.model("c.bin", []byte("a model in the old store")))
	if err := pluginhost.EnsureEntry(t.Context(), dw, c, f.fetch, pluginhost.Adoption{}, nil); err != nil {
		t.Fatal(err)
	}
	dw.Close()
	if err := r.srv.updateState(func(st *State) error {
		st.Store, st.Draining = store, []string{drain}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.close()
	return removalRig{dir: r.dir, store: store, drain: drain, works: []pluginhost.StoreWork{a, b, c}}
}

func gone(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

func TestRemovalTakesEveryStoreAndTheServiceDirectory(t *testing.T) {
	rig := newRemovalRig(t)
	rem, err := Remove(rig.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !rem.Done() || !gone(t, rig.dir) || !gone(t, rig.store) || !gone(t, rig.drain) {
		t.Fatalf("not everything went: %+v", rem)
	}
	if rem.Freed <= 0 {
		t.Fatalf("no bytes freed were counted: %+v", rem)
	}
	// Twice is once: nothing is there, and that is done.
	again, err := Remove(rig.dir)
	if err != nil || !again.Done() {
		t.Fatalf("a second removal: %+v %v", again, err)
	}
}

func TestRemovalKeepsAStoreAnActivationPins(t *testing.T) {
	rig := newRemovalRig(t)
	key := strings.TrimPrefix(rig.works[0].Entry, "v/")
	pin, err := plugstore.Store{Dir: rig.store}.PinView(key)
	if err != nil {
		t.Fatal(err)
	}
	before := tree(t, rig.store)
	rem, err := Remove(rig.dir)
	if err != nil {
		t.Fatal(err)
	}
	if rem.Done() {
		t.Fatal("a removal with a pinned store said it was done")
	}
	kept := rem.store(rig.store)
	if kept == nil || kept.Gone || !strings.Contains(kept.Kept, rig.works[0].Entry) {
		t.Fatalf("the pinned store is not kept and named with its entry: %+v", rem)
	}
	if got := tree(t, rig.store); !slices.Equal(got, before) {
		t.Fatalf("a pinned store was touched:\n before %v\n after  %v", before, got)
	}
	if gone(t, filepath.Join(rig.dir, stateFileName)) {
		t.Fatal("state.json went while a store it names was kept")
	}
	if !gone(t, rig.drain) {
		t.Fatal("the store nothing pins was kept with the pinned one")
	}
	pin.Close()
	if rem, err := Remove(rig.dir); err != nil || !rem.Done() || !gone(t, rig.dir) || !gone(t, rig.store) {
		t.Fatalf("once let go: %+v %v", rem, err)
	}
}

func TestRemovalRefusesWhileTheServiceHoldsItsLock(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	serve(t, Options{Dir: dir})
	before := settled(t, dir)
	if _, err := Remove(dir); !errors.Is(err, ErrServiceRuns) {
		t.Fatalf("removal under a running service: %v, want ErrServiceRuns", err)
	}
	if got := tree(t, dir); !slices.Equal(got, before) {
		t.Fatalf("a refused removal touched the service directory:\n before %v\n after  %v", before, got)
	}
}

func TestRemovalLeavesWhatTheStoreDidNotMake(t *testing.T) {
	rig := newRemovalRig(t)
	foreign := []string{filepath.Join(rig.store, "notes.txt"), filepath.Join(rig.store, plugstore.DirBlobs, "readme"), filepath.Join(rig.dir, "mine.txt")}
	for _, p := range foreign {
		if err := os.WriteFile(p, []byte("not the store's"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rem, err := Remove(rig.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range foreign {
		if gone(t, p) {
			t.Errorf("%s, which nothing of AII OS made, was removed", p)
		}
	}
	s := rem.store(rig.store)
	if s == nil || !s.Gone || !slices.Contains(s.Left, "notes.txt") || !slices.Contains(s.Left, filepath.Join(plugstore.DirBlobs, "readme")) {
		t.Fatalf("the store's own names are not gone, or what stays is not named: %+v", s)
	}
	if !slices.Contains(rem.Left, "mine.txt") || rem.DirGone {
		t.Fatalf("the service directory's foreign file is not named as staying: %+v", rem)
	}
	for _, own := range []string{stateFileName, lockFileName} {
		if !gone(t, filepath.Join(rig.dir, own)) {
			t.Errorf("the service's own %s stayed", own)
		}
	}
	for _, own := range []string{plugstore.LockFile, plugstore.DirViews, plugstore.DirRoots, plugstore.DirArchives, plugstore.DirTrash} {
		if !gone(t, filepath.Join(rig.store, own)) {
			t.Errorf("the store's own %s stayed", own)
		}
	}
}

func TestAStoreOnAVolumeNotThereKeepsTheServiceDirectory(t *testing.T) {
	rig := newRemovalRig(t)
	unmounted := filepath.Join(t.TempDir(), "volume", "plugins")
	st := mustLoad(t, rig.dir)
	st.Draining = append(st.Draining, unmounted)
	if err := saveState(filepath.Join(rig.dir, stateFileName), st); err != nil {
		t.Fatal(err)
	}
	rem, err := Remove(rig.dir)
	if err != nil {
		t.Fatal(err)
	}
	if s := rem.store(unmounted); s == nil || s.Gone || !strings.Contains(s.Kept, "not there") {
		t.Fatalf("a store on a volume not there is not kept and named: %+v", rem)
	}
	if rem.Done() || gone(t, filepath.Join(rig.dir, stateFileName)) {
		t.Fatal("state.json, the only record of the unmounted store, went")
	}
	if !gone(t, rig.store) || !gone(t, rig.drain) {
		t.Fatal("the stores that are there were not taken")
	}
}

func TestADrainingStoreIsRemovedToo(t *testing.T) {
	rig := newRemovalRig(t)
	if gone(t, rig.drain) {
		t.Fatal("rig: no draining store")
	}
	if _, err := Remove(rig.dir); err != nil {
		t.Fatal(err)
	}
	if !gone(t, rig.drain) {
		t.Fatal("the draining store stayed")
	}
}

// A REMOVAL STOPPED AFTER ANY ONE STEP, THEN RUN AGAIN, ENDS WHERE ONE RUN
// ENDS: every step is idempotent, and state.json goes last, so a rerun
// always knows where the stores lie.
func TestRemovalResumesAfterAStopAtEveryStep(t *testing.T) {
	probe := newRemovalRig(t)
	steps := 0
	if _, err := removeSteps(probe.dir, func() bool { steps++; return true }); err != nil {
		t.Fatal(err)
	}
	if steps < 10 {
		t.Fatalf("the removal took only %d steps", steps)
	}
	t.Logf("one removal takes %d steps; it is stopped after each in turn", steps)
	for k := 1; k < steps; k++ {
		rig := newRemovalRig(t)
		n := 0
		if _, err := removeSteps(rig.dir, func() bool { n++; return n <= k }); !errors.Is(err, errStopped) {
			t.Fatalf("stop after step %d: %v", k, err)
		}
		if gone(t, filepath.Join(rig.dir, stateFileName)) && (!gone(t, rig.store) || !gone(t, rig.drain)) {
			t.Fatalf("after step %d state.json went while a store stayed", k)
		}
		rem, err := Remove(rig.dir)
		if err != nil || !rem.Done() || !gone(t, rig.dir) || !gone(t, rig.store) || !gone(t, rig.drain) {
			t.Fatalf("the rerun after a stop at step %d of %d did not end where one run ends: %+v %v", k, steps, rem, err)
		}
	}
}

func TestRemovalNeverFollowsALinkOutOfTheStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making a symbolic link needs a privilege the guest's account may lack; Linux and macOS cover the walk")
	}
	rig := newRemovalRig(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "precious")
	if err := os.MkdirAll(filepath.Join(target, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "inner", "file"), []byte("not the store's"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{
		filepath.Join(rig.store, plugstore.DirRoots, "0123456789abcdef"),  // a root that is a link
		filepath.Join(rig.store, plugstore.DirBlobs, "x.partial"),         // debris that is a link
		filepath.Join(rig.store, plugstore.DirTrash, "0123456789abcdef0"), // trash that is a link
		filepath.Join(rig.store, "elsewhere"),                             // a foreign link at the top
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	before := tree(t, outside)
	if _, err := Remove(rig.dir); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, outside); !slices.Equal(got, before) {
		t.Fatalf("removal followed a link out of the store:\n before %v\n after  %v", before, got)
	}
	// A store whose top is a link is not removed through it.
	rig2 := newRemovalRig(t)
	real := rig2.drain
	moved := filepath.Join(t.TempDir(), "real-store")
	if err := os.Rename(real, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, real); err != nil {
		t.Fatal(err)
	}
	beforeMoved := tree(t, moved)
	rem, err := Remove(rig2.dir)
	if err != nil {
		t.Fatal(err)
	}
	if s := rem.store(real); s == nil || s.Gone {
		t.Fatalf("a store reached through a link was taken: %+v", rem)
	}
	if got := tree(t, moved); !slices.Equal(got, beforeMoved) {
		t.Fatal("a store reached through a link was touched")
	}
}

// A STOP DURING A PENDING FETCH LEAVES NOTHING REMOVAL CANNOT TAKE: the
// service stopped mid-download leaves its pending entry in the state and
// its partial in the store, and both go as the store's own names.
func TestAStopDuringAPendingFetchLeavesNothingRemovalCannotTake(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-0")
	f := newFetches()
	model := f.model("big.bin", []byte(strings.Repeat("model bytes ", 4096)))
	f.half[model.URL] = true
	r := serve(t, Options{Dir: dir, StartupSlots: startupSlots(slot), store: storeOptions{fetch: f.fetch, settleWait: -1}})
	l := dialRaw(t, dir)
	if _, err := l.ask(MsgHandshake, l.signed(newKey(t), slot, false)); err != nil {
		t.Fatal(err)
	}
	if res, err := l.need(NeedParams{Plugin: "org.example.p", Works: []pluginhost.StoreWork{viewWork(model)}}); err != nil || res.State != NeedPending {
		t.Fatalf("need: %+v %v", res, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.count(model.URL) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	r.stop()
	if err := r.waitEnd(t, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if st := mustLoad(t, dir); len(st.Pending) == 0 {
		t.Fatal("rig: nothing was pending when the service stopped")
	}
	store := filepath.Join(dir, "store")
	partial := false
	for _, p := range tree(t, store) {
		partial = partial || strings.Contains(p, plugstore.PartialSuffix)
	}
	if !partial {
		t.Fatalf("rig: no partial download in the store: %v", tree(t, store))
	}
	rem, err := Remove(dir)
	if err != nil || !rem.Done() || !gone(t, dir) || !gone(t, store) {
		t.Fatalf("removal after a stop mid-fetch: %+v %v (left %v)", rem, err, tree(t, store))
	}
}

func TestTheRelaunchBudgetMakesNoServiceDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "machine")
	b := newBudget(dir)
	v, err := b.take()
	if err == nil || v.start || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("a budget with no service directory: %+v %v", v, err)
	}
	if !gone(t, dir) {
		t.Fatal("the budget made the service directory")
	}
	if err := b.clear(); err == nil || !gone(t, dir) {
		t.Fatalf("clearing a budget with no service directory: %v", err)
	}
}

// A SERVICE RUNS WHILE IT HOLDS ITS LOCK, and asking makes nothing: what
// uninstall's step 2 waits on before the store is touched.
func TestServiceRunsIsItsLockHeld(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	if runs, err := ServiceRuns(dir); err != nil || runs || !gone(t, dir) {
		t.Fatalf("no directory: %v %v", runs, err)
	}
	r := serve(t, Options{Dir: dir})
	if runs, err := ServiceRuns(dir); err != nil || !runs {
		t.Fatalf("a running service: %v %v", runs, err)
	}
	r.stop()
	r.waitEnd(t, 10*time.Second)
	if runs, err := ServiceRuns(dir); err != nil || runs {
		t.Fatalf("a stopped service: %v %v", runs, err)
	}
}
