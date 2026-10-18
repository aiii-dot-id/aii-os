package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// continuity_page_test.go — the person's door, where it meets the app: the
// config door takes its keys, and the application owns the slow acts the
// page asks for. The door itself — the escrow made in memory, the check
// held to THIS identity and THIS host's snapshot key — is
// internal/continuity's (continuity_page_test.go there).

const pagePass = "correct horse battery staple"

// THE DOOR TAKES THE THREE KEYS, held to their bounds, and they apply at once.
func TestTheConfigDoorTakesTheMaintenanceKeys(t *testing.T) {
	a, _, _ := maintApp(t)
	initial := a.configState().Maintenance
	if !initial.Enabled || initial.BackupKeep != continuity.MaintenanceKeep(continuity.MaintenanceConfig{}) {
		t.Fatalf("effective maintenance readback: %+v", initial)
	}
	for _, bad := range []map[string]any{
		{"maintenance.backup_keep": float64(0)}, {"maintenance.backup_keep": float64(366)},
		{"maintenance.enabled": "yes"}, {"maintenance.on_demand_spacing_seconds": float64(86401)},
	} {
		if _, err := a.applyConfigChange(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	saved, err := a.applyConfigChange(map[string]any{"maintenance.backup_keep": float64(14), "maintenance.enabled": false})
	if err != nil {
		t.Fatalf("a good change: %v", err)
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Maintenance struct {
			Enabled    bool `json:"enabled"`
			BackupKeep int  `json:"backup_keep"`
		} `json:"maintenance"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Maintenance.Enabled || wire.Maintenance.BackupKeep != 14 {
		t.Fatalf("save reply cannot confirm the submitted preferences: %+v", wire.Maintenance)
	}
	if v := continuityDoor(a).View(); v.Enabled || v.BackupKeep != 14 {
		t.Errorf("the change did not reach the view at once: %+v", v)
	}
}

// THE APPLICATION OWNS THE PAGE'S SLOW ACTS. It is wired as their owner — so
// Stop joins them before the store closes — it refuses new ones once it is
// stopping, and work a page asked for ends when the application does, not
// when the page's patience does.
func TestThePagesSlowActsBelongToTheApplication(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	hooks := continuityDoor(a)
	if hooks.Background == nil {
		stopApp(t, a)
		t.Fatal("the page's slow acts have no owner: they run detached")
	}

	// Owned work is joined: Stop does not return while it runs.
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	if !hooks.Background(func() {
		close(started)
		<-release
		// What the detached goroutines did after Stop began: read the store.
		_ = hooks.View()
		close(finished)
	}) {
		stopApp(t, a)
		t.Fatal("a running application refused owned work")
	}
	<-started
	stop := stopAppAside(a)
	stopped := stop.done
	select {
	case <-stopped:
		t.Fatal("STOP RETURNED WHILE THE PAGE'S WORK WAS STILL RUNNING: the store closes under it")
	case <-time.After(300 * time.Millisecond):
	}
	// And once stopping, nothing new starts.
	for wait := 0; ; wait++ {
		a.bgMu.Lock()
		stopping := a.stopping
		a.bgMu.Unlock()
		if stopping {
			break
		}
		if wait > 400 {
			t.Fatal("rig: Stop never began")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if hooks.Background(func() { t.Error("work was started by an application that is stopping") }) {
		t.Error("a stopping application accepted new work")
	}
	close(release)
	select {
	case <-stopped:
		stop.check(t)
	case <-time.After(30 * time.Second):
		t.Fatal("Stop never returned after the owned work finished")
	}
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before the owned work finished")
	}
}

// A page's patience is not the bound on work the application is stopping.
func TestWorkAPageAskedForEndsWhenTheApplicationStops(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	ctx, done := a.untilStopped(context.Background())
	defer done()
	select {
	case <-ctx.Done():
		t.Fatal("ended before anything stopped")
	default:
	}
	stopApp(t, a)
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("THE PAGE'S WORK OUTLIVES THE APPLICATION: its context did not end at Stop")
	}
}

// The update check's goroutine finishes on its own clock, and its last act
// is a status broadcast, which reads the store. After Stop has begun that
// broadcast is not made.
func TestAStatusBroadcastFromFinishedWorkIsNotMadeAfterStop(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	if !a.broadcastWhenOwned() {
		stopApp(t, a)
		t.Fatal("a running application did not broadcast")
	}
	stopApp(t, a)
	if a.broadcastWhenOwned() {
		t.Fatal("A BROADCAST WAS STARTED AFTER STOP: it reads a store that is closed or closing")
	}
}

func TestTheUpdateCheckersWorkBelongsToTheApplication(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	if a.updateChecker == nil {
		stopApp(t, a)
		t.Fatal("no update checker was wired")
	}
	stopApp(t, a)
	_, err := a.checkForUpdateNow()
	if !errors.Is(err, updates.ErrStopping) {
		t.Fatalf("A CHECK WAS ACCEPTED BY A STOPPED APPLICATION (or refused for another reason): %v", err)
	}
}
