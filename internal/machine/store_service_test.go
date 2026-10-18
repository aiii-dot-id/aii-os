//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func TestTwoIdentitiesNeedingOneEntryFetchItOnce(t *testing.T) {
	root := t.TempDir()
	a, b := slotIn(t, root, "identity-a"), slotIn(t, root, "identity-b")
	f := newFetches()
	gate := make(chan struct{})
	f.before = func(ctx context.Context, url string, offset int64) error {
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return nil
	}
	r := newStoreRig(t, "", f, Options{})
	m := f.model("tts.bin", bytes.Repeat([]byte("voice"), 300))
	work := viewWork(m)
	la, lb := r.link(newKey(t), a), r.link(newKey(t), b)
	need := NeedParams{Plugin: "org.example.voice", Works: []pluginhost.StoreWork{work, archiveRef(m.SHA256)}}
	first, err := la.need(need)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != NeedPending || first.Store != r.storeDir() {
		t.Fatalf("the first need of an absent entry: want pending in %s, got %+v", r.storeDir(), first)
	}
	second, err := lb.need(need)
	if err != nil || second.State != NeedPending {
		t.Fatalf("the second need while the first's job runs: want pending, got %+v, %v", second, err)
	}
	close(gate)
	la.needUntil(need, NeedPresent, 10*time.Second)
	lb.needUntil(need, NeedPresent, 10*time.Second)
	if n := f.count(m.URL); n != 1 {
		t.Fatalf("two identities needed one entry: %d fetches, want exactly 1", n)
	}
	st := r.diskState()
	for _, slot := range []string{a, b} {
		for _, e := range []string{work.Entry, "a/" + m.SHA256} {
			if !slices.Contains(st.Refs, Ref{Slot: slot, Plugin: "org.example.voice", Entry: e}) {
				t.Errorf("state.json does not refer %s to %s: %+v", slot, e, st.Refs)
			}
		}
	}
	raw, err := la.ask(MsgStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	var s Status
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Store != r.storeDir() || len(s.Refs) != 2 {
		t.Fatalf("a link's status names the store and its own two references: %+v", s)
	}
}

func TestPendingIsOnDiskBeforeTheFirstByte(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	var r *storeRig
	seen := make(chan []Pending, 1)
	f.before = func(ctx context.Context, url string, offset int64) error {
		st, err := loadState(filepath.Join(r.dir, stateFileName))
		if err != nil {
			return err
		}
		select {
		case seen <- st.Pending:
		default:
		}
		return nil
	}
	r = newStoreRig(t, "", f, Options{})
	m := f.model("stt.bin", []byte("listen carefully"))
	work := viewWork(m)
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.ears", Works: []pluginhost.StoreWork{work}}
	l.needUntil(need, NeedPresent, 10*time.Second)
	var pending []Pending
	select {
	case pending = <-seen:
	default:
		t.Fatal("the fetcher was never asked")
	}
	if len(pending) != 1 || pending[0].Entry != work.Entry || pending[0].Work.View == nil {
		t.Fatalf("state.json at the first byte: pending %+v, want %s with its work", pending, work.Entry)
	}
	waitFor(t, "the published entry's pending pin cleared", func() bool { return len(r.diskState().Pending) == 0 })
}

