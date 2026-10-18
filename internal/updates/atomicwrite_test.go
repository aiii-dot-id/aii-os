package updates

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeFileAtomic is how the update machinery puts bytes under a real
// name — including aii.previous, the image a failed update restores
// from. What it must guarantee: the whole content, the intended mode,
// and no debris left behind for a later run to trip over.
func TestWriteFileAtomicPublishesWholeContentAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aii.previous")
	content := []byte("the whole binary, or the old one — never half")

	if err := writeFileAtomic(path, content, 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}

	// Windows carries no POSIX mode bits; Chmod there moves only the
	// read-only flag, so the claim is a Unix one.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("mode = %v, want 0755 — an executable published non-executable cannot be restored from", fi.Mode().Perm())
		}
	}

	assertNoDebris(t, dir, "aii.previous")
}

// Replacing an existing file is the ordinary case: the backup is
// rewritten on every update. The old bytes must go, and no temporary
// may survive under a name a later run could mistake for real.
func TestWriteFileAtomicReplacesWithoutLeavingDebris(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending.json")

	if err := writeFileAtomic(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("content = %q, want the replacement", got)
	}
	assertNoDebris(t, dir, "pending.json")
}

// The directory must hold the published name and nothing else. The
// earlier implementation used a fixed path+".tmp", which a failed run
// left behind — and whose permissions a later run would then inherit
// through O_CREATE|O_TRUNC.
func assertNoDebris(t *testing.T, dir, want string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == want {
			continue
		}
		t.Errorf("left behind %q in the publish directory; only %q should exist", e.Name(), want)
	}
	if len(entries) == 0 {
		t.Errorf("nothing was published at all")
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("a temporary file survived: %q", e.Name())
		}
	}
}

// The concrete reason the temporary name is now unique. The earlier
// implementation wrote to a FIXED path+".tmp"; an interrupted run left
// that file behind, and os.WriteFile does not apply its mode argument
// to a file that already exists. The next update therefore published
// the stale file's permissions instead of the ones it asked for — so a
// restore image could land non-executable, and nothing would say why.
func TestWriteFileAtomicIgnoresAStaleTemporary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "aii.previous")

	// Exactly the debris an interrupted run used to leave.
	stale := path + ".tmp"
	if err := os.WriteFile(stale, []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(path, []byte("the real image"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("published mode = %v, want 0755: the stale temporary's permissions were inherited", fi.Mode().Perm())
	}
	if got, _ := os.ReadFile(path); string(got) != "the real image" {
		t.Errorf("published content = %q, want the new bytes", got)
	}
}

// THE RELEASE BLOCKER, end to end. An interrupted update leaves debris
// at the staging name. os.WriteFile does not apply its mode argument to
// a file that already exists, so the swap used to publish the debris's
// permissions onto the RUNNING BINARY — 0600, which cannot be executed.
// The install is then bricked by the update mechanism itself.
func TestSwapPublishesAnExecutableDespiteStaleStaging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	// Exactly what an interrupted run leaves behind.
	if err := os.WriteFile(exePath+".new", []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := swapForTest(t, exePath, []byte("new binary"), dir); err != nil {
		t.Fatalf("swap: %v", err)
	}
	fi, err := os.Stat(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("the running binary was published as %v — it cannot be executed, so the update bricked the install", fi.Mode().Perm())
	}
	if got, _ := os.ReadFile(exePath); string(got) != "new binary" {
		t.Fatalf("published content = %q, want the new image", got)
	}
}

// And the same debris on the rescue path, which is worse: the rollback
// exists to recover a failed update, so publishing a non-executable
// image here bricks the thing that was supposed to do the saving.
func TestRollbackRestoresAnExecutableDespiteStaleStaging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	if _, err := swapForTest(t, exePath, []byte("new binary"), dir); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if err := os.WriteFile(exePath+".rollback", []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}

	// First boot begins the attempt; the second finds no marker and restores.
	checkRollbackAt(dir, exePath)
	if rolled := checkRollbackAt(dir, exePath); rolled == "" {
		t.Fatal("a failed update boot did not roll back")
	}

	fi, err := os.Stat(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("the RESCUE image was restored as %v — the rollback bricked what it came to rescue", fi.Mode().Perm())
	}
	if got, _ := os.ReadFile(exePath); string(got) != "old binary" {
		t.Fatalf("restored content = %q, want the backup", got)
	}
}

