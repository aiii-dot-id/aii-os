package updates

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// offeredWith is offered with the signed payload named: a checker over the
// install in, holding release ver of archive whose signature signs payload,
// published on GitHub at published.
func (rig *helperRig) offeredWith(t *testing.T, in *helperInstall, ver string, archive []byte, payload releaseArchivePayload, published string) (*Checker, func() []string) {
	t.Helper()
	sig := rig.signer.signReleasePayload(t, payload)
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
		TagName:     "v" + ver,
		PublishedAt: published,
		Assets: []githubAsset{
			{Name: assetName(ver), BrowserDownloadURL: srv.URL + "/archive"},
			{Name: assetName(ver) + ".platform.sig", BrowserDownloadURL: srv.URL + "/sig"},
		},
	})
	c.state.setAvailable(ver)
	return c, func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(fetched) }
}

// signedFor is the payload a release ver of archive is signed with.
func signedFor(ver string, archive []byte) releaseArchivePayload {
	return releaseArchivePayload{
		ArchiveHash: sha256hex(archive), Version: ver,
		Platform: packagefmt.HostPlatform(), Arch: hostArchForTest(), SourceRev: "highest0",
	}
}

func readHighestForTest(t *testing.T, tgt target) (highestVerified, bool) {
	t.Helper()
	raw, err := os.ReadFile(tgt.highest)
	if errors.Is(err, os.ErrNotExist) {
		return highestVerified{}, false
	}
	var h highestVerified
	if err != nil || json.Unmarshal(raw, &h) != nil {
		t.Fatalf("the record of the newest release verified does not read: %s (%v)", raw, err)
	}
	return h, true
}

func TestAReleaseBelowTheNewestVerifiedIsRefusedAndTheSameOneIsInstalledAgain(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	in := rig.install(t, archive)
	tgt := targetForTest(t, in.exe)
	if _, ok := readHighestForTest(t, tgt); ok {
		t.Fatal("a fresh install has a record of a release verified before it verified any")
	}

	// A fresh install with no record takes the release offered, and the
	// release it verified is recorded, with GitHub's date beside it.
	in.c.cachedRelease.PublishedAt = "2026-10-30T12:00:00Z"
	if err := in.apply(); err != nil {
		t.Fatalf("a fresh install refused the release offered: %v", err)
	}
	h, ok := readHighestForTest(t, tgt)
	platform, arch := hostTarget()
	if !ok || h.Version != "9.9.9" || h.ArchiveSHA256 != sha256hex(archive) || h.Platform != platform || h.Arch != arch ||
		h.ReleasePublishedAt != "2026-10-30T12:00:00Z" || time.Since(h.VerifiedAt) > time.Hour {
		t.Fatalf("the release verified is not what the record holds: %+v (present %v)", h, ok)
	}
	if !slices.Contains(Leftovers(tgt.exe), tgt.highest) {
		t.Error("Leftovers does not name the record of the newest release verified, so an uninstall would leave it")
	}

	// The update boots healthy, and later a person puts an older program
	// in its place by hand: this program has verified 9.9.9.
	checkRollbackAt(in.dir, in.exe)
	writeBootMarkerAt(in.dir, in.exe)
	if err := os.WriteFile(in.exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A correctly signed older release offered as the latest is refused by
	// both numbers, before anything is fetched, and nothing is touched.
	older, fetchedOlder := rig.offeredWith(t, in, "9.9.8", archive, signedFor("9.9.8", archive), "")
	err := older.applyTo(context.Background(), in.exe)
	var below *BelowHighestVerifiedError
	if !errors.As(err, &below) || below.Offered != "9.9.8" || below.Highest != "9.9.9" {
		t.Fatalf("a release below the newest verified was not refused as that: %v", err)
	}
	if want := "9.9.8 is older than 9.9.9, the newest release this program has verified, so it is not installed here."; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not name both releases: %q", err)
	}
	if got := fetchedOlder(); len(got) != 0 {
		t.Errorf("a release below the newest verified was fetched all the same: %q", got)
	}
	assertApplyFile(t, in.exe, "old binary")
	if exists(tgt.backup) || exists(tgt.record) || older.State().Snapshot("").NeedsRestart {
		t.Error("a refused release left a backup, a record or a pending restart")
	}
	if h2, _ := readHighestForTest(t, tgt); h2 != h {
		t.Errorf("a refusal changed the record: %+v, was %+v", h2, h)
	}

	// The same release is installed again: a program reinstalled lower is
	// brought back to it.
	same, fetchedSame := rig.offeredWith(t, in, "9.9.9", archive, signedFor("9.9.9", archive), "2026-10-30T12:00:00Z")
	if err := same.applyTo(context.Background(), in.exe); err != nil {
		t.Fatalf("the newest release verified was refused when offered again: %v", err)
	}
	if len(fetchedSame()) == 0 {
		t.Error("the release offered again was not fetched")
	}
	if got, _ := os.ReadFile(in.exe); string(got) == "old binary" {
		t.Error("the release offered again was not installed")
	}
	if h2, _ := readHighestForTest(t, tgt); h2 != h {
		t.Errorf("the same release verified again moved the record (its first archive is kept): %+v, was %+v", h2, h)
	}

	// A newer release raises the record.
	checkRollbackAt(in.dir, in.exe)
	writeBootMarkerAt(in.dir, in.exe)
	newer, _ := rig.offeredWith(t, in, "9.9.10", archive, signedFor("9.9.10", archive), "")
	if err := newer.applyTo(context.Background(), in.exe); err != nil {
		t.Fatalf("a newer release was refused: %v", err)
	}
	if h, _ := readHighestForTest(t, tgt); h.Version != "9.9.10" || h.ReleasePublishedAt != "" {
		t.Errorf("a newer release verified did not raise the record: %+v", h)
	}
}