// A JOB THAT ENDS CLEARS ITS PENDING, and stays ended: a need repeated by
// a sweep is answered with the end and its remedy and starts nothing; the
// operator's Try again (retry) makes it pending again and tries once more.
func TestAJobThatEndsClearsItsPendingUntilTryAgain(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	m := f.model("gone.bin", []byte("not there"))
	f.mu.Lock()
	delete(f.bodies, m.URL) // the server answers 404
	f.mu.Unlock()
	work := viewWork(m)
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.gone", Works: []pluginhost.StoreWork{work}}
	ended := l.needUntil(need, NeedEnded, 10*time.Second)
	if ended.Remedy == "" || ended.Cause == "" {
		t.Fatalf("an ended download names its cause and remedy: %+v", ended)
	}
	if st := r.diskState(); len(st.Pending) != 0 {
		t.Fatalf("an ended job is still pending on disk: %+v", st.Pending)
	}
	calls := f.count(m.URL)
	again, err := l.need(need)
	if err != nil || again.State != NeedEnded {
		t.Fatalf("a need repeated without Try again: want ended, got %+v, %v", again, err)
	}
	if st := r.diskState(); len(st.Pending) != 0 {
		t.Fatalf("a repeated need made an ended entry pending again: %+v", st.Pending)
	}
	time.Sleep(50 * time.Millisecond)
	if f.count(m.URL) != calls {
		t.Fatalf("a need repeated without Try again fetched again")
	}
	f.mu.Lock()
	f.bodies[m.URL] = []byte("not there")
	f.mu.Unlock()
	need.Retry = true
	if res, err := l.need(need); err != nil || (res.State != NeedPending && res.State != NeedPresent) {
		t.Fatalf("Try again: want pending, got %+v, %v", res, err)
	}
	need.Retry = false
	l.needUntil(need, NeedPresent, 10*time.Second)
}

// A KILLED SERVICE RESUMES FROM ITS PARTIAL. The download stops halfway
// when the service does; the next service, with no identity asking,
// resumes the pending entry from the bytes already on disk.
func TestAStoppedServiceResumesItsPendingEntryFromThePartial(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	m := f.model("big.bin", bytes.Repeat([]byte("0123456789"), 1000))
	f.half[m.URL] = true
	r := newStoreRig(t, "", f, Options{})
	dir := r.dir
	l := r.link(newKey(t), slot)
	work := viewWork(m)
	if res, err := l.need(NeedParams{Plugin: "org.example.big", Works: []pluginhost.StoreWork{work}}); err != nil || res.State != NeedPending {
		t.Fatalf("need: %+v, %v", res, err)
	}
	partial := filepath.Join(r.storeDir(), plugstore.DirBlobs, m.SHA256+plugstore.PartialSuffix)
	waitFor(t, "half the download on disk", func() bool {
		fi, err := os.Stat(partial)
		return err == nil && fi.Size() == m.Size/2
	})
	r.close()
	if st := r.diskState(); len(st.Pending) != 1 || st.Pending[0].Entry != work.Entry {
		t.Fatalf("a stopped service's pending: %+v", st.Pending)
	}
	f.mu.Lock()
	f.half[m.URL] = false
	f.mu.Unlock()
	r2 := newStoreRig(t, dir, f, Options{})
	waitFor(t, "the pending entry published", func() bool { return r2.published(work.Entry) })
	f.mu.Lock()
	offsets := f.offsets[m.URL]
	f.mu.Unlock()
	if len(offsets) != 2 || offsets[1] != m.Size/2 {
		t.Fatalf("the restarted service fetched from %v, want a second call from %d", offsets, m.Size/2)
	}
	// Its record is written before its job says it is ready, and the
	// pending entry is cleared on that word: a moment after.
	waitFor(t, "the resumed entry's pending pin cleared", func() bool { return len(r2.diskState().Pending) == 0 })
}

