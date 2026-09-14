package updates

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// .
// .
// .
// .
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

	// .
	// .
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

// .
// .
// .
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

// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
func TestWriteFileAtomicIgnoresAStaleTemporary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "aii.previous")

	// .
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

// .
// .
// .
// .
// .
func TestSwapPublishesAnExecutableDespiteStaleStaging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	// .
	if err := os.WriteFile(exePath+".new", []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := swapBinary(exePath, []byte("new binary"), dir); err != nil {
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

// .
// .
// .
func TestRollbackRestoresAnExecutableDespiteStaleStaging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	if err := swapBinary(exePath, []byte("new binary"), dir); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if err := os.WriteFile(exePath+".rollback", []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}

	// .
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

// .
// .
// .
// .
// .
// .
func TestSwapRetiresTheMarkerRatherThanDeletingIt(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	if err := swapBinary(exePath, []byte("new binary"), dir); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if fileExists(dir, ".boot_completed") {
		t.Error("the live marker must not survive the swap — the next boot would read the update as already proven")
	}
	if !fileExists(dir, ".boot_completed.retired") {
		t.Error("the marker must be retired aside, not deleted: a delete is durable on neither platform without extra work, and on Windows there is no directory handle to sync")
	}

	// .
	// .
	WriteBootMarker(dir)
	if fileExists(dir, ".boot_completed.retired") {
		t.Error("a healthy boot must clear the retired marker too, or it accumulates across updates")
	}
	if !fileExists(dir, ".boot_completed") {
		t.Error("a healthy boot must arm the live marker")
	}
}

// .
// .
// .
// .
// .
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

	// .
	// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
func TestRearmBootMarkerReportsItsFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".boot_completed.retired", "ok")
	// .
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

	// .
	// .
	clean := t.TempDir()
	writeFile(t, clean, ".boot_completed.retired", "ok")
	if back, err := rearmBootMarker(clean); err != nil || !back {
		t.Errorf("a clean re-arm must report back=true, got %v %v", back, err)
	}
	if !fileExists(clean, ".boot_completed") {
		t.Error("the marker was not restored")
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
		// .
		// .
		{"landed, unsynced, ErrNotExist", true, os.ErrNotExist, true, true},
	} {
		retired, err := classifyRetire(tc.published, tc.err)
		if retired != tc.retired {
			t.Errorf("%s: retired = %v, want %v", tc.name, retired, tc.retired)
		}
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, want error: %v", tc.name, err, tc.wantErr)
		}
		// .
		// .
		if tc.published && tc.err != nil && err == nil {
			t.Errorf("%s: a rename with no durable proof reported success", tc.name)
		}
	}
}

// .
// .
func TestSwapDoesNotPublishWhenRetirementFails(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")
	// .
	// .
	if err := os.MkdirAll(filepath.Join(dir, ".boot_completed.retired", "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := swapBinary(exePath, []byte("new binary"), dir); err == nil {
		t.Fatal("a swap whose marker could not be retired must not proceed")
	}
	if got, _ := os.ReadFile(exePath); string(got) != "old binary" {
		t.Errorf("the executable was published anyway: %q", got)
	}
	if !fileExists(dir, ".boot_completed") {
		t.Error("the live marker was lost even though nothing was published")
	}
}
