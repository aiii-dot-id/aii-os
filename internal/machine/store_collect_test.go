//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// archiveIn writes an archive's file into the store by its digest.
func (r *storeRig) archiveIn(body []byte) string {
	r.t.Helper()
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	path, err := plugstore.Store{Dir: r.storeDir()}.Archive(sha)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o400); err != nil {
		r.t.Fatal(err)
	}
	return sha
}

// refer records refs as a need would, without one.
func (r *storeRig) refer(refs ...Ref) {
	r.t.Helper()
	if err := r.srv.updateState(func(st *State) error {
		st.Refs = append(st.Refs, refs...)
		return nil
	}); err != nil {
		r.t.Fatal(err)
	}
}

// collect runs one pass and fails the test on a refusal.
func (r *storeRig) collect() {
	r.t.Helper()
	if err := r.srv.store.collect(); err != nil {
		r.t.Fatalf("a collection pass: %v", err)
	}
}

func TestTheCollectorKeepsWhatIsReferencedPendingOrPinned(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	referenced := viewWork(f.model("kept.bin", []byte("referenced")))
	pinned := viewWork(f.model("pinned.bin", []byte("pinned by an activation")))
	loose := viewWork(f.model("loose.bin", []byte("nothing holds this one")))
	pendingModel := f.model("coming.bin", []byte("being brought"))
	pending := viewWork(pendingModel)
	for _, w := range []pluginhost.StoreWork{referenced, pinned, loose, pending} {
		r.ensure(w)
	}
	keptArchive := r.archiveIn([]byte("an archive a slot keeps"))
	looseArchive := r.archiveIn([]byte("an archive nothing keeps"))
	orphan := f.model("orphan.bin", []byte("a blob no view links"))
	r.ensure(viewWork(orphan))
	if err := os.RemoveAll(filepath.Join(r.storeDir(), viewWork(orphan).Entry)); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(r.storeDir(), viewWork(orphan).Entry+plugstore.RecordSuffix))
	r.refer(Ref{Slot: slot, Plugin: "org.example.a", Entry: referenced.Entry}, Ref{Slot: slot, Plugin: "org.example.a", Entry: "a/" + keptArchive})
	if err := r.srv.updateState(func(st *State) error {
		st.Pending = append(st.Pending, Pending{Entry: pending.Entry, Since: r.clock.Now().Format(time.RFC3339), Work: pending})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pin, err := plugstore.Store{Dir: r.storeDir()}.PinView(strings.TrimPrefix(pinned.Entry, "v/"))
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()

	live := []string{referenced.Entry, pinned.Entry, pending.Entry, "a/" + keptArchive, blobEntry(pendingModel)}
	gone := []string{loose.Entry, "a/" + looseArchive, blobEntry(orphan)}
	r.collect()
	r.clock.advance(59 * time.Minute)
	r.collect()
	for _, e := range append(live, gone...) {
		if !r.exists(e) {
			t.Fatalf("%s was taken before an hour had passed", e)
		}
	}
	if st := r.diskState(); st.Unreferenced[filepath.Join(r.storeDir(), loose.Entry)] == "" {
		t.Fatalf("the first pass did not note when it found %s unreferenced: %+v", loose.Entry, st.Unreferenced)
	}
	r.clock.advance(2 * time.Minute)
	r.collect()
	for _, e := range live {
		if !r.exists(e) {
			t.Errorf("the collector took %s, which is held", e)
		}
	}
	for _, e := range gone {
		if r.exists(e) {
			t.Errorf("the collector kept %s an hour after it was found unreferenced", e)
		}
	}
	if left, _ := os.ReadDir(filepath.Join(r.storeDir(), plugstore.DirTrash)); len(left) != 0 {
		t.Errorf("the pass left its trash behind: %d entries", len(left))
	}
	// Once the activation lets go, the pinned view goes at the next pass
	// (its hour has passed), and its blob an hour after no view links it.
	pin.Close()
	r.collect()
	if r.exists(pinned.Entry) {
		t.Errorf("a view nothing holds any more was kept")
	}
	pinnedBlob := blobEntry(pinned.View.Models[0])
	r.clock.advance(61 * time.Minute)
	r.collect()
	if r.exists(pinnedBlob) {
		t.Errorf("a blob no view links was kept an hour on")
	}
	if !r.exists(blobEntry(referenced.View.Models[0])) {
		t.Errorf("the referenced view's blob was taken")
	}
}

// ONE RECORD THAT DOES NOT READ CANCELS THE WHOLE PASS: the collector
// cannot say what an entry it cannot read is, so it takes nothing at all.
func TestOneRecordThatDoesNotReadCancelsThePass(t *testing.T) {
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	loose := viewWork(f.model("loose.bin", []byte("nothing holds this one")))
	r.ensure(loose)
	torn := filepath.Join(r.storeDir(), plugstore.DirRoots, strings.Repeat("ab", 8))
	if err := os.MkdirAll(torn, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(torn+plugstore.RecordSuffix, []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.srv.store.collect(); err == nil {
		t.Fatal("a pass over an unreadable record answered nil")
	}
	r.clock.advance(2 * time.Hour)
	if err := r.srv.store.collect(); err == nil || !strings.Contains(err.Error(), torn) {
		t.Fatalf("a pass over an unreadable record: want a refusal naming it, got %v", err)
	}
	if !r.exists(loose.Entry) || !r.exists(blobEntry(loose.View.Models[0])) {
		t.Fatal("a pass with an unreadable record took something")
	}
	if !isDirAt(torn) {
		t.Fatal("the unreadable record's entry was taken")
	}
}

// RELEASED, THEN THE GRACE, THEN THE TRASH: a release asks for a pass, and
// the pass notes the entry; it goes an hour later.
func TestAReleasedEntryGoesAnHourAfterTheRelease(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	work := viewWork(f.model("once.bin", []byte("needed once")))
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.once", Works: []pluginhost.StoreWork{work}}
	l.needUntil(need, NeedPresent, 10*time.Second)
	if _, err := l.ask(MsgRelease, ReleaseParams{Refs: []LinkRef{{Plugin: "org.example.once", Entry: work.Entry}}}); err != nil {
		t.Fatal(err)
	}
	r.collect()
	if !r.exists(work.Entry) {
		t.Fatal("a released entry was taken at once, with no grace")
	}
	r.clock.advance(61 * time.Minute)
	r.collect()
	if r.exists(work.Entry) {
		t.Fatal("a released entry was kept an hour after it was found unreferenced")
	}
}

// PARTIALS OF WHAT IS NOT BEING BROUGHT GO AFTER THE GRACE; a pending
// entry's partial stays.
func TestPartialsOfWhatIsNotPendingGoAfterTheGrace(t *testing.T) {
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	kept := f.model("resuming.bin", []byte("a download that resumes"))
	left := f.model("abandoned.bin", []byte("a download nothing resumes"))
	work := viewWork(kept)
	if err := r.srv.updateState(func(st *State) error {
		st.Pending = append(st.Pending, Pending{Entry: work.Entry, Since: r.clock.Now().Format(time.RFC3339), Work: work})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var partials []string
	for _, d := range []pluginhost.ModelDecl{kept, left} {
		blob, _ := plugstore.Store{Dir: r.storeDir()}.Blob(d.SHA256)
		p := blob + plugstore.PartialSuffix
		if err := os.WriteFile(p, []byte("half"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := r.clock.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		partials = append(partials, p)
	}
	r.collect()
	if _, err := os.Stat(partials[0]); err != nil {
		t.Errorf("a pending entry's partial was taken: %v", err)
	}
	if _, err := os.Stat(partials[1]); err == nil {
		t.Errorf("a partial nothing resumes was kept past its grace")
	}
}

// A SLOT THAT IS GONE TAKES ITS REFERENCES WITH IT — after two passes an
// hour apart that both found it gone while its parent is there (H2). A slot
// whose parent is missing (a volume not mounted) keeps them, however long.
func TestAGoneSlotsReferencesGoAfterTwoPassesAnHourApart(t *testing.T) {
	root := t.TempDir()
	stays, goes := slotIn(t, root, "identity-a"), slotIn(t, root, "identity-b")
	unmounted := filepath.Join(root, "work-volume", "identity-c")
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	work := viewWork(f.model("m.bin", []byte("m")))
	r.ensure(work)
	r.refer(Ref{Slot: stays, Plugin: "p", Entry: work.Entry}, Ref{Slot: goes, Plugin: "p", Entry: work.Entry}, Ref{Slot: unmounted, Plugin: "p", Entry: work.Entry})
	if err := os.RemoveAll(goes); err != nil {
		t.Fatal(err)
	}
	r.collect()
	r.clock.advance(30 * time.Minute)
	r.collect()
	if n := len(r.diskState().Refs); n != 3 {
		t.Fatalf("references went before two passes an hour apart found the slot gone: %d left", n)
	}
	r.clock.advance(31 * time.Minute)
	r.collect()
	st := r.diskState()
	for _, ref := range st.Refs {
		if ref.Slot == goes {
			t.Errorf("a slot gone an hour still holds %+v", ref)
		}
	}
	if len(st.Refs) != 2 {
		t.Fatalf("want the kept slot's and the unmounted slot's references, got %+v", st.Refs)
	}
	r.clock.advance(5 * time.Hour)
	r.collect()
	if len(r.diskState().Refs) != 2 {
		t.Fatalf("a slot under a missing parent lost its references")
	}
}

// THE COLLECTOR WAITS FOR THE ACCOUNT TO SETTLE: every recorded identity
// linked once since the start, or ten minutes — so an identity that has
// not linked yet does not find what it runs from collected.
func TestTheCollectorWaitsForEveryRecordedIdentityToLink(t *testing.T) {
	root := t.TempDir()
	a, b := slotIn(t, root, "identity-a"), slotIn(t, root, "identity-b")
	dir := filepath.Join(t.TempDir(), "machine")
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	ka, kb := newKey(t), newKey(t)
	pre := State{Identities: []Record{
		{Slot: a, Identity: ka.Fingerprint(), PublicKey: ka.PublicKeyB64(), How: RecordedByRegister, At: "2026-10-09T00:00:00Z"},
		{Slot: b, Identity: kb.Fingerprint(), PublicKey: kb.PublicKeyB64(), How: RecordedByRegister, At: "2026-10-09T00:00:00Z"},
	}}
	if err := saveState(filepath.Join(dir, stateFileName), pre); err != nil {
		t.Fatal(err)
	}
	passes := make(chan struct{}, 8)
	r := newStoreRig(t, dir, newFetches(), Options{store: storeOptions{settleWait: time.Hour, collected: func() { passes <- struct{}{} }}})
	r.link(ka, a)
	select {
	case <-passes:
		t.Fatal("a pass ran before every recorded identity had linked")
	case <-time.After(200 * time.Millisecond):
	}
	r.link(kb, b)
	select {
	case <-passes:
	case <-time.After(5 * time.Second):
		t.Fatal("no pass once every recorded identity had linked")
	}
}

// AND TEN MINUTES SETTLE IT when an identity never links.
func TestTheCollectorRunsOnceTheSettlingWaitHasPassed(t *testing.T) {
	passes := make(chan struct{}, 8)
	newStoreRig(t, "", newFetches(), Options{store: storeOptions{settleWait: 50 * time.Millisecond, collected: func() { passes <- struct{}{} }}})
	select {
	case <-passes:
	case <-time.After(5 * time.Second):
		t.Fatal("no pass after the settling wait")
	}
}

func isDirAt(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