func TestAnIdentitysCopiesAreAdoptedOnlyFromUnderItsOwnSlot(t *testing.T) {
	root := t.TempDir()
	slot := slotIn(t, root, "identity-a")
	other := slotIn(t, root, "identity-b")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	m := f.model("own.bin", []byte("my very own model bytes"))
	n := f.model("theirs.bin", []byte("another identity's model bytes"))
	mine := filepath.Join(slot, "data", "plugins-models", ".by-id", "org.example.own")
	theirs := filepath.Join(other, "data", "plugins-models", ".by-id", "org.example.own")
	for dir, d := range map[string]pluginhost.ModelDecl{mine: m, theirs: n} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := []byte("my very own model bytes")
		if d.Name == n.Name {
			body = []byte("another identity's model bytes")
		}
		if err := os.WriteFile(filepath.Join(dir, d.Name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.own", Works: []pluginhost.StoreWork{viewWork(m, n)}, Adopt: pluginhost.Adoption{ModelDirs: []string{mine, theirs}}}
	l.needUntil(need, NeedPresent, 10*time.Second)
	if c := f.count(m.URL); c != 0 {
		t.Errorf("the identity's own copy under its slot was fetched (%d), not adopted", c)
	}
	if c := f.count(n.URL); c != 1 {
		t.Errorf("a copy under another slot: %d fetches, want 1 (passed over)", c)
	}
}

// A NEED THAT NAMES A STORE TO ADOPT FROM IS REFUSED: which stores the
// writer adopts from is the service's own (a store it moved away from).
func TestANeedNamesNoStoreToAdoptFrom(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	m := f.model("x.bin", []byte("x"))
	l := r.link(newKey(t), slot)
	_, err := l.need(NeedParams{Plugin: "org.example.x", Works: []pluginhost.StoreWork{viewWork(m)}, Adopt: pluginhost.Adoption{Stores: []string{filepath.Join(slot, "store")}}})
	if !isRefusal(err, ReasonBadRequest) {
		t.Fatalf("a need naming a store: want %s, got %v", ReasonBadRequest, err)
	}
	if st := r.diskState(); len(st.Refs) != 0 {
		t.Fatalf("a refused need left references: %+v", st.Refs)
	}
}

// A WORK THAT DOES NOT MAKE ITS KEY IS REFUSED, typed, and nothing is
// referred to or fetched.
func TestAWorkWhoseContentsDoNotMakeItsKeyIsRefused(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	m := f.model("k.bin", []byte("k"))
	w := viewWork(m)
	w.Entry = "v/0123456789abcdef"
	l := r.link(newKey(t), slot)
	res, err := l.need(NeedParams{Plugin: "org.example.k", Works: []pluginhost.StoreWork{w}})
	if err != nil || res.State != NeedRefused || res.Cause == "" {
		t.Fatalf("a work that does not make its key: want refused with its cause, got %+v, %v", res, err)
	}
	if st := r.diskState(); len(st.Refs) != 0 || f.total() != 0 {
		t.Fatalf("a refused work left references (%+v) or fetched (%d)", st.Refs, f.total())
	}
}

// RELEASE LETS GO OF THE LINK'S OWN REFERENCES ONLY.
func TestReleaseLetsGoOfTheLinksOwnReferencesOnly(t *testing.T) {
	root := t.TempDir()
	a, b := slotIn(t, root, "identity-a"), slotIn(t, root, "identity-b")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	m := f.model("shared.bin", []byte("shared"))
	work := viewWork(m)
	la, lb := r.link(newKey(t), a), r.link(newKey(t), b)
	need := NeedParams{Plugin: "org.example.shared", Works: []pluginhost.StoreWork{work}}
	la.needUntil(need, NeedPresent, 10*time.Second)
	lb.needUntil(need, NeedPresent, 10*time.Second)
	if _, err := la.ask(MsgRelease, ReleaseParams{Refs: []LinkRef{{Plugin: "org.example.shared", Entry: work.Entry}}}); err != nil {
		t.Fatal(err)
	}
	st := r.diskState()
	if slices.Contains(st.Refs, Ref{Slot: a, Plugin: "org.example.shared", Entry: work.Entry}) {
		t.Errorf("a released reference is still recorded")
	}
	if !slices.Contains(st.Refs, Ref{Slot: b, Plugin: "org.example.shared", Entry: work.Entry}) {
		t.Errorf("one identity's release took another's reference: %+v", st.Refs)
	}
}

func TestAPlacedStoreAdoptsFromTheOldOneAndDrainsIt(t *testing.T) {
	root := t.TempDir()
	slot := slotIn(t, root, "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	old := r.storeDir()
	kept := viewWork(f.model("kept.bin", []byte("pinned while it moves")))
	moved := viewWork(f.model("moved.bin", []byte("moved by digest")))
	l := r.link(newKey(t), slot)
	for _, w := range []pluginhost.StoreWork{kept, moved} {
		l.needUntil(NeedParams{Plugin: "org.example.move", Works: []pluginhost.StoreWork{w}}, NeedPresent, 10*time.Second)
	}
	fetched := f.total()
	pin, err := plugstore.Store{Dir: old}.PinView(kept.Entry[2:])
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()

	if _, err := l.ask(MsgStorePlace, PlaceParams{Dir: filepath.Join(old, "inner")}); !isRefusal(err, ReasonStorePlace) {
		t.Fatalf("a place inside the store: want %s, got %v", ReasonStorePlace, err)
	}
	next := filepath.Join(root, "elsewhere", "plugins")
	raw, err := l.ask(MsgStorePlace, PlaceParams{Dir: next})
	if err != nil {
		t.Fatalf("store.place: %v", err)
	}
	var placed PlaceResult
	if err := json.Unmarshal(raw, &placed); err != nil || placed.Store != next || len(placed.Draining) != 1 || placed.Draining[0] != old {
		t.Fatalf("store.place answered %+v, %v", placed, err)
	}
	if got, err := StoreDir(r.dir); err != nil || got != next {
		t.Fatalf("an identity that is not linked reads the store at %s, %v; want %s", got, err, next)
	}
	for _, w := range []pluginhost.StoreWork{kept, moved} {
		res := l.needUntil(NeedParams{Plugin: "org.example.move", Works: []pluginhost.StoreWork{w}}, NeedPresent, 10*time.Second)
		if res.Store != next {
			t.Fatalf("a need after the move answers the store %s", res.Store)
		}
	}
	if f.total() != fetched {
		t.Fatalf("the move fetched %d files; every byte was in the old store", f.total()-fetched)
	}
	r.clock.advance(61 * time.Minute)
	r.collect()
	r.clock.advance(61 * time.Minute)
	r.collect()
	if _, err := os.Stat(filepath.Join(old, kept.Entry)); err != nil {
		t.Fatalf("the drain took a view an activation pins: %v", err)
	}
	if _, err := os.Stat(filepath.Join(old, moved.Entry)); err == nil {
		t.Fatal("the drain kept a view nothing runs from, which the new store holds")
	}
	pin.Close()
	for range 3 {
		r.clock.advance(61 * time.Minute)
		r.collect()
	}
	if st := r.diskState(); len(st.Draining) != 0 || st.Store != next {
		t.Fatalf("a drained store is still recorded: store %s, draining %v", st.Store, st.Draining)
	}
}

func TestADrainedStoreWhoseRetirementWasNotRecordedIsRetiredByTheNextPass(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a directory mode does not stop this writer here (Windows ACLs, or root)")
	}
	root := t.TempDir()
	r := newStoreRig(t, "", newFetches(), Options{})
	old := r.storeDir()
	l := r.link(newKey(t), slotIn(t, root, "identity-a"))
	if _, err := l.ask(MsgStorePlace, PlaceParams{Dir: filepath.Join(root, "elsewhere", "plugins")}); err != nil {
		t.Fatalf("store.place: %v", err)
	}
	if st := r.diskState(); len(st.Draining) != 1 || st.Draining[0] != old {
		t.Fatalf("the store moved away from is not recorded as draining: %v", st.Draining)
	}
	if err := os.Chmod(r.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(r.dir, 0o700) })
	r.collect()
	if st := r.diskState(); len(st.Draining) != 1 {
		t.Fatalf("a retirement that was not written is recorded: draining %v", st.Draining)
	}
	if err := os.Chmod(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r.collect()
	if st := r.diskState(); len(st.Draining) != 0 {
		t.Fatalf("the next pass did not retire the drained store: draining %v", st.Draining)
	}
}

func TestAStoreMovedWhileAFetchRunsBringsTheEntryToItsNewPlace(t *testing.T) {
	root := t.TempDir()
	slot := slotIn(t, root, "identity-a")
	f := newFetches()
	gate, entered := make(chan struct{}), make(chan struct{}, 1)
	f.before = func(ctx context.Context, url string, offset int64) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return nil
	}
	r := newStoreRig(t, "", f, Options{})
	body := []byte("brought while the store moved")
	m := f.model("moving.bin", body)
	work := viewWork(m)
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.move", Works: []pluginhost.StoreWork{work}}
	if res, err := l.need(need); err != nil || res.State != NeedPending {
		t.Fatalf("the first need of an absent entry: want pending, got %+v, %v", res, err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the fetch never began")
	}
	next := filepath.Join(root, "elsewhere", "plugins")
	if _, err := l.ask(MsgStorePlace, PlaceParams{Dir: next}); err != nil {
		t.Fatalf("store.place: %v", err)
	}
	close(gate)
	file := filepath.Join(next, work.Entry, filepath.FromSlash(m.Dest()))
	waitFor(t, "the entry verified in the new store, with nobody asking again", func() bool {
		raw, err := os.ReadFile(file)
		return err == nil && bytes.Equal(raw, body)
	})
	res := l.needUntil(need, NeedPresent, 10*time.Second)
	if res.Store != next {
		t.Fatalf("the need answered present in %s; the store is %s", res.Store, next)
	}
	waitFor(t, "the published entry's pending pin cleared", func() bool { return len(r.diskState().Pending) == 0 })
}

func TestAReadyJobWhoseEntryIsNotInTheStoreIsNotAnsweredPresent(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	m := f.model("gone.bin", []byte("published, then not there"))
	work := viewWork(m)
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.ready", Works: []pluginhost.StoreWork{work}}
	l.needUntil(need, NeedPresent, 10*time.Second)
	ss := r.srv.store
	if st, ok := ss.acq.Status(work.Entry); !ok || st.Phase != pluginhost.PhaseReady {
		t.Fatalf("rig: the entry's job is not kept ready after publication: %+v, %v", st, ok)
	}
	ss.mu.Lock()
	ss.works[work.Entry] = work
	ss.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(r.storeDir(), work.Entry)); err != nil {
		t.Fatal(err)
	}
	if res, err := l.need(need); err != nil || res.State == NeedPresent {
		t.Fatalf("a need answered %+v, %v while the entry is not in the store", res, err)
	}
	l.needUntil(need, NeedPresent, 10*time.Second)
	if _, err := os.Stat(filepath.Join(r.storeDir(), work.Entry, filepath.FromSlash(m.Dest()))); err != nil {
		t.Fatalf("answered present, and the entry is not in the store: %v", err)
	}
}

// A STORE THAT CANNOT BE WRITTEN REFUSES EVERY NEED WITH ITS REMEDY, and
// the operator's new place mends it without a restart.
func TestAStoreThatCannotBeWrittenRefusesNeedsUntilPlacedElsewhere(t *testing.T) {
	root := t.TempDir()
	slot := slotIn(t, root, "identity-a")
	dir := filepath.Join(t.TempDir(), "machine")
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	notADir := filepath.Join(root, "a-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveState(filepath.Join(dir, stateFileName), State{Store: notADir}); err != nil {
		t.Fatal(err)
	}
	f := newFetches()
	r := newStoreRig(t, dir, f, Options{})
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.x", Works: []pluginhost.StoreWork{viewWork(f.model("x.bin", []byte("x")))}}
	_, err := l.need(need)
	var ref *Refusal
	if !isRefusal(err, ReasonNoStore) || !errors.As(err, &ref) || !strings.Contains(ref.Remedy, "aii machine store") {
		t.Fatalf("a need of a store that cannot be written: want %s naming its remedy, got %v", ReasonNoStore, err)
	}
	if _, err := l.ask(MsgStorePlace, PlaceParams{Dir: filepath.Join(root, "plugins")}); err != nil {
		t.Fatal(err)
	}
	l.needUntil(need, NeedPresent, 10*time.Second)
}

func TestTheWriterWaitsForRoomOnTheStoresVolume(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{store: storeOptions{freeDisk: func(string) (int64, error) { return 1 << 30, nil }}})
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.room", Works: []pluginhost.StoreWork{viewWork(f.model("room.bin", []byte("needs room")))}}
	res := l.needUntil(need, NeedWaiting, 10*time.Second)
	if !strings.Contains(res.Cause, "free") || res.AfterMS < 1000 {
		t.Fatalf("a need short of room: %+v", res)
	}
	if f.total() != 0 {
		t.Fatalf("a need short of room fetched")
	}
}

