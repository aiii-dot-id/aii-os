package updates

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// offered is the program started again over an install that stands: a new
// checker holding a signed release ver of archive, and what it fetched.
func (rig *helperRig) offered(t *testing.T, in *helperInstall, ver string, archive []byte) (*Checker, func() []string) {
	t.Helper()
	sig := rig.signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: sha256hex(archive), Version: ver,
		Platform: packagefmt.HostPlatform(), Arch: hostArchForTest(), SourceRev: "failedhere0",
	})
	var mu sync.Mutex
	var fetched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fetched = append(fetched, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/archive":
			_, _ = w.Write(archive)
		case "/sig":
			_, _ = w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := newTestChecker(rig.signer.env, in.dir, &githubRelease{
		TagName: "v" + ver,
		Assets: []githubAsset{
			{Name: assetName(ver), BrowserDownloadURL: srv.URL + "/archive"},
			{Name: assetName(ver) + ".platform.sig", BrowserDownloadURL: srv.URL + "/sig"},
		},
	})
	c.state.setAvailable(ver)
	return c, func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(fetched) }
}

func TestARolledBackReleaseIsNotInstalledAgainAndTheNextOneIs(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	in, _ := besideInstall(t, rig) // the real apply of 9.9.9
	tgt := targetForTest(t, in.exe)

	// The swap's record names the release it installed.
	pend, present, err := readRecord(tgt.record)
	if !present || err != nil || pend.Version != "9.9.9" || pend.NewSHA256 == "" {
		t.Fatalf("the update's record does not name the release it installed: %+v (%v)", pend, err)
	}
	if exists(tgt.failed) {
		t.Fatal("nothing has failed here and a failed release is recorded")
	}
	if failedBoots(in) == "" {
		t.Fatal("two boots of the update that never finished did not roll it back")
	}
	assertApplyFile(t, in.exe, "old binary")

	// The rollback kept the one fact, and Leftovers names its file.
	raw, err := os.ReadFile(tgt.failed)
	var kept failedUpdate
	if err != nil || json.Unmarshal(raw, &kept) != nil || kept.Version != "9.9.9" || kept.NewSHA256 != pend.NewSHA256 || kept.At.IsZero() {
		t.Fatalf("the rollback did not record the release it undid: %s (%v)", raw, err)
	}
	if !slices.Contains(Leftovers(tgt.exe), tgt.failed) {
		t.Error("Leftovers does not name the record of a rolled-back release, so an uninstall would leave it")
	}

	// The restored program is offered the same release: refused by name,
	// before anything is fetched, and nothing is touched.
	again, fetched := rig.offered(t, in, "9.9.9", archive)
	err = again.applyTo(context.Background(), in.exe)
	var refused *FailedHereError
	if !errors.As(err, &refused) || refused.Version != "9.9.9" || refused.NewSHA256 != pend.NewSHA256 {
		t.Fatalf("the release that was rolled back here was not refused as that: %v", err)
	}
	if !strings.Contains(err.Error(), "9.9.9 did not finish a boot on this machine") || !strings.Contains(err.Error(), "A later release is installed as usual") {
		t.Errorf("the refusal does not say what failed and what happens next: %q", err)
	}
	if got := fetched(); len(got) != 0 {
		t.Errorf("a refused release was fetched all the same: %q", got)
	}
	assertApplyFile(t, in.exe, "old binary")
	if exists(tgt.backup) || exists(tgt.record) || again.State().Snapshot("").NeedsRestart {
		t.Error("a refused release left a backup, a record or a pending restart")
	}
	// And again an hour later: the fact stands until another release comes.
	if err := again.applyTo(context.Background(), in.exe); !errors.As(err, &refused) {
		t.Fatalf("the second offer of the rolled-back release was not refused: %v", err)
	}

	// A different release is installed as usual, and the fact has served.
	next, fetchedNext := rig.offered(t, in, "9.9.10", archive)
	if err := next.applyTo(context.Background(), in.exe); err != nil {
		t.Fatalf("the release after the one that failed was not installed: %v", err)
	}
	if len(fetchedNext()) == 0 || exists(tgt.failed) {
		t.Errorf("the next release: fetched %q, the old fact still there: %v", fetchedNext(), exists(tgt.failed))
	}
	if pend, present, err := readRecord(tgt.record); !present || err != nil || pend.Version != "9.9.10" {
		t.Errorf("the next release's record: %+v (%v)", pend, err)
	}
}

// A fact that does not read cannot say which release failed. Refusing every
// update over it would be the worse fault: it is removed, and the update
// goes on.
func TestAFailedReleaseRecordThatDoesNotReadStopsNoUpdate(t *testing.T) {
	rig := newHelperRig(t)
	in := rig.install(t, contractArchive(t, packagefmt.HostPlatform()))
	tgt := targetForTest(t, in.exe)
	if err := os.WriteFile(tgt.failed, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := in.apply(); err != nil {
		t.Fatalf("an unreadable record of a failed release stopped an update: %v", err)
	}
	if exists(tgt.failed) {
		t.Error("the unreadable record was left to be met again")
	}
}

// A record an earlier build wrote names no version: its rollback leaves no
// fact, as before, and nothing is refused on an empty name.
func TestARollbackOfARecordWithNoVersionLeavesNoFact(t *testing.T) {
	rig := newHelperRig(t)
	in, _ := besideInstall(t, rig)
	tgt := targetForTest(t, in.exe)
	pend, _, err := readRecord(tgt.record)
	if err != nil {
		t.Fatal(err)
	}
	pend.Version = ""
	if err := writeRecord(tgt.record, pend); err != nil {
		t.Fatal(err)
	}
	if failedBoots(in) == "" {
		t.Fatal("the update was not rolled back")
	}
	if exists(tgt.failed) {
		t.Error("a rollback that could not name the release left a record all the same")
	}
	if err := tgt.refusedHere(""); err != nil {
		t.Errorf("an empty release name was refused: %v", err)
	}
}

// The hourly check shows the refusal where the operator reads why nothing
// was installed, and it is not a pending restart.
func TestTheHourlyCheckShowsWhyARolledBackReleaseIsNotInstalled(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	srv, _ := blockingRelease(t, ready)
	c := checkNowChecker(t, srv, true, func() bool { return false }, func() bool { return false })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tgt := targetForTest(t, exe)
	raw, _ := json.Marshal(failedUpdate{Version: "0.4.0", NewSHA256: "ab"})
	if err := os.WriteFile(tgt.failed, raw, 0o644); err != nil {
		t.Skipf("cannot write beside the test binary: %v", err)
	}
	t.Cleanup(func() { os.Remove(tgt.failed) })
	c.tick(t.Context())
	c.tick(t.Context())
	snap := c.State().Snapshot("0.3.0")
	if snap.AvailableVersion != "0.4.0" || snap.NeedsRestart || !strings.Contains(snap.LastError, "0.4.0 did not finish a boot on this machine") {
		t.Fatalf("the rolled-back release is offered and its refusal is not shown: %+v", snap)
	}
	if c.saidFailedHere != "0.4.0" {
		t.Errorf("the refusal was not noted as said for 0.4.0: %q", c.saidFailedHere)
	}
}
