package updates

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAReleaseWaitsForAnAnnouncedListThatIsNotHere(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	srv, downloads := blockingRelease(t, ready)
	c := checkNowChecker(t, srv, true, func() bool { return false }, func() bool { return false })
	c.canStage = func() (bool, string) { return true, "" }

	var bring error
	calls := 0
	c.SetTrustLists(func(context.Context) (string, error) { calls++; return "", bring })

	behind := fmt.Errorf("the platform list could not be brought here: %w", ErrReleaseListBehind)
	bring = behind
	c.tick(t.Context())
	snap := c.State().Snapshot("0.3.0")
	if calls != 1 {
		t.Fatalf("the lists were brought %d times on one check, want once", calls)
	}
	if snap.AvailableVersion != "0.4.0" || snap.LastError != "" || atomic.LoadInt32(downloads) != 0 {
		t.Fatalf("an update was attempted past a missing release list: %+v, %d downloads", snap, atomic.LoadInt32(downloads))
	}
	if snap.Lists != behind.Error() {
		t.Fatalf("the card is not told why: Lists = %q", snap.Lists)
	}

	// Another root's list: said, and the release decision goes on (here it
	// fails for want of a signing root, which is what shows it ran).
	bring = errors.New("the reviewer's list could not be brought here")
	c.tick(t.Context())
	snap = c.State().Snapshot("0.3.0")
	if snap.LastError == "" || !strings.Contains(snap.Lists, "reviewer") {
		t.Fatalf("a plugin list's failure held the release decision, or was not said: %+v", snap)
	}

	bring = nil
	c.tick(t.Context())
	if snap = c.State().Snapshot("0.3.0"); snap.Lists != "" {
		t.Fatalf("a check that brought every list still says %q", snap.Lists)
	}
}
