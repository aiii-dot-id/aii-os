package app

import (
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// THE UPDATES CARD NAMES THE NEWEST RELEASE THIS PROGRAM HAS VERIFIED (row
// 7), which no update goes below: its version, the day (UTC) GitHub gives
// for it and the day it was verified here, read at each view from the
// record beside the program; nothing when there is none.
func TestTheUpdatesCardNamesTheNewestReleaseVerified(t *testing.T) {
	h := newListsHost(t, map[string]int64{"platform_release": 1})
	h.app.updateChecker = updates.NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return h.plat.Env },
		func() string { return "0.2.0" },
		func() bool { return false }, func() string { return "" },
		nil, t.TempDir(), func() bool { return false }, func() bool { return false })
	was := highestVerified
	t.Cleanup(func() { highestVerified = was })

	highestVerified = func() (updates.HighestSeen, bool) {
		return updates.HighestSeen{Version: "0.2.1", PublishedAt: "2026-10-30T23:30:00-05:00",
			VerifiedAt: time.Date(2026, 10, 31, 1, 0, 0, 0, time.UTC)}, true
	}
	view := h.app.updateStateView()
	if view.HighestVersion != "0.2.1" || view.HighestPublished != "2026-10-31" || view.HighestVerified != "2026-10-31" {
		t.Fatalf("the card is told %q, published %q, verified %q", view.HighestVersion, view.HighestPublished, view.HighestVerified)
	}

	// GitHub gave no date: the version and the day it was verified only.
	highestVerified = func() (updates.HighestSeen, bool) {
		return updates.HighestSeen{Version: "0.2.1", VerifiedAt: time.Date(2026, 10, 31, 1, 0, 0, 0, time.UTC)}, true
	}
	if view := h.app.updateStateView(); view.HighestVersion != "0.2.1" || view.HighestPublished != "" || view.HighestVerified != "2026-10-31" {
		t.Fatalf("with no date from GitHub the card is told %+v", view)
	}

	highestVerified = func() (updates.HighestSeen, bool) { return updates.HighestSeen{}, false }
	if view := h.app.updateStateView(); view.HighestVersion != "" || view.HighestPublished != "" || view.HighestVerified != "" {
		t.Fatalf("a program that verified nothing tells the card %+v", view)
	}
}
