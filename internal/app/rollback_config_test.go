package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// before is a configuration as a previous version left it: nothing in it
// that version did not declare.
const before = `{"prompt":{"recent_turns":20}}`

func writeConfigFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A ROLLBACK PUTS BACK THE CONFIGURATION THE PREVIOUS VERSION RAN WITH. A
// version reads its configuration strictly, and this one records
// memory.meaning_source by itself the first time a source is handed a
// text. The boot-health rollback restored the previous program and left
// the file, so the restored program refused its own configuration and
// the identity stayed down.
//
// Here the update's first boot keeps the file. The host then records a
// choice through the door it uses in a running identity, and the file
// holds the new key. A second boot of the same update does not take
// that file for the previous version's. The rollback puts back the very
// bytes the previous version left, and keeps the newer file beside them.
func TestARollbackPutsBackTheConfigurationThePreviousVersionRanWith(t *testing.T) {
	path := writeConfigFile(t)
	keepConfigBeforeUpdate(path, updates.ErrBootRecoveryPending)
	if got := readText(t, path+configBeforeUpdate); got != before {
		t.Fatalf("the update's first boot must keep the file as it found it: %q", got)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if err := a.recordMeaningChoice(meaningChoiceNone); err != nil {
		t.Fatalf("the host could not record its choice, so the file holds nothing new: %v", err)
	}
	newer := readText(t, path)
	if !strings.Contains(newer, `"meaning_source"`) {
		t.Fatalf("the host recorded its choice and the file does not hold the key, so this proves nothing: %s", newer)
	}

	keepConfigBeforeUpdate(path, updates.ErrBootRecoveryPending)
	if got := readText(t, path+configBeforeUpdate); got != before {
		t.Fatalf("a second boot of the update took the newer version's file for the previous one's: %q", got)
	}

	if err := restoreConfigBeforeUpdate(path); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); got != before {
		t.Fatalf("the restored program must read the bytes it left, and reads: %q", got)
	}
	if got := readText(t, path+configRolledBack); got != newer {
		t.Fatalf("what the newer version left must be kept beside the file: %q", got)
	}
	if _, err := os.Stat(path + configBeforeUpdate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the copy was put back and still stands: %v", err)
	}
}

// THE COPY IS MADE FOR AN UPDATE THAT IS NOT SETTLED, AND GOES WHEN IT IS.
// With no update in flight nothing is copied. A boot that settles the
// update removes the copy. A boot whose marker could not be written has
// not settled it, and the next start rolls back: the copy stays for it.
func TestTheCopyIsKeptOnlyWhileAnUpdateIsNotSettled(t *testing.T) {
	path := writeConfigFile(t)
	keepConfigBeforeUpdate(path, nil)
	if _, err := os.Stat(path + configBeforeUpdate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("with no update in flight there is nothing to roll back to, and a copy was made: %v", err)
	}
	keepConfigBeforeUpdate(path, updates.ErrBootRecoveryPending)
	dropConfigBeforeUpdate(path, updates.ErrBootRecoveryPending)
	if got := readText(t, path+configBeforeUpdate); got != before {
		t.Fatalf("an update that is not settled still needs its copy: %q", got)
	}
	dropConfigBeforeUpdate(path, nil)
	if _, err := os.Stat(path + configBeforeUpdate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the update is settled and its copy still stands: %v", err)
	}
	if got := readText(t, path); got != before {
		t.Fatalf("settling an update must not touch the file: %q", got)
	}
}

// A ROLLBACK WITH NO COPY LEAVES THE FILE, and one whose file was never
// changed sets nothing aside.
func TestARollbackWithNoCopyLeavesTheFileAndAnUnchangedFileIsNotSetAside(t *testing.T) {
	path := writeConfigFile(t)
	if err := restoreConfigBeforeUpdate(path); err != nil || readText(t, path) != before {
		t.Fatalf("with no copy the file is left as it is: %v", err)
	}
	keepConfigBeforeUpdate(path, updates.ErrBootRecoveryPending)
	if err := restoreConfigBeforeUpdate(path); err != nil || readText(t, path) != before {
		t.Fatalf("an unchanged file is put back as itself: %v", err)
	}
	if _, err := os.Stat(path + configRolledBack); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing was changed under the newer version, and a file was set aside: %v", err)
	}
}

// THE CONFIGURATION IS PUT BACK BEFORE THE RESTORED PROGRAM STARTS, and a
// boot with no rollback puts nothing back. A configuration that cannot be
// put back does not keep the restored program from starting: it is the
// program on disk whatever this process does.
func TestAfterRollbackPutsTheConfigurationBackBeforeTheRestoredProgramStarts(t *testing.T) {
	var order []string
	step := func(name string) func() { return func() { order = append(order, name) } }
	putBack := func() error { order = append(order, "configuration"); return nil }
	reexec := func() error { order = append(order, "start"); return nil }
	if err := afterRollback("/data/aii.previous", putBack, step("release"), reexec); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "configuration,release,start" {
		t.Fatalf("the order after a rollback was %s", got)
	}
	order = nil
	if err := afterRollback("", putBack, step("release"), reexec); err != nil || len(order) != 0 {
		t.Fatalf("a boot with no rollback did %v (%v)", order, err)
	}
	failing := func() error { return errors.New("the disk is full") }
	if err := afterRollback("/data/aii.previous", failing, step("release"), reexec); err != nil || strings.Join(order, ",") != "release,start" {
		t.Fatalf("a configuration that could not be put back must not keep the restored program from starting: %v %v", order, err)
	}
}