// DAMAGE REPORTED IN A NEED IS REPAIRED ONCE (H1): the identity that found
// a blob's bytes wrong names its view; the writer verifies again under the
// view's record held exclusively, fetches the model into a new file, and
// relinks; the next verification passes.
func TestDamageReportedInANeedIsRepaired(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	m := f.model("fragile.bin", []byte("bytes that will be flipped"))
	work := viewWork(m)
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.fragile", Works: []pluginhost.StoreWork{work}}
	l.needUntil(need, NeedPresent, 10*time.Second)
	blob := filepath.Join(r.storeDir(), blobEntry(m))
	if err := os.Chmod(blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("bytes that will be FLIPPED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _ := l.need(need); res.State != NeedPresent {
		t.Fatalf("damage nobody reported is not looked for: %+v", res)
	}
	need.Damaged = []string{work.Entry}
	if res, err := l.need(need); err != nil || res.State != NeedPending {
		t.Fatalf("a need reporting damage: want pending, got %+v, %v", res, err)
	}
	need.Damaged = nil
	l.needUntil(need, NeedPresent, 10*time.Second)
	got, err := os.ReadFile(filepath.Join(r.storeDir(), work.Entry, m.Name))
	if err != nil || string(got) != "bytes that will be flipped" {
		t.Fatalf("the view's file after the repair: %q, %v", got, err)
	}
	if n := f.count(m.URL); n != 2 {
		t.Fatalf("the repair fetched %d times in all, want 2 (the first and the repair)", n)
	}
}

// A REPAIR NEVER DISTURBS ANOTHER IDENTITY'S RUNNING ACTIVATION (H1): while
// an activation pins the damaged view, the need waits, saying so, and the
// view's file is left as it is.
func TestARepairWaitsWhileAnotherActivationPinsTheEntry(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	m := f.model("shared.bin", []byte("bytes another identity runs on"))
	work := viewWork(m)
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.shared", Works: []pluginhost.StoreWork{work}}
	l.needUntil(need, NeedPresent, 10*time.Second)
	view := filepath.Join(r.storeDir(), work.Entry, m.Name)
	before, err := os.Stat(view)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := plugstore.Store{Dir: r.storeDir()}.PinView(work.Entry[2:])
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()
	blob := filepath.Join(r.storeDir(), blobEntry(m))
	if err := os.Chmod(blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("bytes another identity runs ON"), 0o600); err != nil {
		t.Fatal(err)
	}
	need.Damaged = []string{work.Entry}
	if _, err := l.need(need); err != nil {
		t.Fatal(err)
	}
	need.Damaged = nil
	res := l.needUntil(need, NeedWaiting, 10*time.Second)
	if !strings.Contains(res.Cause, "an activation runs from") {
		t.Fatalf("a repair an activation holds off: %+v", res)
	}
	after, err := os.Stat(view)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("the pinned view's file was replaced while an activation ran from it (%v)", err)
	}
	if n := f.count(m.URL); n != 1 {
		t.Fatalf("a repair an activation holds off fetched (%d in all)", n)
	}
}