func TestASecondDifferentArchiveOfAVerifiedReleaseIsRefused(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	in := rig.install(t, archive)
	tgt := targetForTest(t, in.exe)
	if err := in.apply(); err != nil {
		t.Fatalf("the first archive of 9.9.9 was refused: %v", err)
	}
	h, ok := readHighestForTest(t, tgt)
	if !ok || h.ArchiveSHA256 != sha256hex(archive) {
		t.Fatalf("the first archive is not recorded: %+v", h)
	}
	checkRollbackAt(in.dir, in.exe)
	writeBootMarkerAt(in.dir, in.exe)
	if err := os.WriteFile(in.exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	other := append(append([]byte(nil), archive...), 0)
	second, fetched := rig.offeredWith(t, in, "9.9.9", other, signedFor("9.9.9", other), "")
	err := second.applyTo(context.Background(), in.exe)
	var refused *SecondArchiveError
	if !errors.As(err, &refused) || refused.Version != "9.9.9" {
		t.Fatalf("a second, different archive of a verified release was not refused as that: %v", err)
	}
	if want := "a second, different 9.9.9 was offered"; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not say what was offered: %q", err)
	}
	if len(fetched()) == 0 {
		t.Fatal("the second archive was refused before it was fetched and its signature asked; this test asks both")
	}
	assertApplyFile(t, in.exe, "old binary")
	if exists(tgt.backup) || exists(tgt.record) || second.State().Snapshot("").NeedsRestart {
		t.Error("a refused second archive left a backup, a record or a pending restart")
	}
	if h2, _ := readHighestForTest(t, tgt); h2 != h {
		t.Errorf("a refused second archive changed the record: %+v, was %+v", h2, h)
	}

	same, _ := rig.offeredWith(t, in, "9.9.9", archive, signedFor("9.9.9", archive), "")
	if err := same.applyTo(context.Background(), in.exe); err != nil {
		t.Fatalf("the first archive offered again was refused: %v", err)
	}
}

