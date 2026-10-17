package updates

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// AN INSTALL AN OLDER UPDATER BROUGHT HERE IS COMPLETED BY THE PROGRAM IT
// INSTALLED (complete.go). These drive the real apply, the real healthy
// boot and the real fetch-and-verify.

// byAnOlderUpdater leaves in as an updater from before 0.1.17 leaves an
// install: the real apply's new aii, the files beside it as they stood
// before, and a record that names nothing beside aii.
func byAnOlderUpdater(t *testing.T, rig *helperRig) (*helperInstall, []string) {
	t.Helper()
	in, names := besideInstall(t, rig)
	tgt := targetForTest(t, in.exe)
	pend, present, err := readRecord(tgt.record)
	if !present || err != nil {
		t.Fatalf("the update left no readable record: %v", err)
	}
	for _, b := range pend.Beside {
		os.Remove(besideBackup(tgt, b.Name))
		if err := os.WriteFile(filepath.Join(in.dir, b.Name), []byte("previous "+b.Name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pend.Beside, pend.Version = nil, ""
	if err := writeRecord(tgt.record, pend); err != nil {
		t.Fatal(err)
	}
	return in, names
}

// restarted is the program started again over in: a checker whose own
// release (its version is the test checker's, 1.0.0) is the signed archive.
func restarted(t *testing.T, rig *helperRig, in *helperInstall, archive []byte) (*Checker, func() []string) {
	t.Helper()
	c, fetched := rig.offered(t, in, "1.0.0", archive)
	release := c.cachedRelease
	c.ownRelease = func(context.Context) (*githubRelease, error) { return release, nil }
	return c, fetched
}

func TestAnInstallUpdatedByAnOlderUpdaterIsCompletedAtTheNextCheck(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	in, names := byAnOlderUpdater(t, rig)
	tgt := targetForTest(t, in.exe)
	image, err := os.ReadFile(in.exe)
	if err != nil {
		t.Fatal(err)
	}

	// The healthy boot of that update notes it, and retires the record.
	writeBootMarkerAt(in.dir, in.exe)
	raw, err := os.ReadFile(tgt.incomplete)
	var fact incompleteInstall
	if err != nil || json.Unmarshal(raw, &fact) != nil || fact.NewSHA256 != sha256hex(image) {
		t.Fatalf("the healthy boot of an update that installed aii alone left no note of it: %s (%v)", raw, err)
	}
	if exists(tgt.record) || exists(tgt.backup) {
		t.Fatal("the healthy boot did not retire the update")
	}
	if !slices.Contains(Leftovers(tgt.exe), tgt.incomplete) {
		t.Error("Leftovers does not name the note, so an uninstall would leave it")
	}

	// With no network the boot stays healthy, the install is said to be
	// incomplete, and nothing is changed.
	down, _ := restarted(t, rig, in, archive)
	down.ownRelease = func(context.Context) (*githubRelease, error) { return nil, errors.New("no route to host") }
	var incomplete *IncompleteInstallError
	if err := down.completeInstall(context.Background(), in.exe); !errors.As(err, &incomplete) ||
		!strings.Contains(err.Error(), "is incomplete") || !strings.Contains(err.Error(), "no route to host") || !strings.Contains(err.Error(), "tried again at the next check") {
		t.Fatalf("an install that could not be completed was not said to be incomplete, with why: %v", err)
	}
	for _, name := range names {
		assertApplyFile(t, filepath.Join(in.dir, name), "previous "+name)
	}
	if !exists(tgt.incomplete) {
		t.Fatal("a completion that failed forgot there was one to do")
	}

	// The next check completes it: the release's files beside aii, and
	// aii itself untouched; no update is recorded and no restart is asked.
	c, fetched := restarted(t, rig, in, archive)
	if err := c.completeInstall(context.Background(), in.exe); err != nil {
		t.Fatalf("the completion: %v", err)
	}
	for _, name := range names {
		assertApplyFile(t, filepath.Join(in.dir, name), name)
	}
	assertApplyFile(t, in.exe, string(image))
	if got := fetched(); !slices.Equal(got, []string{"/archive", "/sig"}) {
		t.Errorf("the completion fetched %q, want the archive and its signature and nothing else", got)
	}
	if exists(tgt.incomplete) || exists(tgt.record) || exists(tgt.backup) || c.State().Snapshot("").NeedsRestart {
		t.Error("a completed install left its note, a record, a backup or a pending restart")
	}
	// Nothing is left to do: a second check fetches nothing.
	again, fetchedAgain := restarted(t, rig, in, archive)
	if err := again.completeInstall(context.Background(), in.exe); err != nil || len(fetchedAgain()) != 0 {
		t.Errorf("a complete install was completed again: %v, fetched %q", err, fetchedAgain())
	}
}

// An update this build applies installs the whole set, and its healthy
// boot notes nothing.
func TestAnUpdateThatInstalledTheWholeSetLeavesNothingToComplete(t *testing.T) {
	in, _ := besideInstall(t, newHelperRig(t))
	writeBootMarkerAt(in.dir, in.exe)
	if exists(targetForTest(t, in.exe).incomplete) {
		t.Fatal("an update that installed everything was noted as incomplete")
	}
}

// The files beside aii are a release's only together with its aii: a
// verified archive whose aii is not the running program installs nothing,
// and a program that is no longer the noted image ends the matter.
func TestACompletionNeverPutsAnotherProgramsFilesBesideThisOne(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	name := installedBeside(packagefmt.HostPlatform())[0]

	t.Run("the release's aii is another program", func(t *testing.T) {
		in := rig.install(t, archive)
		tgt := targetForTest(t, in.exe)
		writeFile(t, in.dir, name, "previous "+name)
		raw, _ := json.Marshal(incompleteInstall{NewSHA256: sha256hex([]byte("old binary"))}) // the program that stands, which is no release's
		if err := os.WriteFile(tgt.incomplete, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		c, fetched := restarted(t, rig, in, archive)
		if err := c.completeInstall(context.Background(), in.exe); err != nil {
			t.Fatalf("a release that is not this program is no failure to retry: %v", err)
		}
		assertApplyFile(t, filepath.Join(in.dir, name), "previous "+name)
		assertApplyFile(t, in.exe, "old binary")
		if exists(tgt.incomplete) || len(fetched()) == 0 {
			t.Errorf("note still there: %v; fetched %q", exists(tgt.incomplete), fetched())
		}
	})
	t.Run("the program changed since the note", func(t *testing.T) {
		in := rig.install(t, archive)
		tgt := targetForTest(t, in.exe)
		raw, _ := json.Marshal(incompleteInstall{NewSHA256: sha256hex([]byte("an image long gone"))})
		if err := os.WriteFile(tgt.incomplete, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		c, fetched := restarted(t, rig, in, archive)
		if err := c.completeInstall(context.Background(), in.exe); err != nil || exists(tgt.incomplete) || len(fetched()) != 0 {
			t.Errorf("a note about another image: %v, note there %v, fetched %q", err, exists(tgt.incomplete), fetched())
		}
	})
	t.Run("a note that does not read", func(t *testing.T) {
		in := rig.install(t, archive)
		tgt := targetForTest(t, in.exe)
		if err := os.WriteFile(tgt.incomplete, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		c, fetched := restarted(t, rig, in, archive)
		if err := c.completeInstall(context.Background(), in.exe); err != nil || exists(tgt.incomplete) || len(fetched()) != 0 {
			t.Errorf("an unreadable note: %v, note there %v, fetched %q", err, exists(tgt.incomplete), fetched())
		}
	})
}

// Whole or not at all: when one file cannot be installed, what was already
// written goes back as it stood, and the completion is still owed.
func TestACompletionThatCannotFinishPutsBackWhatItWrote(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	// In the archive's own order: the completion writes them in it, so the
	// last is the one to fail at with the first already written.
	_, beside, err := extractBinary(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(beside) < 2 {
		t.Skip("this platform's release installs one file beside aii: there is no second to fail at")
	}
	first, last := beside[0].name, beside[len(beside)-1].name
	in, _ := byAnOlderUpdater(t, rig)
	tgt := targetForTest(t, in.exe)
	writeBootMarkerAt(in.dir, in.exe)
	// Once the first file is in place, the last one's place is taken by a
	// directory that is not empty: its install fails with the first written.
	step := replaceStep
	t.Cleanup(func() { replaceStep = step })
	replaceStep = func(at string) {
		if at != "completed "+first {
			return
		}
		assertApplyFile(t, filepath.Join(in.dir, first), first)
		if err := os.Remove(filepath.Join(in.dir, last)); err != nil {
			t.Error(err)
		}
		if err := os.MkdirAll(filepath.Join(in.dir, last, "held"), 0o755); err != nil {
			t.Error(err)
		}
	}
	c, _ := restarted(t, rig, in, archive)
	var incomplete *IncompleteInstallError
	if err := c.completeInstall(context.Background(), in.exe); !errors.As(err, &incomplete) {
		t.Fatalf("a completion that could not write %s was not said to be incomplete: %v", last, err)
	}
	assertApplyFile(t, filepath.Join(in.dir, first), "previous "+first)
	if !exists(tgt.incomplete) {
		t.Error("a completion that was put back forgot it is still owed")
	}
}

// The check shows it where the operator reads the state of updates.
func TestTheCheckSaysAnInstallIsIncompleteUntilItIsWhole(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	srv, _ := blockingRelease(t, ready)
	c := checkNowChecker(t, srv, true, func() bool { return false }, func() bool { return false })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tgt := targetForTest(t, exe)
	image, err := imageHash(tgt.exe)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(incompleteInstall{NewSHA256: image})
	if err := os.WriteFile(tgt.incomplete, raw, 0o644); err != nil {
		t.Skipf("cannot write beside the test binary: %v", err)
	}
	t.Cleanup(func() { os.Remove(tgt.incomplete) })
	c.ownRelease = func(context.Context) (*githubRelease, error) { return nil, errors.New("no route to host") }
	c.applying.Store(true) // isolate the check: no apply of the release it sees
	c.tick(t.Context())
	if snap := c.State().Snapshot("0.3.0"); !strings.Contains(snap.LastError, "is incomplete") || snap.NeedsRestart {
		t.Fatalf("an incomplete install is not shown by the check: %+v", snap)
	}
	if !exists(tgt.incomplete) {
		t.Error("the note is gone though nothing was completed")
	}
}
