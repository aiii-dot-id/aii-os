package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestStandingOfferRoundTrips(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.StandingOffer(); err != nil || got != nil {
		t.Fatalf("a fresh store holds %v, %v — want nothing, no error", got, err)
	}
	want := []StandingSeat{{Name: "pl_com_example_memory_store", Print: "5b1c"}, {Name: "pl_com_example_memory_search", Print: "9e02"}}
	if err := s.SetStandingOffer(want); err != nil {
		t.Fatal(err)
	}
	if got, err := s.StandingOffer(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("read back %v, %v", got, err)
	}
	if err := s.SetStandingOffer(nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.StandingOffer(); err != nil || len(got) != 0 {
		t.Fatalf("a cleared offer reads back %v, %v", got, err)
	}
}

// The first writer stored a JSON list of names in the same runtime_meta row.
// Set up that persisted value before opening, not after today's schema owner.
func oldStandingOffer(t *testing.T, value string, compressed bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO runtime_meta VALUES (?,?,?)`, standingOfferKey, value, "original timestamp"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if compressed {
		target := filepath.Join(t.TempDir(), "compressed.db")
		if ok, err := ConvertDatabase(t.Context(), path, target, true); err != nil || !ok {
			t.Fatalf("compress fixture: %v %v", ok, err)
		}
		path = target
	}
	return path
}

func standingOfferBytes(t *testing.T, s *Store) string {
	t.Helper()
	var value, stamp string
	if err := s.DB().QueryRow(`SELECT value,updated_at FROM runtime_meta WHERE key=?`, standingOfferKey).Scan(&value, &stamp); err != nil {
		t.Fatal(err)
	}
	if stamp != "original timestamp" {
		t.Fatalf("conversion rewrote the recorded choice time: %q", stamp)
	}
	return value
}

func TestStandingOfferUpgradeNormalizesOnceWithoutGrantingFingerprints(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprint(compressed), func(t *testing.T) {
			path := oldStandingOffer(t, `["second","first","second"]`, compressed)
			for pass := 0; pass < 2; pass++ {
				s, err := New(path)
				if err != nil {
					t.Fatal(err)
				}
				if got := standingOfferBytes(t, s); got != `[{"name":"second"},{"name":"first"},{"name":"second"}]` {
					t.Errorf("normal open did not persist current seats: %s", got)
				}
				want := []StandingSeat{{Name: "second"}, {Name: "first"}, {Name: "second"}}
				if got, err := s.StandingOffer(); err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("recorded names/order changed or fingerprints invented: %v %v", got, err)
				}
				change, converted := s.schemaConversions["runtime_meta"]
				if converted != (pass == 0) || (converted && (change.Before != 1 || change.After != 1 || change.Reason == "")) {
					t.Errorf("conversion not reported exactly once: pass=%d %+v", pass, s.schemaConversions)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestStandingOfferUpgradePreservesCurrentAndUnreadableRecords(t *testing.T) {
	for _, value := range []string{`[ {"name":"kept","print":"recorded","future":"untouched"} ]`, `[]`, `null`, ``, `["unfinished"`, `{"future":true}`} {
		t.Run(value, func(t *testing.T) {
			path := oldStandingOffer(t, value, false)
			s, err := New(path)
			if err != nil {
				t.Fatalf("an optional unreadable offer must not prevent healthy startup: %v", err)
			}
			defer s.Close()
			if got := standingOfferBytes(t, s); got != value || len(s.schemaConversions) != 0 {
				t.Fatalf("current/unknown record rewritten: %q, %+v", got, s.schemaConversions)
			}
		})
	}
}

func TestStandingOfferUpgradeRollsBackWithReplayAndLeavesSafeReadOnly(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprint(compressed), func(t *testing.T) {
			const original = `["kept"]`
			path := oldStandingOffer(t, original, compressed)
			fault := errors.New("source interrupted after normalization")
			s, err := OpenAndReplay(t.Context(), path, MirrorHead{}, func(func(*ledger.Event) error) error { return fault })
			var outcome *SchemaFailure
			if s != nil || !errors.Is(err, fault) || !errors.As(err, &outcome) || outcome.Outcome != SchemaUnchanged {
				t.Fatalf("failed replay published a store or lost its outcome: %v %v", s, err)
			}
			ro, err := OpenReadOnly(path)
			if err != nil {
				t.Fatal(err)
			}
			defer ro.Close()
			if got := standingOfferBytes(t, ro); got != original {
				t.Fatalf("failed replay or SAFE mount changed original choice: %q", got)
			}
		})
	}
}

func TestStandingOfferReaderRequiresUpgradedRepresentation(t *testing.T) {
	s := testStore(t)
	if err := setRuntimeMeta(s.w(), standingOfferKey, `["not upgraded"]`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StandingOffer(); err == nil {
		t.Fatal("ordinary reader still interprets the old representation")
	}
}

func TestStandingOfferUpgradeComposesWithCommunicationsAndCarry(t *testing.T) {
	t.Run("communications", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "old.db")
		communicationsFixture(t, path, "communications-before-labels.sql")
		raw := openRawForTest(t, path)
		if _, err := raw.Exec(`INSERT INTO runtime_meta VALUES (?, ?, 'original timestamp')`, standingOfferKey, `["kept"]`); err != nil {
			t.Fatal(err)
		}
		raw.Close()
		s, err := New(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		var count int64
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM runtime_meta`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if c := s.schemaConversions["runtime_meta"]; c.Before != 1 || c.After != count || count <= 1 {
			t.Fatalf("standing conversion overwrote earlier conversion evidence: %+v actual=%d", c, count)
		}
		if got := standingOfferBytes(t, s); got != `[{"name":"kept"}]` {
			t.Fatalf("offer not upgraded alongside communications: %q", got)
		}
	})
	t.Run("carry", func(t *testing.T) {
		dbPath, ledgerPath, kp := carryFixture(t)
		raw := openRawForTest(t, dbPath)
		if _, err := raw.Exec(`INSERT INTO runtime_meta VALUES (?, ?, 'original timestamp')`, standingOfferKey, `["kept"]`); err != nil {
			t.Fatal(err)
		}
		raw.Close()
		report, err := CarryAcross(dbPath, ledgerPath, carryHead(t, ledgerPath, kp))
		if err != nil {
			t.Fatal(err)
		}
		if c, ok := report.Converted["runtime_meta"]; !ok || c.Before != 2 || c.After != 2 {
			t.Fatalf("carry did not report value conversion: %+v", report)
		}
		if _, unchanged := report.Ephemeral["runtime_meta"]; unchanged {
			t.Fatal("converted representation reported as unchanged")
		}
	})
}
