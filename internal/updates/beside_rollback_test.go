package updates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// besideInstall is a bare install in which every file this host's release
// installs beside aii stands at its previous release, updated through the
// real apply to an archive in the contract's layout.
func besideInstall(t *testing.T, rig *helperRig) (*helperInstall, []string) {
	t.Helper()
	platform := packagefmt.HostPlatform()
	in := rig.install(t, contractArchive(t, platform))
	names := installedBeside(platform)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(in.dir, name), []byte("previous "+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := in.apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return in, names
}

// failedBoots is two boots of the update that began and never finished:
// the first is counted, the second rolls back.
func failedBoots(in *helperInstall) string {
	checkRollbackAt(in.dir, in.exe)
	return checkRollbackAt(in.dir, in.exe)
}

func TestAnUpdateKeepsWhatItDisplacedBesideAiiAndSaysSoInItsRecord(t *testing.T) {
	in, names := besideInstall(t, newHelperRig(t))
	tgt := targetForTest(t, in.exe)
	pend, present, err := readRecord(tgt.record)
	if !present || err != nil {
		t.Fatalf("the update left no readable record: %v", err)
	}
	var recorded []string
	for _, b := range pend.Beside {
		recorded = append(recorded, b.Name)
		if b.BackupSHA256 != sha256hex([]byte("previous "+b.Name)) || b.NewSHA256 != sha256hex([]byte(b.Name)) {
			t.Errorf("the record's hashes for %s are not those of what stood and what was installed: %+v", b.Name, b)
		}
		assertApplyFile(t, besideBackup(tgt, b.Name), "previous "+b.Name)
		assertApplyFile(t, filepath.Join(in.dir, b.Name), b.Name)
	}
	if !slices.Equal(recorded, names) {
		t.Fatalf("the record names %q beside aii, want every file the update installed there: %q", recorded, names)
	}
	// Uninstall is told of every one of them.
	left := Leftovers(tgt.exe) // the resolved path, as tgt's names are (a temp directory may sit behind a symlink)
	for _, name := range names {
		if !slices.Contains(left, besideBackup(tgt, name)) {
			t.Errorf("Leftovers does not name %s, which an update keeps beside the program", besideBackup(tgt, name))
		}
	}
}

func TestARollbackRestoresWhatStoodBesideAii(t *testing.T) {
	in, names := besideInstall(t, newHelperRig(t))
	tgt := targetForTest(t, in.exe)
	if restored := failedBoots(in); restored == "" {
		t.Fatal("two boots of the update that never finished did not roll it back")
	}
	assertApplyFile(t, in.exe, "old binary")
	for _, name := range names {
		assertApplyFile(t, filepath.Join(in.dir, name), "previous "+name)
		if exists(besideBackup(tgt, name)) {
			t.Errorf("the rollback is done and what it kept of %s is still there", name)
		}
		if fi, err := os.Stat(filepath.Join(in.dir, name)); err != nil || !installedExecutable(fi) {
			t.Errorf("%s was restored %v (%v), want an executable", name, fi, err)
		}
	}
	if exists(tgt.backup) || exists(tgt.record) {
		t.Error("the rollback is done and its backup or record is still there")
	}
}

// What the update put where nothing stood goes with it; what someone changed
// since the update stays theirs; and what was kept is restored only while it
// is still what the swap recorded.
func TestARollbackTreatsEachFileBesideAiiByWhatItFinds(t *testing.T) {
	rig := newHelperRig(t)
	platform := packagefmt.HostPlatform()
	name := installedBeside(platform)[0]

	t.Run("added by the update", func(t *testing.T) {
		in := rig.install(t, contractArchive(t, platform))
		if err := in.apply(); err != nil {
			t.Fatal(err)
		}
		assertApplyFile(t, filepath.Join(in.dir, name), name)
		if failedBoots(in) == "" {
			t.Fatal("no rollback")
		}
		assertApplyFile(t, in.exe, "old binary")
		if exists(filepath.Join(in.dir, name)) {
			t.Errorf("%s, which the update added, is still beside the previous aii", name)
		}
	})
	t.Run("changed since the update", func(t *testing.T) {
		in, _ := besideInstall(t, rig)
		if err := os.WriteFile(filepath.Join(in.dir, name), []byte("a person's own "+name), 0o755); err != nil {
			t.Fatal(err)
		}
		if failedBoots(in) == "" {
			t.Fatal("no rollback")
		}
		assertApplyFile(t, in.exe, "old binary")
		assertApplyFile(t, filepath.Join(in.dir, name), "a person's own "+name)
	})
	t.Run("a backup that is not what was kept", func(t *testing.T) {
		in, _ := besideInstall(t, rig)
		tgt := targetForTest(t, in.exe)
		if err := os.WriteFile(besideBackup(tgt, name), []byte("not what stood"), 0o644); err != nil {
			t.Fatal(err)
		}
		if failedBoots(in) == "" {
			t.Fatal("no rollback")
		}
		assertApplyFile(t, in.exe, "old binary")
		assertApplyFile(t, filepath.Join(in.dir, name), name)
	})
	t.Run("aii replaced by hand", func(t *testing.T) {
		in, names := besideInstall(t, rig)
		tgt := targetForTest(t, in.exe)
		if err := os.WriteFile(in.exe, []byte("an operator's repair"), 0o755); err != nil {
			t.Fatal(err)
		}
		failedBoots(in)
		assertApplyFile(t, in.exe, "an operator's repair")
		for _, n := range names {
			assertApplyFile(t, filepath.Join(in.dir, n), n)
			if exists(besideBackup(tgt, n)) {
				t.Errorf("the update's state was retired and what it kept of %s was not", n)
			}
		}
		if exists(tgt.backup) || exists(tgt.record) {
			t.Error("the update's state was not retired")
		}
	})
}

// A healthy boot of the update retires everything the swap kept.
func TestAHealthyBootRetiresWhatTheUpdateKeptBesideAii(t *testing.T) {
	in, names := besideInstall(t, newHelperRig(t))
	tgt := targetForTest(t, in.exe)
	checkRollbackAt(in.dir, in.exe) // the update's first boot begins
	writeBootMarkerAt(in.dir, in.exe)
	assertApplyFile(t, in.exe, hostProgram())
	for _, name := range names {
		assertApplyFile(t, filepath.Join(in.dir, name), name)
		if exists(besideBackup(tgt, name)) {
			t.Errorf("the update booted healthy and what it kept of %s is still there", name)
		}
	}
	if exists(tgt.backup) || exists(tgt.record) {
		t.Error("the update booted healthy and its backup or record is still there")
	}
}

// A rollback cut off after the files beside aii are back and before aii is
// finds aii still the update's at the next boot and finishes; it does not
// read files that are already back as someone's change.
func TestARollbackCutOffBeforeAiiIsFinishedByTheNextBoot(t *testing.T) {
	in, names := besideInstall(t, newHelperRig(t))
	tgt := targetForTest(t, in.exe)
	checkRollbackAt(in.dir, in.exe)
	pend, _, err := readRecord(tgt.record)
	if err != nil {
		t.Fatal(err)
	}
	r, ok, err := recoveryOf(in.dir, in.exe)
	if !ok || err != nil {
		t.Fatalf("no recovery to drive: %v", err)
	}
	if err := r.restoreBeside(pend); err != nil { // the first half, and then the cut
		t.Fatal(err)
	}
	assertApplyFile(t, in.exe, hostProgram())
	if checkRollbackAt(in.dir, in.exe) == "" {
		t.Fatal("the next boot did not finish the rollback")
	}
	assertApplyFile(t, in.exe, "old binary")
	for _, name := range names {
		assertApplyFile(t, filepath.Join(in.dir, name), "previous "+name)
	}
}

// A RECORD FROM BEFORE UPDATES KEPT THESE FILES IS READ AS IT ALWAYS WAS:
// aii alone is rolled back, and nothing beside it is touched. And the new
// field is one an earlier build's reader passes over.
func TestARecordWithoutTheFilesBesideAiiRollsBackAiiAlone(t *testing.T) {
	in, names := besideInstall(t, newHelperRig(t))
	tgt := targetForTest(t, in.exe)
	raw, err := os.ReadFile(tgt.record)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["beside"]; !ok {
		t.Fatalf("the record does not carry the files beside aii under \"beside\": %s", raw)
	}
	// What an earlier build read: every field it knew, by the names it knew.
	var earlier struct {
		Attempts     int    `json:"attempts"`
		BackupSHA256 string `json:"backup_sha256"`
		NewSHA256    string `json:"new_sha256"`
		Owner        string `json:"owner,omitempty"`
	}
	if err := json.Unmarshal(raw, &earlier); err != nil || earlier.BackupSHA256 != sha256hex([]byte("old binary")) || earlier.NewSHA256 != sha256hex([]byte(hostProgram())) || earlier.Owner == "" {
		t.Fatalf("an earlier build's reader does not read this record as its own: %+v (%v)", earlier, err)
	}
	// And a record as an earlier build wrote it, read by this one.
	delete(fields, "beside")
	old, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tgt.record, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if failedBoots(in) == "" {
		t.Fatal("a record without the files beside aii did not roll aii back")
	}
	assertApplyFile(t, in.exe, "old binary")
	for _, name := range names {
		assertApplyFile(t, filepath.Join(in.dir, name), name)
	}
}

// AN UPDATE CUT OFF BETWEEN THE FILES BESIDE aii AND aii ITSELF leaves the
// aii that booted healthy beside the update's launcher and helper, with the
// update's record and backups. Whichever way that aii's next boots go, the
// files beside it are put back before the state is retired: by the healthy
// boot that retires it, and by a boot that reaches the rollback, which reads
// an aii that is still the swap's backup as a swap that never published and
// not as someone's repair.
func TestAnUpdateCutOffBeforeAiiPutsBackWhatItWroteBesideIt(t *testing.T) {
	rig := newHelperRig(t)
	platform := packagefmt.HostPlatform()
	cutOff := func(t *testing.T) (*helperInstall, []string) {
		t.Helper()
		in := rig.install(t, contractArchive(t, platform))
		names := installedBeside(platform)
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(in.dir, name), []byte("previous "+name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		previous := replaceStep
		t.Cleanup(func() { replaceStep = previous })
		type cut struct{}
		replaceStep = func(step string) {
			if step == "beside-installed" {
				panic(cut{})
			}
		}
		func() {
			defer func() {
				if _, ok := recover().(cut); !ok {
					t.Fatal("the apply was not cut off between the files beside aii and aii")
				}
			}()
			_ = in.apply()
		}()
		replaceStep = previous
		assertApplyFile(t, in.exe, "old binary")
		for _, name := range names {
			assertApplyFile(t, filepath.Join(in.dir, name), name)
		}
		return in, names
	}
	back := func(t *testing.T, in *helperInstall, names []string) {
		t.Helper()
		tgt := targetForTest(t, in.exe)
		assertApplyFile(t, in.exe, "old binary")
		for _, name := range names {
			assertApplyFile(t, filepath.Join(in.dir, name), "previous "+name)
			if exists(besideBackup(tgt, name)) {
				t.Errorf("the state was retired and what was kept of %s was not", name)
			}
		}
		if exists(tgt.backup) || exists(tgt.record) {
			t.Error("the cut-off update's backup or record is still there")
		}
	}
	t.Run("the next boot is healthy", func(t *testing.T) {
		in, names := cutOff(t)
		checkRollbackAt(in.dir, in.exe)
		writeBootMarkerAt(in.dir, in.exe)
		back(t, in, names)
	})
	t.Run("the next boots do not finish", func(t *testing.T) {
		in, names := cutOff(t)
		failedBoots(in)
		back(t, in, names)
	})
}
