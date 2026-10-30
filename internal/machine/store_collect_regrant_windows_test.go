//go:build windows

package machine

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func TestAViewWindowsWouldNotMoveKeepsItsCapability(t *testing.T) {
	f := newFetches()
	r := newStoreRig(t, "", f, Options{})
	model := f.model("held.bin", []byte("a view whose file is open"))
	held := viewWork(model)
	r.ensure(held)
	blob := filepath.Join(r.storeDir(), blobEntry(model))
	sid, err := plugstore.CapabilitySID(plugstore.Entry{Kind: plugstore.DirViews, ID: held.Entry[2:]})
	if err != nil {
		t.Fatal(err)
	}
	granted := func() bool {
		caps, err := plugstore.Capabilities(blob)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(caps, func(c *windows.SID) bool { return c.String() == sid.String() })
	}
	if !granted() {
		t.Fatal("rig: the view's blob does not carry the view's capability")
	}
	inView, err := os.Open(filepath.Join(r.storeDir(), held.Entry, "held.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer inView.Close()
	r.collect()
	r.clock.advance(61 * time.Minute)
	r.collect()
	if !r.exists(held.Entry) || !r.published(held.Entry) {
		t.Fatal("rig: the view whose file is open was not left whole with its record")
	}
	if !granted() {
		t.Fatal("a view Windows would not move was left recorded and present with its capability gone from its blob")
	}
	inView.Close()
	r.collect()
	if r.exists(held.Entry) {
		t.Fatal("the view let go was kept at the next pass")
	}
}
