package app

import (
	"context"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/updates"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .

const pagePass = "correct horse battery staple"

// .
// .
// .
func TestMakingAndCheckingAnEscrowFromThePageTurnsEncryptionOn(t *testing.T) {
	a, home, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_a")
	hostSnapshotKey(t, a)

	v := a.continuityView()
	if v.Encrypting || v.Unencrypted != store.UnencryptedNoEscrow || !strings.Contains(v.UnencryptedText, "made and checked below") {
		t.Fatalf("before any escrow: %+v", v)
	}
	if strings.Contains(v.UnencryptedText, "aii ") || strings.Contains(v.UnencryptedText, home) {
		t.Errorf("the page's words send a person to a terminal or a path: %q", v.UnencryptedText)
	}

	before := dirListing(t, home)
	file, name, err := a.escrowCreateHere([]byte(pagePass))
	if err != nil || len(file) == 0 || !strings.HasPrefix(name, "escrow-"+a.door.kp.Fingerprint()[:12]+"-") || !strings.HasSuffix(name, ".age") {
		t.Fatalf("create: %q %v", name, err)
	}
	if after := dirListing(t, home); after != before {
		t.Errorf("MAKING THE FILE WROTE ON THE HOST: before %s, after %s", before, after)
	}
	if a.continuityView().Encrypting {
		t.Fatal("MAKING THE FILE TURNED ENCRYPTION ON: a file nobody has saved protects nothing")
	}
	// .
	// .
	c, kp, err := escrow.Open(file, []byte(pagePass))
	if err != nil || kp.Fingerprint() != a.door.kp.Fingerprint() || c.SnapshotKey == nil {
		t.Fatalf("the file the person saves: %v", err)
	}
	c.Wipe()

	// .
	// .
	for what, tc := range map[string]struct{ file, pass []byte }{
		"a wrong passphrase":     {file, []byte("not the passphrase at all")},
		"a file that is not one": {[]byte("hello"), []byte(pagePass)},
		"a truncated file":       {file[:len(file)/2], []byte(pagePass)},
		"no file":                {nil, []byte(pagePass)},
	} {
		if _, err := a.escrowCheckHere(tc.file, tc.pass); err == nil || strings.Contains(err.Error(), home) {
			t.Errorf("%s: %v", what, err)
		}
	}
	if a.continuityView().Encrypting {
		t.Fatal("a check that failed turned encryption on")
	}

	view, err := a.escrowCheckHere(file, []byte(pagePass))
	if err != nil || !view.Encrypting || !view.EscrowCovers || view.EscrowCheckedAt == "" {
		t.Fatalf("checking the saved file: %+v %v", view, err)
	}
	if _, err := os.Stat(a.escrowReceiptPath(cfg)); err != nil {
		t.Fatalf("no receipt was written: %v", err)
	}
	made, err := a.personTake(t.Context())
	if err != nil || len(made.Snapshots) != 1 || !made.Snapshots[0].Encrypted || !made.Snapshots[0].OnDemand {
		t.Fatalf("Back up now after a checked escrow: %+v %v", made.Snapshots, err)
	}
	if said, err := a.personVerify(t.Context(), made.Snapshots[0].Name); err != nil || !strings.Contains(said, "proved") {
		t.Fatalf("Prove it restores: %q %v", said, err)
	}
}

// .
// .
// .
func TestAnEscrowThatDoesNotCoverThisHostSignsNoReceipt(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_a")
	hostSnapshotKey(t, a)
	old, _, err := a.escrowCreateHere([]byte(pagePass))
	if err != nil {
		t.Fatal(err)
	}
	// .
	os.Remove(a.snapshotKeyPath(cfg))
	hostSnapshotKey(t, a)
	if _, err := a.escrowCheckHere(old, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "OLDER snapshot key") {
		t.Errorf("an escrow holding an older snapshot key: %v", err)
	}

	// .
	otherDir := t.TempDir()
	otherKP, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(otherDir, "identity.sec")
	if _, err := crypto.SaveKeyPair(otherKP, otherPath); err != nil {
		t.Fatal(err)
	}
	otherKey, _ := os.ReadFile(otherPath)
	snap, _ := escrow.NewSnapshotKey()
	foreign, err := escrow.Seal(escrow.Contents{IdentityKey: otherKey, SnapshotKey: snap}, []byte(pagePass))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.escrowCheckHere(foreign, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "another identity") {
		t.Errorf("another identity's escrow: %v", err)
	}
	if _, err := os.Stat(a.escrowReceiptPath(cfg)); !os.IsNotExist(err) {
		t.Fatalf("A RECEIPT WAS SIGNED FOR AN ESCROW THAT DOES NOT COVER THIS HOST: %v", err)
	}
	if a.continuityView().Encrypting {
		t.Fatal("encryption is on with no escrow that covers this host")
	}
}

// .
// .
func TestThePagesEscrowRefusals(t *testing.T) {
	a, _, keyPath := maintApp(t)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	if _, _, err := a.escrowCreateHere([]byte(pagePass)); err == nil || !strings.Contains(err.Error(), "no snapshot key yet") {
		t.Errorf("before the snapshot key exists: %v", err)
	}
	hostSnapshotKey(t, a)
	if _, _, err := a.escrowCreateHere([]byte("short")); err == nil || !strings.Contains(err.Error(), "shorter than") {
		t.Errorf("a short passphrase: %v", err)
	}
	file, _, err := a.escrowCreateHere([]byte(pagePass))
	if err != nil {
		t.Fatal(err)
	}
	a.enterSafe("test: the record is frozen")
	if _, _, err := a.escrowCreateHere([]byte(pagePass)); err == nil || !strings.Contains(err.Error(), "SAFE") {
		t.Errorf("create in SAFE: %v", err)
	}
	if _, err := a.escrowCheckHere(file, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "SAFE") {
		t.Errorf("check in SAFE: %v", err)
	}
	if _, err := a.personTake(t.Context()); err == nil || !strings.Contains(err.Error(), "Restart normally") || strings.Contains(err.Error(), "recall source") || strings.Contains(err.Error(), "your operator") {
		t.Errorf("Back up now in SAFE must be refused in the PERSON's words: %v", err)
	}
	if v := a.continuityView(); !v.Safe || v.NextPass != "" {
		t.Errorf("the view in SAFE: %+v", v)
	}
}

// .
func TestTheConfigDoorTakesTheMaintenanceKeys(t *testing.T) {
	a, _, _ := maintApp(t)
	for _, bad := range []map[string]interface{}{
		{"maintenance.backup_keep": float64(0)}, {"maintenance.backup_keep": float64(366)},
		{"maintenance.enabled": "yes"}, {"maintenance.on_demand_spacing_seconds": float64(86401)},
	} {
		if _, err := a.applyConfigChange(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	if _, err := a.applyConfigChange(map[string]interface{}{"maintenance.backup_keep": float64(14), "maintenance.enabled": false}); err != nil {
		t.Fatalf("a good change: %v", err)
	}
	if v := a.continuityView(); v.Enabled || v.BackupKeep != 14 {
		t.Errorf("the change did not reach the view at once: %+v", v)
	}
}

// .
func dirListing(t *testing.T, root string) string {
	t.Helper()
	var names []string
	filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err == nil {
			names = append(names, strings.TrimPrefix(p, root))
		}
		return nil
	})
	return strings.Join(names, "|")
}

// .
// .
// .
// .
func TestThePagesSlowActsBelongToTheApplication(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	hooks := a.continuityHooks()
	if hooks.Background == nil {
		a.Stop()
		t.Fatal("the page's slow acts have no owner: they run detached")
	}

	// .
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	if !hooks.Background(func() {
		close(started)
		<-release
		// .
		_ = a.continuityView()
		close(finished)
	}) {
		a.Stop()
		t.Fatal("a running application refused owned work")
	}
	<-started
	stopped := make(chan struct{})
	go func() { a.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("STOP RETURNED WHILE THE PAGE'S WORK WAS STILL RUNNING: the store closes under it")
	case <-time.After(300 * time.Millisecond):
	}
	// .
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
	case <-time.After(30 * time.Second):
		t.Fatal("Stop never returned after the owned work finished")
	}
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before the owned work finished")
	}
}

// .
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
	a.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("THE PAGE'S WORK OUTLIVES THE APPLICATION: its context did not end at Stop")
	}
}

// .
// .
// .
func TestAStatusBroadcastFromFinishedWorkIsNotMadeAfterStop(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	if !a.broadcastWhenOwned() {
		a.Stop()
		t.Fatal("a running application did not broadcast")
	}
	a.Stop()
	if a.broadcastWhenOwned() {
		t.Fatal("A BROADCAST WAS STARTED AFTER STOP: it reads a store that is closed or closing")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestTheUpdateCheckersWorkBelongsToTheApplication(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	if a.updateChecker == nil {
		a.Stop()
		t.Fatal("no update checker was wired")
	}
	a.Stop()
	_, err := a.checkForUpdateNow()
	if !errors.Is(err, updates.ErrStopping) {
		t.Fatalf("A CHECK WAS ACCEPTED BY A STOPPED APPLICATION (or refused for another reason): %v", err)
	}
}