// Written after the signature verifies, never on an offer or an install
// alone: a release whose signature does not bind its archive raises
// nothing, and so cannot be used to hold a program below a release it
// never verified.
func TestOnlyAVerifiedReleaseIsRecordedAsTheNewestVerified(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	in := rig.install(t, archive)
	tgt := targetForTest(t, in.exe)
	forged := signedFor("9.9.20", []byte("some other archive"))
	c, fetched := rig.offeredWith(t, in, "9.9.20", archive, forged, "")
	err := c.applyTo(context.Background(), in.exe)
	if err == nil || !strings.Contains(err.Error(), "signature verification REFUSED") {
		t.Fatalf("a release whose signature binds another archive was not refused at its signature: %v", err)
	}
	if len(fetched()) == 0 {
		t.Fatal("the release was refused before its signature was asked; this test asks the signature")
	}
	if h, ok := readHighestForTest(t, tgt); ok {
		t.Fatalf("a release whose signature did not verify was recorded as verified: %+v", h)
	}
	// And the program is not held below it: the release it really
	// verifies is installed.
	if err := in.apply(); err != nil {
		t.Fatalf("after a refused signature, the release that verifies was refused: %v", err)
	}
}

// The boot-health rollback is not an install: it puts the previous program
// back whatever the record says, and the record survives it and the
// restart after, so the restored program is still held above what it
// verified.
func TestTheNewestVerifiedSurvivesARollbackAndARestart(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	in, _ := besideInstall(t, rig) // the real apply of 9.9.9
	tgt := targetForTest(t, in.exe)
	if failedBoots(in) == "" {
		t.Fatal("two boots of the update that never finished did not roll it back")
	}
	assertApplyFile(t, in.exe, "old binary")
	if h, ok := readHighestForTest(t, tgt); !ok || h.Version != "9.9.9" {
		t.Fatalf("the rollback took the record of the newest release verified with it: %+v (present %v)", h, ok)
	}
	// The restarted program is a new checker over the same install.
	restarted, fetched := rig.offeredWith(t, in, "9.9.8", archive, signedFor("9.9.8", archive), "")
	var below *BelowHighestVerifiedError
	if err := restarted.applyTo(context.Background(), in.exe); !errors.As(err, &below) {
		t.Fatalf("after a rollback and a restart, a release below the newest verified was not refused: %v", err)
	}
	if len(fetched()) != 0 {
		t.Error("it was fetched")
	}
}