// armedUpdate leaves in dir what a swap leaves: the backup, the record
// with no boot counted, the marker retired. The record names no real
// program, so a start that did roll back would retire the state and not
// write over the test binary.
func armedUpdate(t *testing.T, dir string) {
	t.Helper()
	backup := []byte("the previous binary")
	sum := sha256.Sum256(backup)
	if err := os.WriteFile(filepath.Join(dir, "aii.previous"), backup, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"attempts":0,"backup_sha256":"` + hex.EncodeToString(sum[:]) + `","new_sha256":"` + strings.Repeat("0", 64) + `"}`
	if err := os.WriteFile(filepath.Join(dir, ".update_pending"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
}

// THROUGH A BOOT. The first boot of an update that then runs healthy has
// settled it by the time it is up, and no copy is left behind; a copy
// found standing from an earlier boot of the same update goes too. The
// file is the same bytes throughout: nothing at a boot rewrites it.
func TestAHealthyBootOfAnUpdateLeavesNoCopyBehind(t *testing.T) {
	for _, standing := range []bool{false, true} {
		dir := t.TempDir()
		keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Update")
		cfg := safebootConfig(t, dir, "Update", keyPath, ledgerPath, dbPath)
		if err := os.WriteFile(cfg.SourcePath, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}
		if standing {
			if err := os.WriteFile(cfg.SourcePath+configBeforeUpdate, []byte(before), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		armedUpdate(t, dir)
		a := New(cfg)
		if err := startLiveForTest(a); err != nil {
			t.Fatal(err)
		}
		if _, safe := a.SafeMode(); safe {
			stopApp(t, a)
			t.Fatal("the fixture booted SAFE, so this is not a healthy boot")
		}
		_, err := os.Stat(cfg.SourcePath + configBeforeUpdate)
		got := readText(t, cfg.SourcePath)
		stopApp(t, a)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("copy standing before the boot: %v. The update is settled and a copy of the configuration is left: %v", standing, err)
		}
		if !bytes.Equal([]byte(got), []byte(before)) {
			t.Fatalf("the boot rewrote the configuration: %q", got)
		}
	}
}

func TestABootThatLeavesTheRollbackArmedHasKeptTheConfiguration(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Update")
	buildPriorProjection(t, ledgerPath, dbPath)
	undeclaredColumn(t, dbPath)
	cfg := safebootConfig(t, dir, "Update", keyPath, ledgerPath, dbPath)
	if err := os.WriteFile(cfg.SourcePath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	armedUpdate(t, dir)
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if _, safe := a.SafeMode(); !safe || !updates.RollbackArmed(dir) {
		t.Fatalf("the fixture must boot SAFE with its rollback armed (safe %v, armed %v)", safe, updates.RollbackArmed(dir))
	}
	if got := readText(t, cfg.SourcePath+configBeforeUpdate); got != before {
		t.Fatalf("the boot that leaves a rollback armed must have kept the configuration for it: %q", got)
	}
}

// undeclaredColumn gives the database a column with data that this
// program's schema does not declare, as a newer version's would have.
func undeclaredColumn(t *testing.T, dbPath string) {
	t.Helper()
	st, err := store.NewForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 3, Rounds: 1}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	execAll(`ALTER TABLE turn_metrics ADD COLUMN future_tokens INTEGER`, `UPDATE turn_metrics SET future_tokens = 7`)(t, st.DB())
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

// A SAFE BOOT THAT COUNTS AS A BOOT SETTLES THE UPDATE TOO. A record that
// does not verify is not the program's fault: the boot is SAFE, the
// marker is written, the rollback is retired, and the copy goes with it.
func TestASafeBootThatSettlesTheUpdateDropsTheCopy(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Update")
	buildPriorProjection(t, ledgerPath, dbPath)
	tamperChain(t, keyPath, ledgerPath)
	cfg := safebootConfig(t, dir, "Update", keyPath, ledgerPath, dbPath)
	if err := os.WriteFile(cfg.SourcePath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	armedUpdate(t, dir)
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if _, safe := a.SafeMode(); !safe || updates.RollbackArmed(dir) {
		t.Fatalf("the fixture must boot SAFE with its rollback retired (safe %v, armed %v)", safe, updates.RollbackArmed(dir))
	}
	if _, err := os.Stat(cfg.SourcePath + configBeforeUpdate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the update is settled by a SAFE boot and its copy still stands: %v", err)
	}
}
