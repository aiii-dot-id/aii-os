package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// A CONFIGURATION THE NEW PROGRAM REFUSES TWICE IS AN UPDATE THAT FAILED TO
// BOOT, AND ONE IT THEN LOADS IS NOT. The start loaded its configuration
// and ended on a refusal, before the rollback check: an update whose
// program refused the file its predecessor ran on died at every start, the
// previous program beside it and never used. The first refused start of an
// unsettled update is now noted; the second has the updater restore the
// previous program, and no refused start counts a boot. A start that loads
// the file forgets the note, so an operator who corrects the key keeps the
// update. What a refused start says is held to the updater's record: no
// promise that the update stands once a boot is counted against it, and a
// restore that did not complete says what stands.
func TestAStartThatRefusesItsConfigurationIsNotedThenRolledBack(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(t.TempDir(), "record")
	if err := os.Mkdir(record, 0o700); err != nil {
		t.Fatal(err)
	}
	asJSON := func(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }
	path := filepath.Join(dir, "config.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A file the loader refuses (a key it does not know) still says where
	// the record is.
	write(`{"identity":{"ledger_path":"` + asJSON(filepath.Join(record, "ledger.jsonl")) + `"},"no_such_key":true}`)
	if _, err := ReadConfig(path); err == nil {
		t.Fatal("rig: the loader took the file this test means it to refuse")
	}
	noted := func() bool { _, err := os.Stat(path + configRefusedOnce); return err == nil }

	var asked, acts []string
	// The updater as it answers an unsettled update asked to restore
	// without a boot: it restores, or (restores false) its restore does
	// not complete. It counts nothing either way; counted is the boots an
	// earlier start of the update left in its record.
	counted, restores := 0, true
	check := func(dataDir string) string {
		asked = append(asked, dataDir)
		if !restores {
			return ""
		}
		return "/opt/aii/aii"
	}
	armed := func(string) bool { return counted >= 1 }
	pending := func(string) error { return updates.ErrBootRecoveryPending }
	settled := func(string) error { return nil }
	putBack := func() error { acts = append(acts, "put back"); return nil }
	reexec := func() error { acts = append(acts, "hand over"); return nil }
	claim := func(d string) (func(), error) {
		acts = append(acts, "claim "+filepath.Base(d))
		return func() { acts = append(acts, "release") }, nil
	}
	reset := func() { asked, acts, counted, restores = nil, nil, 0, true }

	// No update of this identity's is unsettled: the operator's own typo.
	// Nothing is noted and the updater is not asked, however often.
	for i := 0; i < 2; i++ {
		rolled, said, err := refusedConfigRollback(path, claim, settled, armed, check, putBack, reexec)
		if rolled || said != "" || err != nil || len(asked) != 0 || noted() {
			t.Fatalf("with no update unsettled a refused start must change nothing: rolled=%v said=%q err=%v asked=%v noted=%v", rolled, said, err, asked, noted())
		}
	}

	// An unsettled update, first refused start: noted, and said to be; the
	// updater is not asked, so nothing is counted against the update yet,
	// and only then is the operator promised that the update stands.
	reset()
	rolled, said, err := refusedConfigRollback(path, claim, pending, armed, check, putBack, reexec)
	if rolled || said != refusedStartNoted || err != nil || !noted() || len(asked) != 0 || strings.Join(acts, ", ") != "claim record, release" {
		t.Fatalf("THE FIRST REFUSED START WAS NOT ONLY NOTED: rolled=%v said=%q err=%v noted=%v asked=%v acts=%v", rolled, said, err, noted(), asked, acts)
	}

	// The operator corrects the key and the file loads: the note goes, and
	// a later refused start is the first again. The update stands.
	ForgetRefusedStart(path)
	if noted() {
		t.Fatal("A START THAT LOADED THE FILE LEFT THE REFUSAL NOTED: the next refused start would roll a good release back")
	}

	// A BOOT OF THE UPDATE IS ALREADY COUNTED (a start that loaded and died
	// before it was healthy): the next start is rolled back by the live
	// boot's own check whatever the file says, so the first refused start
	// does not promise that correcting the key keeps the update.
	reset()
	counted = 1
	rolled, said, err = refusedConfigRollback(path, claim, pending, armed, check, putBack, reexec)
	if rolled || err != nil || !noted() || len(asked) != 0 || said != refusedStartArmed || strings.Contains(said, "the update stands") {
		t.Fatalf("A REFUSED START PROMISED AN UPDATE THE RECORD ALREADY COUNTS A BOOT AGAINST: rolled=%v said=%q err=%v noted=%v asked=%v", rolled, said, err, noted(), asked)
	}
	ForgetRefusedStart(path)
	reset()
	if rolled, said, _ := refusedConfigRollback(path, claim, pending, armed, check, putBack, reexec); rolled || said != refusedStartNoted || len(asked) != 0 {
		t.Fatalf("after a start that loaded, a refused start must be the first again: rolled=%v said=%q asked=%v", rolled, said, asked)
	}

	// THE SECOND REFUSED START, AND THE RESTORE DOES NOT COMPLETE. Nothing
	// was counted, and that is what is said: correcting the key keeps the
	// update. The note stays, so a refused start tries again, and the
	// updater is asked once.
	reset()
	restores = false
	rolled, said, err = refusedConfigRollback(path, claim, pending, armed, check, putBack, reexec)
	if rolled || err != nil || said != refusedTwiceIdle || !noted() || len(asked) != 1 || strings.Join(acts, ", ") != "claim record, release" {
		t.Fatalf("A RESTORE THAT DID NOT COMPLETE WAS NOT SAID AS IT STANDS, or lost the note: rolled=%v said=%q err=%v noted=%v asked=%v acts=%v", rolled, said, err, noted(), asked, acts)
	}
	// The same over a boot an earlier start of the update counted: that
	// count is the live boot's to act on, and the line says so in place
	// of the promise.
	asked, acts = nil, nil
	counted = 1
	if rolled, said, _ := refusedConfigRollback(path, claim, pending, armed, check, putBack, reexec); rolled || said != refusedTwiceArmed || strings.Contains(said, "the update stands") || len(asked) != 1 || !noted() {
		t.Fatalf("a second refused start over a counted boot whose restore does not complete: rolled=%v said=%q asked=%v noted=%v", rolled, said, asked, noted())
	}

	// The second refused start in a row: the updater restores, the
	// configuration is put back, the claim released, and the restored
	// program handed the start. The note is gone.
	reset()
	rolled, said, err = refusedConfigRollback(path, claim, pending, armed, check, putBack, reexec)
	if !rolled || said != "" || err != nil || strings.Join(acts, ", ") != "claim record, put back, release, hand over" {
		t.Fatalf("the second refused start must roll back: rolled=%v said=%q err=%v acts=%v", rolled, said, err, acts)
	}
	if len(asked) != 1 || asked[0] != record || noted() {
		t.Fatalf("the updater must be asked once, for the record's own directory: asked=%v noted=%v", asked, noted())
	}

	// A hand-over that fails is said, and the start is not called rolled back.
	if err := os.WriteFile(path+configRefusedOnce, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	reset()
	failed := errors.New("exec failed")
	if rolled, _, err := refusedConfigRollback(path, claim, pending, armed, check, putBack, func() error { return failed }); rolled || !errors.Is(err, failed) {
		t.Fatalf("a failed hand-over: rolled=%v err=%v", rolled, err)
	}

	// An identity already running from the directory holds the claim: a
	// second start asks the updater nothing and notes nothing.
	_ = os.Remove(path + configRefusedOnce)
	reset()
	held := func(string) (func(), error) { return nil, errors.New("held") }
	if rolled, said, err := refusedConfigRollback(path, held, pending, armed, check, putBack, reexec); rolled || said != "" || err != nil || len(asked) != 0 || len(acts) != 0 || noted() {
		t.Fatalf("A SECOND START SETTLED THE UPDATE OF A RUNNING IDENTITY: rolled=%v said=%q err=%v asked=%v acts=%v", rolled, said, err, asked, acts)
	}

	// A file that does not read at all has its record where a
	// configuration without the key puts it; a directory that is not there
	// has no update to settle and is not made.
	write(`{"identity":{"ledger_path":`)
	if got, want := recordDirOfRefusedConfig(path), filepath.Dir(defaultConfig().Identity.LedgerPath); got != want {
		t.Fatalf("a file that does not read: the record's directory is %q, want %q", got, want)
	}
	// A refusal that cannot be noted is said, and the updater is not
	// asked: here the file's own directory is not there.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, filepath.Dir(defaultConfig().Identity.LedgerPath)), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	reset()
	if rolled, said, err := refusedConfigRollback(filepath.Join(home, "gone", "config.json"), claim, pending, armed, check, putBack, reexec); rolled || said != "" || err == nil || len(asked) != 0 {
		t.Fatalf("A REFUSAL THAT COULD NOT BE NOTED WAS NOT SAID: rolled=%v said=%q err=%v asked=%v", rolled, said, err, asked)
	}

	t.Chdir(t.TempDir())
	reset()
	if rolled, said, err := refusedConfigRollback(path, claim, pending, armed, check, putBack, reexec); rolled || said != "" || err != nil || len(asked) != 0 || len(acts) != 0 {
		t.Fatalf("a home with no record directory: rolled=%v said=%q err=%v asked=%v acts=%v", rolled, said, err, asked, acts)
	}
	if _, err := os.Stat(filepath.Dir(defaultConfig().Identity.LedgerPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused start made the record's directory: %v", err)
	}
}
