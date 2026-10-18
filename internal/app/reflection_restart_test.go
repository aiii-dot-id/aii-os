package app

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestBootKeepsTheReflectionDeadlinesAlreadyOwed(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "aii.db")
	current, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { current.Close() })
	// The life clock exists only after birth, as it does on a real boot.
	if err := current.Materialize(&ledger.Event{Seq: 1, Prev: "0", Timestamp: "2026-09-23T00:00:00Z",
		Type: ledger.EventRing0Genesis, Ring: 0, Payload: []byte(`{"name":"test"}`),
		Content: "birth fixture", Sig: "fixture"}); err != nil {
		t.Fatal(err)
	}
	cfg := *defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.LedgerPath = filepath.Join(data, "ledger.jsonl")
	cfg.Identity.DBPath = path
	cfg.Identity.KeyPath = filepath.Join(data, "identity.sec")
	cfg.Tools.CWD = dir
	a := New(&cfg)
	boot := func() {
		t.Helper()
		a.store = current
		a.timeFac = cognitive.NewTIME(current, current)
		for _, name := range []string{"rhythm", a.continuity.AlarmOwner().Name(), memoryOwnerName} {
			a.timeFac.RegisterOwner(&countingOwner{name: name})
		}
		if err := a.armFacilityAlarms(cfg); err != nil {
			t.Fatal(err)
		}
	}
	deadlines := func() (int64, int64) {
		t.Helper()
		rows, err := current.DueAlarms("life", math.MaxInt64, 10)
		if err != nil {
			t.Fatal(err)
		}
		var self, review int64
		for _, row := range rows {
			switch row.AlarmID {
			case "reflect:self_model":
				self = row.Deadline
			case "reflect:identity_review":
				review = row.Deadline
			}
		}
		return self, review
	}
	boot()
	if self, review := deadlines(); self != cognitive.SelfModelCadence || review != cognitive.IdentityReviewCadence {
		t.Fatalf("first boot armed self/review at %d/%d", self, review)
	}
	for range 30 {
		if err := current.IncrementLifetimeTicks(); err != nil {
			t.Fatal(err)
		}
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	current, err = store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	boot()
	if self, review := deadlines(); self != cognitive.SelfModelCadence || review != cognitive.IdentityReviewCadence {
		t.Fatalf("restart postponed owed self/review to %d/%d", self, review)
	}
	// TIME's own transition owns the next deadline after a firing. Boot
	// must not overwrite that settled value either.
	if ok, err := current.UpdateAlarmDeadlineCAS("reflect:self_model", cognitive.SelfModelCadence, 100); err != nil || !ok {
		t.Fatalf("settle reflection: %v %v", ok, err)
	}
	for range 30 {
		if err := current.IncrementLifetimeTicks(); err != nil {
			t.Fatal(err)
		}
	}
	boot()
	if self, review := deadlines(); self != 100 || review != cognitive.IdentityReviewCadence {
		t.Fatalf("another restart replaced a settled or due alarm: %d/%d", self, review)
	}
}