// The boot marker is RENAMED aside, not deleted. Its retirement has to
// be durable before the new executable is published — a lost unlink
// beside a surviving publish leaves the next boot calling an unproven
// update healthy and deleting the backup that could have undone it.
// atomicfile.Replace is the only operation here durable on BOTH
// platforms, so the marker moves through it.
func TestSwapRetiresTheMarkerRatherThanDeletingIt(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	if _, err := swapForTest(t, exePath, []byte("new binary"), dir); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if fileExists(dir, ".boot_completed") {
		t.Error("the live marker must not survive the swap — the next boot would read the update as already proven")
	}
	if !fileExists(dir, ".boot_completed.retired") {
		t.Error("the marker must be retired aside, not deleted: a delete is durable on neither platform without extra work, and on Windows there is no directory handle to sync")
	}

	// A healthy boot ends the retired marker's job along with the rest
	// of the update state.
	writeBootMarkerAt(dir, exePath)
	if fileExists(dir, ".boot_completed.retired") {
		t.Error("a healthy boot must clear the retired marker too, or it accumulates across updates")
	}
	if !fileExists(dir, ".boot_completed") {
		t.Error("a healthy boot must arm the live marker")
	}
}

// The retire/re-arm pair, driven directly. The swap-failure route into
// re-arm needs staging to fail, and the only lever for that is
// directory permissions — which root ignores, so a test written that
// way SKIPS on this host and a mutation removing the re-arm survives
// it. Testing the pair is the honest coverage.
func TestBootMarkerRetiresAsideAndComesBack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".boot_completed", "ok")

	retired, err := retireBootMarker(dir)
	if err != nil || !retired {
		t.Fatalf("retire: %v %v", retired, err)
	}
	if fileExists(dir, ".boot_completed") {
		t.Error("the live marker must be gone once retired")
	}
	if !fileExists(dir, ".boot_completed.retired") {
		t.Error("the marker must be moved ASIDE, not deleted")
	}

	if back, err := rearmBootMarker(dir); err != nil || !back {
		t.Fatalf("rearm: back=%v err=%v", back, err)
	}
	if !fileExists(dir, ".boot_completed") {
		t.Error("re-arm must put the marker back: the old binary is still in place and had booted healthy")
	}
	if fileExists(dir, ".boot_completed.retired") {
		t.Error("a re-armed marker must not also be left retired")
	}

	// Nothing armed: retiring is a no-op that says so, so the caller
	// does not later try to put back something it never moved.
	if err := os.Remove(filepath.Join(dir, ".boot_completed")); err != nil {
		t.Fatal(err)
	}
	retired, err = retireBootMarker(dir)
	if err != nil {
		t.Fatalf("retiring nothing must not error: %v", err)
	}
	if retired {
		t.Error("retiring nothing must report false")
	}
}

// RETIRING MUST NOT FAIL OPEN. The preflight os.Stat treated every
// failure as "nothing armed" — permission, I/O, a broken symlink — and
// returned success, so the swap published a new executable with the
// healthy marker still in place and the next boot blessed a binary
// that had never proved itself.
//
// A dangling symlink is the case that separates the two
// implementations without needing permissions root would ignore:
// os.Stat follows the link and fails, while os.Rename moves the link
// itself and succeeds. The marker IS there; Stat simply cannot see it.
func TestRetiringABootMarkerDoesNotFailOpen(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".boot_completed")
	if err := os.Symlink(filepath.Join(dir, "target-that-is-gone"), marker); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("setup: this marker must be un-Stat-able for the test to mean anything")
	}

	retired, err := retireBootMarker(dir)
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if !retired {
		t.Fatal("a marker that EXISTS but cannot be Stat'd was reported absent — the swap would publish with it still armed, and the next boot would bless an unproven binary")
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Error("the marker is still in place")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".boot_completed.retired")); err != nil {
		t.Errorf("it was not moved aside: %v", err)
	}
}