func TestANeedThatCouldNotBeRecordedIsBroughtWhenAskedAgain(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	m := f.model("once.bin", []byte("brought on the second ask"))
	work := viewWork(m)
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.once", Works: []pluginhost.StoreWork{work}}
	path := filepath.Join(r.dir, stateFileName)
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.need(need); !isRefusal(err, ReasonNotRecorded) {
		t.Fatalf("a need whose state write fails: want %s, got %v", ReasonNotRecorded, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	l.needUntil(need, NeedPresent, 10*time.Second)
	if n := f.count(m.URL); n != 1 {
		t.Fatalf("the need asked again fetched %d times, want once", n)
	}
	if st := r.diskState(); !slices.Contains(st.Refs, Ref{Slot: slot, Plugin: "org.example.once", Entry: work.Entry}) {
		t.Fatalf("the need asked again is not recorded: %+v", st.Refs)
	}
}

func TestAnEndedJobReservesNoRoomOnTheStoresVolume(t *testing.T) {
	const margin = 2 << 30
	const large = 800 << 20
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	gone := pluginhost.ModelDecl{Name: "large.bin", URL: "https://models.example/large.bin", SHA256: strings.Repeat("4", 64), Size: large}
	answer := make(chan struct{})
	f.before = func(ctx context.Context, url string, offset int64) error {
		if url != gone.URL {
			return nil
		}
		select {
		case <-answer: // and then the server answers 404
		case <-ctx.Done():
		}
		return nil
	}
	r := newStoreRig(t, "", f, Options{store: storeOptions{freeDisk: func(string) (int64, error) { return margin + large, nil }}})
	ended, deliverEnd, delivered := holdFirstStoreEnd(r)
	l := r.link(newKey(t), slot)
	failed := NeedParams{Plugin: "org.example.large", Works: []pluginhost.StoreWork{viewWork(gone)}}
	if res, err := l.need(failed); err != nil || res.State != NeedPending {
		t.Fatalf("the large need: %+v, %v", res, err)
	}
	waitFor(t, "the large download to begin", func() bool { return f.count(gone.URL) == 1 })
	small := NeedParams{Plugin: "org.example.small", Works: []pluginhost.StoreWork{viewWork(f.model("small.bin", []byte("fits beside the margin")))}}
	if res := l.needUntil(small, NeedWaiting, 10*time.Second); !strings.Contains(res.Cause, "the downloads ahead of it 800.0 MB") {
		t.Fatalf("a download being brought holds its room: %+v", res)
	}
	close(answer)
	awaitStoreStep(t, "the terminal status before its machine callback", ended)
	if res, err := l.need(failed); err != nil || res.State != NeedPending {
		t.Errorf("an end not yet accounted by the machine must stay pending: %+v, %v", res, err)
	}
	deliverEnd()
	awaitStoreStep(t, "the machine's terminal bookkeeping", delivered)
	l.needUntil(failed, NeedEnded, 10*time.Second)
	// The small one's wait is a minute; Try again wakes it out of it.
	small.Retry = true
	if _, err := l.need(small); err != nil {
		t.Fatal(err)
	}
	small.Retry = false
	l.needUntil(small, NeedPresent, 10*time.Second)
	if res, err := l.need(failed); err != nil || res.State != NeedEnded {
		t.Fatalf("the ended download, asked again without Try again: want ended, got %+v, %v", res, err)
	}
	failed.Retry = true
	if _, err := l.need(failed); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Try again to ask for the ended download again", func() bool { return f.count(gone.URL) == 2 })
}

// A retry can arrive after terminal acquisition status but before its callback.
// That old callback must not clear the retry's pending record or park its new
// attempt. Hold both the callback and the retry's fetch at those exact steps.
func TestARetryBeforeTheEndCallbackKeepsItsPendingRecord(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	m := f.model("retry.bin", []byte("the retry succeeds"))
	retrying, finish := make(chan struct{}), make(chan struct{})
	f.before = func(ctx context.Context, url string, offset int64) error {
		if f.count(url) == 1 {
			return &pluginhost.FetchStatusError{Status: 404}
		}
		close(retrying)
		select {
		case <-finish:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}
	r := newStoreRig(t, "", f, Options{})
	ended, deliverEnd, delivered := holdFirstStoreEnd(r)
	l := r.link(newKey(t), slot)
	work := viewWork(m)
	need := NeedParams{Plugin: "org.example.retry", Works: []pluginhost.StoreWork{work}}
	if _, err := l.need(need); err != nil {
		t.Fatal(err)
	}
	awaitStoreStep(t, "the terminal status before its machine callback", ended)
	need.Retry = true
	if res, err := l.need(need); err != nil || res.State != NeedPending {
		t.Fatalf("Try again before the old callback: %+v, %v", res, err)
	}
	need.Retry = false
	deliverEnd()
	awaitStoreStep(t, "the old end callback", delivered)
	awaitStoreStep(t, "the retried fetch", retrying)
	if st := r.diskState(); len(st.Pending) != 1 || st.Pending[0].Entry != work.Entry {
		t.Errorf("the old end callback removed the retry's pending record: %+v", st.Pending)
	}
	if res, err := l.need(need); err != nil || res.State != NeedPending {
		t.Errorf("the old end callback parked the retry: %+v, %v", res, err)
	}
	close(finish)
	l.needUntil(need, NeedPresent, 10*time.Second)
}
