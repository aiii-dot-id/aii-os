//go:build windows

package machine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func TestAnEntryHeldOpenIsSkippedAndTakenOnceLetGo(t *testing.T) {
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	held := viewWork(f.model("held.bin", []byte("a view whose file is open")))
	orphan := f.model("orphan.bin", []byte("a blob no view links, open"))
	loose := viewWork(f.model("loose.bin", []byte("nothing holds this one")))
	for _, w := range []pluginhost.StoreWork{held, viewWork(orphan), loose} {
		r.ensure(w)
	}
	if err := os.RemoveAll(filepath.Join(r.storeDir(), viewWork(orphan).Entry)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(r.storeDir(), viewWork(orphan).Entry+plugstore.RecordSuffix)); err != nil {
		t.Fatal(err)
	}
	inView, err := os.Open(filepath.Join(r.storeDir(), held.Entry, "held.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer inView.Close()
	blob, err := os.Open(filepath.Join(r.storeDir(), blobEntry(orphan)))
	if err != nil {
		t.Fatal(err)
	}
	defer blob.Close()
	r.collect()
	r.clock.advance(61 * time.Minute)
	r.collect()
	if r.exists(loose.Entry) {
		t.Fatal("an entry nothing holds was kept because another was open")
	}
	if !r.exists(held.Entry) || !r.published(held.Entry) {
		t.Fatal("a view whose file is open was not left whole with its record")
	}
	if !r.exists(blobEntry(orphan)) {
		t.Fatal("an open blob was taken")
	}
	inView.Close()
	blob.Close()
	r.collect()
	if r.exists(held.Entry) || r.exists(blobEntry(orphan)) {
		t.Fatal("entries let go were kept at the next pass")
	}
}

// A VIEW THE COLLECTOR TAKES TAKES ITS CAPABILITY WITH IT (Q3-b): the blob
// it shared with a view that stays is read by that view's children alone.
func TestATakenViewsCapabilityLeavesTheBlobItShared(t *testing.T) {
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	body := []byte("one model under two names")
	kept, gone := f.model("kept.bin", body), f.model("gone.bin", body)
	keptView, goneView := viewWork(kept), viewWork(gone)
	r.ensure(keptView)
	r.ensure(goneView)
	r.refer(Ref{Slot: t.TempDir(), Plugin: "org.example.kept", Entry: keptView.Entry})
	blob := filepath.Join(r.storeDir(), blobEntry(kept))
	caps := func() []string {
		got, err := plugstore.Capabilities(blob)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range got {
			out = append(out, c.String())
		}
		return out
	}
	if n := len(caps()); n != 2 {
		t.Fatalf("the shared blob carries %d capabilities, want the two views'", n)
	}
	r.collect()
	r.clock.advance(61 * time.Minute)
	r.collect()
	if r.exists(goneView.Entry) {
		t.Fatal("the unreferenced view was kept")
	}
	sid, err := plugstore.CapabilitySID(plugstore.Entry{Kind: plugstore.DirViews, ID: keptView.Entry[2:]})
	if err != nil {
		t.Fatal(err)
	}
	if got := caps(); len(got) != 1 || got[0] != sid.String() {
		t.Fatalf("the blob after its other view went: %v, want only %s", got, sid)
	}
}