// The caller deletes the backup and the tombstone on the strength of
// this answer, so a re-arm that could not put the marker back has to
// say so rather than return nil.
func TestRearmBootMarkerReportsItsFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".boot_completed.retired", "ok")
	// A non-empty directory cannot be renamed over.
	if err := os.MkdirAll(filepath.Join(dir, ".boot_completed", "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	back, err := rearmBootMarker(dir)
	if err == nil {
		t.Error("a re-arm that did not restore the marker must report failure")
	}
	if back {
		t.Error("a rename that never landed must not report the marker back")
	}

	// And the ordinary case reports it back, so the caller's message
	// can tell the two apart.
	clean := t.TempDir()
	writeFile(t, clean, ".boot_completed.retired", "ok")
	if back, err := rearmBootMarker(clean); err != nil || !back {
		t.Errorf("a clean re-arm must report back=true, got %v %v", back, err)
	}
	if !fileExists(clean, ".boot_completed") {
		t.Error("the marker was not restored")
	}
}

// THE FOUR MEANINGS OF Replace's ANSWER. published says the rename
// landed; err says it is durable. Reading only err collapses the pair,
// and the case that gets lost is the dangerous one: a rename that
// landed WITHOUT durable proof, which an ErrNotExist from the sync
// turned into "no marker existed" — after which the swap published the
// executable with the healthy marker possibly still to come back.
//
// This is the injection the natural falsifiers cannot reach: no
// filesystem this test can arrange returns published=true with
// ErrNotExist, so the classification is exercised directly.
func TestClassifyRetireNeverReadsAnUnsyncedRenameAsAbsence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		published bool
		err       error
		retired   bool
		wantErr   bool
	}{
		{"clean rename", true, nil, true, false},
		{"nothing was armed", false, os.ErrNotExist, false, false},
		{"rename failed outright", false, errors.New("permission denied"), false, true},
		{"landed but not durable", true, errors.New("i/o error"), true, true},
		// THE ONE THAT WAS WRONG: durable proof missing, and the errno
		// happens to be ErrNotExist. It must NOT read as absence.
		{"landed, unsynced, ErrNotExist", true, os.ErrNotExist, true, true},
	} {
		retired, err := classifyRetire(tc.published, tc.err)
		if retired != tc.retired {
			t.Errorf("%s: retired = %v, want %v", tc.name, retired, tc.retired)
		}
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, want error: %v", tc.name, err, tc.wantErr)
		}
		// An uncertain retirement must never be silently successful:
		// the caller publishes a binary on the strength of that answer.
		if tc.published && tc.err != nil && err == nil {
			t.Errorf("%s: a rename with no durable proof reported success", tc.name)
		}
	}
}

// And the caller honours it: a retirement that did not certainly happen
// stops the swap before anything is published.
func TestSwapDoesNotPublishWhenRetirementFails(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")
	// A non-empty directory cannot be renamed over, so the retirement
	// fails without any permission root would ignore.
	if err := os.MkdirAll(filepath.Join(dir, ".boot_completed.retired", "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}

	tgt, err := swapForTest(t, exePath, []byte("new binary"), dir)
	if err == nil {
		t.Fatal("a swap whose marker could not be retired must not proceed")
	}
	if got, _ := os.ReadFile(exePath); string(got) != "old binary" {
		t.Errorf("the executable was published anyway: %q", got)
	}
	if !fileExists(dir, ".boot_completed") {
		t.Error("the live marker was lost even though nothing was published")
	}
	// Nothing was published, so the attempt withdraws its claim and its
	// backup: another update is not refused over a swap that never was.
	if exists(tgt.backup) || exists(tgt.record) {
		t.Error("a swap that published nothing left its backup or record beside the target")
	}
}
