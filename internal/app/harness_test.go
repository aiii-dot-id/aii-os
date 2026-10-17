package app

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The package's shared test harness: helpers many test files call, kept
// apart from the topic file any one of them belongs to. Nothing here is
// reachable from production.

// turnCancelRegistered reports whether a cancellable turn is in flight —
// observability for the shutdown cancel hook.
func (a *App) turnCancelRegistered() bool {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	return a.turnCancel != nil
}

// resetModeForTest restores a bare App to NORMAL (stops test beacons).
func (a *App) resetModeForTest() {
	a.mode.mu.Lock()
	a.mode.mode = ModeNormal
	a.mode.reason, a.mode.trigger, a.mode.detail = "", 0, ""
	a.mode.since = time.Time{}
	a.mode.witnessFails = 0
	a.mode.mu.Unlock()
	if a.store != nil {

		a.store.SetFrozen(false, "")
	}
	a.stopSafeBeacon()
}

// newBareApp is New for a test. The App is shut down when the test ends,
// through stopApp, so nothing it started — a SAFE beacon, a turn, a
// worker — outlives the test that made it, and a teardown that fails
// fails the test. A store or ledger the test hands it is the App's from
// then on: its teardown closes them, and the test does not.
func newBareApp(t testing.TB, cfg *Config) *App {
	t.Helper()
	a := New(cfg)
	t.Cleanup(func() { stopApp(t, a) })
	return a
}

// maintApp is an App standing on a real identity (birthFixture: every chain
// really signed) with its store, its ledger and its admission door open, as
// a boot leaves them — and nothing else running. A recovery point is
// captured at that door, and an event that reaches the ledger any other way
// leaves the mirror behind the record. Continuity's own tests stand on a
// fixture of their own (internal/continuity); this one is for the app's
// tests of what meets it: the door, the turn, the config door.
func maintApp(t *testing.T) (*App, string, string) {
	t.Helper()
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Maint")
	buildPriorProjection(t, ledgerPath, dbPath)
	a := newBareApp(t, &Config{
		SourcePath: filepath.Join(dir, "config.json"),
		Identity:   IdentityConfig{LedgerPath: ledgerPath, DBPath: dbPath, KeyPath: keyPath},
	})
	st, err := store.NewForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	a.store = st
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	a.ledger = lg
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	a.door = &ledgerAdapter{Ledger: lg, kp: kp, st: st}
	return a, dir, keyPath
}

func appendFixtureEvent(t *testing.T, a *App, keyPath, id string) {
	t.Helper()
	// experience.create: the one growth event whose replay is a plain
	// insert — a second relationship.upsert would (correctly) be refused
	// at materialization as an unsuperseded Ring 1 fork. Through the
	// door, so the mirror follows the record as it does in a running
	// identity.
	if _, err := a.door.Append(ledger.EventExperienceCreate, 3,
		map[string]any{
			"id": id, "content": "observed " + id, "category": "observation", "provenance": "self",
		}, ""); err != nil {
		t.Fatal(err)
	}
}

// live adds what a day adds to the database and nothing to the record:
// turns of conversation and lived time.
func live(t *testing.T, a *App, turns int) {
	t.Helper()
	for i := 0; i < turns; i++ {
		if err := a.store.AddConversationTurn("operator", "a turn the record never holds"); err != nil {
			t.Fatal(err)
		}
		if err := a.store.IncrementLifetimeTicks(); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshotNameRe matches a snapshot name: the pass's UTC stamp,
// the record it ends at, the on-demand
// mark, the encrypted form's suffix — read as a test reads it.
var snapshotNameRe = regexp.MustCompile(`^ledger-([0-9]{8}T[0-9]{6}Z)-seq([0-9]+)(-ondemand)?(\.tar\.age)?$`)

// backupFiles lists the plaintext snapshots published in dir, by name.
func backupFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && snapshotNameRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// continuityDoor is the person's door onto continuity, as the dashboard
// holds it (buildLiveHandler wires the same).
func continuityDoor(a *App) *dashboard.ContinuityHooks {
	h := &dashboard.WSHandler{}
	a.continuity.WireHooks(h)
	return h.Continuity
}

// maintenancePass runs the daily pass now, as TIME fires its owner.
func maintenancePass(t testing.TB, a *App) {
	t.Helper()
	a.continuity.AlarmOwner().OnAlarm(t.Context(), "", "", 0, "")
}

// askedSnapshot is Back up now on the page (the person's door is not
// spaced): the snapshot it made, as the page then lists it, newest first.
func askedSnapshot(t testing.TB, a *App) (dashboard.ContinuitySnapshot, error) {
	t.Helper()
	v, err := continuityDoor(a).Take(t.Context())
	if err != nil {
		return dashboard.ContinuitySnapshot{}, err
	}
	if len(v.Snapshots) == 0 {
		return dashboard.ContinuitySnapshot{}, errors.New("the page lists no snapshot after Back up now")
	}
	return v.Snapshots[0], nil
}

// askRestore is a person's restore from the page: the plan read, its
// confirm text typed.
func askRestore(t testing.TB, a *App, source, name string) error {
	t.Helper()
	door := continuityDoor(a)
	plan, err := door.RestorePlan(source, name)
	if err != nil {
		return err
	}
	return door.Restore(source, name, plan.ConfirmText)
}