// A record that does not read cannot say which release it holds. It is
// removed and said, and the update goes on: whoever could spoil it could
// remove it.
func TestARecordOfTheNewestVerifiedThatDoesNotReadStopsNoUpdate(t *testing.T) {
	rig := newHelperRig(t)
	in := rig.install(t, contractArchive(t, packagefmt.HostPlatform()))
	tgt := targetForTest(t, in.exe)
	for _, bad := range []string{"{not json", `{"version":"not a version"}`} {
		if err := os.WriteFile(tgt.highest, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := tgt.belowHighest("1.0.0"); err != nil {
			t.Fatalf("an unreadable record (%s) refused a release: %v", bad, err)
		}
		if exists(tgt.highest) {
			t.Fatalf("the unreadable record (%s) was left to be met again", bad)
		}
	}
	if err := in.apply(); err != nil {
		t.Fatalf("the update after an unreadable record was refused: %v", err)
	}
	if h, ok := readHighestForTest(t, tgt); !ok || h.Version != "9.9.9" {
		t.Errorf("the next release verified did not write the record again: %+v", h)
	}
}

// What the card reads: the record beside the program it is asked about.
func TestTheCardIsToldTheNewestReleaseVerified(t *testing.T) {
	rig := newHelperRig(t)
	in := rig.install(t, contractArchive(t, packagefmt.HostPlatform()))
	if _, ok := highestVerifiedAt(in.exe); ok {
		t.Fatal("a program that verified nothing reports a release seen")
	}
	in.c.cachedRelease.PublishedAt = "2026-10-30T12:00:00Z"
	if err := in.apply(); err != nil {
		t.Fatal(err)
	}
	seen, ok := highestVerifiedAt(in.exe)
	if !ok || seen.Version != "9.9.9" || seen.PublishedAt != "2026-10-30T12:00:00Z" || seen.VerifiedAt.IsZero() {
		t.Errorf("the card is not told the release verified: %+v (%v)", seen, ok)
	}
}

// NO RELEASE BEFORE 0.2.0 READS OR REMOVES THE RECORD, so a rollback to
// 0.1.23 keeps it and the release after reads it again (brief rule 1). The
// names below are every file 0.1.23's updater keeps, reads or removes
// beside a program: its targetAt, verbatim from v0.1.23's
// internal/updates/updates.go, its staged files (stageStem, and a bundle's
// bundleStagePrefix) and the probe canStageBesideAt makes; its uninstall
// removes only these (Leftovers) and the backups beside them, each named
// <file>.previous. A record under any of these names, or under a
// name its staging or probe matches, is one 0.1.23 would read as its own
// or remove.
func TestTheHighestRecordIsANameTheReleasedPredecessorNeverTouches(t *testing.T) {
	apps := filepath.Join(string(filepath.Separator), "Applications")
	bundle := targetAt(filepath.Join(apps, "AII OS.app", "Contents", "MacOS", "aii"))
	bin := targetAt(filepath.Join(string(filepath.Separator), "opt", "aii", "aii"))
	predecessor := map[target][]string{
		bundle: {".aii-os-update.lock", ".aii-os-update.json", ".aii-os-previous.app"},
		bin: {".aii.update.lock", ".aii.update.json", ".aii.previous", ".aii.update.failed.json",
			".aii.update.incomplete.json"},
	}
	for tgt, names := range predecessor {
		if tgt.highest == "" || filepath.Dir(tgt.highest) != filepath.Dir(tgt.path) {
			t.Fatalf("the record is not beside the program: %q beside %q", tgt.highest, tgt.path)
		}
		base := filepath.Base(tgt.highest)
		if slices.Contains(names, base) {
			t.Errorf("the record %s is a name 0.1.23 keeps beside %s", base, tgt.path)
		}
		for _, stem := range []string{"." + filepath.Base(tgt.path) + ".stage-", ".aii-swap-probe-", ".aii-update-"} {
			if strings.HasPrefix(base, stem) {
				t.Errorf("the record %s is a name 0.1.23 stages or probes with (%s)", base, stem)
			}
		}
		if strings.HasSuffix(base, ".previous") {
			t.Errorf("the record %s is named as 0.1.23 names what an update kept beside the program", base)
		}
	}
}

// The hourly check shows the refusal where the operator reads why nothing
// was installed, says it to the log once for the release offered, and it
// is not a pending restart.
func TestTheHourlyCheckShowsWhyAReleaseBelowTheNewestVerifiedIsNotInstalled(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	srv, _ := blockingRelease(t, ready)
	c := checkNowChecker(t, srv, true, func() bool { return false }, func() bool { return false })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tgt := targetForTest(t, exe)
	raw, _ := json.Marshal(highestVerified{Version: "0.5.0"})
	if err := os.WriteFile(tgt.highest, raw, 0o644); err != nil {
		t.Fatalf("cannot write beside the test binary: %v", err)
	}
	t.Cleanup(func() { os.Remove(tgt.highest) })
	c.tick(t.Context())
	c.tick(t.Context())
	snap := c.State().Snapshot("0.3.0")
	if snap.AvailableVersion != "0.4.0" || snap.NeedsRestart || !strings.Contains(snap.LastError, "0.4.0 is older than 0.5.0, the newest release this program has verified") {
		t.Fatalf("the release below the newest verified is offered and its refusal is not shown: %+v", snap)
	}
	if c.saidBelow != "0.4.0" {
		t.Errorf("the refusal was not noted as said for 0.4.0: %q", c.saidBelow)
	}
}

// The hourly check shows a second archive's refusal on the card, as it does
// a release below the newest verified, and logs it once for the release.
func TestASecondArchivesRefusalIsShownAndLoggedOnce(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	srv, _ := blockingRelease(t, ready)
	c := checkNowChecker(t, srv, true, func() bool { return false }, func() bool { return false })
	err := &SecondArchiveError{Version: "0.4.0"}
	c.sayRefusal(err, err.Version, &c.saidSecond)
	if snap := c.State().Snapshot("0.3.0"); !strings.Contains(snap.LastError, "a second, different 0.4.0 was offered") {
		t.Fatalf("the refusal is not shown: %+v", snap)
	}
	if c.saidSecond != "0.4.0" {
		t.Errorf("the refusal was not noted as said for 0.4.0: %q", c.saidSecond)
	}
}
