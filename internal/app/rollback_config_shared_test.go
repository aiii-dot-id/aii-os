package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// AN IDENTITY THAT SHARES ITS PROGRAM IS MOVED BACK WITH IT, AND ITS
// CONFIGURATION GOES BACK TOO. It applied no update, so it has no rollback
// of its own: it keeps its file, once per version, at a boot under another
// identity's unsettled update; a start of an older program than the copy
// was kept under puts the copy back before the file is read; and a healthy
// boot with nothing unsettled drops the copy. Its own update, or none, is
// not this path's.
func TestAnIdentityThatSharesItsProgramGetsItsConfigurationBackWhenTheProgramGoesBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		raw, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			return "(none)"
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	anothers := &updates.AnothersUpdateError{Owner: "/home/a/data"}
	if !errors.Is(anothers, updates.ErrBootRecoveryPending) {
		t.Fatal("rig: another identity's unsettled update is not a reason to wait")
	}
	const asOldLeft, asNewWrote = `{"as":"0.2.0 left it"}`, `{"as":"0.2.1 wrote it","a_key_0_2_0_does_not_know":true}`

	// Nothing is kept with nothing unsettled.
	write(asOldLeft)
	keepConfigUnderUpdate(path, "0.2.1", nil)
	if got := keptUnderVersions(path); len(got) != 0 {
		t.Fatalf("a copy was kept with no update unsettled: %v", got)
	}

	// The boot of 0.2.1 under another identity's update keeps the file as
	// 0.2.0 left it; a second boot of it, after 0.2.1 has written the
	// file, does not write over the copy.
	keepConfigUnderUpdate(path, "0.2.1", anothers)
	write(asNewWrote)
	keepConfigUnderUpdate(path, "0.2.1", anothers)
	if got := read(path + configKeptUnder + "0.2.1"); got != asOldLeft {
		t.Fatalf("THE COPY IS NOT THE FILE AS THE PREVIOUS VERSION LEFT IT: %s", got)
	}

	// While the update stands, a healthy boot keeps the copy, and a start
	// of the same version puts nothing back.
	dropConfigKeptUnderSettled(path, "0.2.1", anothers)
	putBackConfigKept(path, "0.2.1", false)
	if read(path) != asNewWrote || read(path+configKeptUnder+"0.2.1") != asOldLeft {
		t.Fatalf("a start of the version the copy was kept under changed something: file %s, copy %s", read(path), read(path+configKeptUnder+"0.2.1"))
	}

	// THE OTHER IDENTITY'S ROLLBACK: the program is 0.2.0 again. Before the
	// file is read the copy goes back, and what 0.2.1 wrote is set aside.
	putBackConfigKept(path, "0.2.0", false)
	if read(path) != asOldLeft {
		t.Fatalf("THE OLDER PROGRAM WOULD READ THE FILE THE NEWER ONE WROTE: %s", read(path))
	}
	if read(path+configRolledBack) != asNewWrote || read(path+configKeptUnder+"0.2.1") != "(none)" {
		t.Fatalf("the newer file was not set aside, or the copy was left to be put back again: aside %s, copy %s", read(path+configRolledBack), read(path+configKeptUnder+"0.2.1"))
	}
	// A second start changes nothing.
	putBackConfigKept(path, "0.2.0", false)
	if read(path) != asOldLeft {
		t.Fatalf("a second start changed the file: %s", read(path))
	}

	// THE OTHER IDENTITY'S UPDATE SETTLED: a healthy boot with nothing
	// unsettled drops the copy, and a later start of an older program (an
	// operator's own reinstall) finds nothing of this to put back.
	write(asOldLeft)
	keepConfigUnderUpdate(path, "0.2.1", anothers)
	write(asNewWrote)
	dropConfigKeptUnderSettled(path, "0.2.1", nil)
	if got := keptUnderVersions(path); len(got) != 0 {
		t.Fatalf("a settled update left its copy: %v", got)
	}
	putBackConfigKept(path, "0.2.0", false)
	if read(path) != asNewWrote {
		t.Fatalf("a copy that was dropped was put back: %s", read(path))
	}

	// Copies of two versions newer than the program: the oldest is the one
	// put back, and the other does not stay to be put back at the next
	// start.
	for v, body := range map[string]string{"0.2.1": asOldLeft, "0.2.2": `{"as":"0.2.1 left it"}`} {
		if err := os.WriteFile(path+configKeptUnder+v, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	putBackConfigKept(path, "0.2.0", false)
	if read(path) != asOldLeft || len(keptUnderVersions(path)) != 0 {
		t.Fatalf("with two newer copies: file %s, copies left %v", read(path), keptUnderVersions(path))
	}
	// UNDER THE CLAIM. A second start of an identity still running the
	// newer version finds the claim held and changes nothing: the running
	// process would reload the swapped file and later save over it.
	write(asNewWrote)
	if err := os.WriteFile(path+configKeptUnder+"0.2.1", []byte(asOldLeft), 0o600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "data")
	if err := os.Mkdir(record, 0o700); err != nil {
		t.Fatal(err)
	}
	write(`{"identity":{"ledger_path":"` + strings.ReplaceAll(filepath.Join(record, "ledger.jsonl"), `\`, `\\`) + `"},"as":"0.2.1 wrote it"}`)
	running := read(path)
	held := func(string) (func(), error) { return nil, errors.New("held") }
	putBackUnderClaim(path, "0.2.0", held)
	if read(path) != running || read(path+configKeptUnder+"0.2.1") != asOldLeft {
		t.Fatalf("A SECOND START SWAPPED THE CONFIGURATION OF A RUNNING IDENTITY: file %s, copy %s", read(path), read(path+configKeptUnder+"0.2.1"))
	}
	var claimed []string
	free := func(d string) (func(), error) {
		claimed = append(claimed, "claim "+filepath.Base(d))
		return func() { claimed = append(claimed, "release") }, nil
	}
	putBackUnderClaim(path, "0.2.0", free)
	if read(path) != asOldLeft || strings.Join(claimed, ", ") != "claim data, release" {
		t.Fatalf("with the claim free the copy goes back under it: file %s, claim %v", read(path), claimed)
	}
	// A name that is not a version is not one of these: the copy versions
	// before 0.2.0 kept is never put back, and goes once settled.
	if err := os.WriteFile(path+configBeforeUpdate, []byte(asNewWrote), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := keptUnderVersions(path); len(got) != 0 {
		t.Fatalf("an earlier version's copy was read as one kept under a version: %v", got)
	}
	dropConfigKeptUnderSettled(path, "0.2.0", nil)
	if read(path+configBeforeUpdate) != "(none)" {
		t.Fatal("a settled update left the copy an earlier version kept")
	}
}

// THROUGH A BOOT, UNDER ANOTHER IDENTITY'S UNSETTLED UPDATE. The identity
// keeps its configuration and providers.json as the previous version left
// them and holds its plugin storage upgrades, and the healthy end of its
// boot keeps the copies while that update stands. Once it has settled, the
// running identity drops the copies at its next beat, and a boot that finds
// them standing drops them at its healthy end.
func TestAnIdentityBootedUnderAnothersUpdateKeepsItsConfigurationUntilThatUpdateSettles(t *testing.T) {
	record, backup := pendingUpdateBesideTheProgram(t, t.TempDir()) // another identity's, beside this test's program
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Sharing")
	cfg := safebootConfig(t, dir, "Sharing", keyPath, ledgerPath, dbPath)
	if err := os.WriteFile(cfg.SourcePath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	copyPath := cfg.SourcePath + configKeptUnder + Current()
	gone := func() bool { _, err := os.Stat(copyPath); return errors.Is(err, os.ErrNotExist) }
	providers := providerFilePath(cfg.SourcePath)
	providersBefore := readText(t, providers) // the fixture's, written beside the configuration
	providersCopy := providers + configKeptUnder + Current()
	providersGone := func() bool { _, err := os.Stat(providersCopy); return errors.Is(err, os.ErrNotExist) }

	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	if _, safe := a.SafeMode(); safe {
		stopApp(t, a)
		t.Fatal("the fixture booted SAFE, so this is not a healthy boot")
	}
	if gone() || readText(t, copyPath) != before {
		stopApp(t, a)
		t.Fatal("A BOOT UNDER ANOTHER IDENTITY'S UNSETTLED UPDATE KEPT NO COPY OF THE CONFIGURATION, or its healthy end dropped it")
	}
	if providersGone() || readText(t, providersCopy) != providersBefore {
		stopApp(t, a)
		t.Fatal("A BOOT UNDER ANOTHER IDENTITY'S UNSETTLED UPDATE KEPT NO COPY OF PROVIDERS.JSON as the previous version left it, or its healthy end dropped it")
	}
	opts := a.pluginOpts
	if opts == nil {
		stopApp(t, a)
		t.Fatal("the boot built no plugin options")
	}
	var anothers *updates.AnothersUpdateError
	if err := opts.StorageUpgradeReady(); !errors.As(err, &anothers) {
		stopApp(t, a)
		t.Fatalf("PLUGIN STORAGE WOULD BE UPGRADED UNDER AN UPDATE THAT CAN STILL MOVE THIS IDENTITY BACK: %v", err)
	}
	// The beat of the running identity's watch, while the update stands.
	a.dropSettledConfigCopies(cfg.SourcePath)
	if gone() || providersGone() {
		stopApp(t, a)
		t.Fatal("the running identity dropped its copy while the update was unsettled")
	}
	// The other identity's healthy boot retires its update.
	for _, p := range []string{record, backup} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := opts.StorageUpgradeReady(); err != nil {
		stopApp(t, a)
		t.Fatalf("plugin storage still waits after the update settled: %v", err)
	}
	a.dropSettledConfigCopies(cfg.SourcePath)
	stopApp(t, a)
	if !gone() || !providersGone() {
		t.Fatal("THE RUNNING IDENTITY KEPT ITS COPY AFTER THE UPDATE SETTLED: a later downgrade by hand would put it back over everything set since")
	}

	// A copy found standing by a boot with nothing unsettled goes at that
	// boot's healthy end.
	if err := os.WriteFile(copyPath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(providersCopy, []byte(providersBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	b := New(cfg)
	if err := startLiveForTest(b); err != nil {
		t.Fatal(err)
	}
	stopApp(t, b)
	if !gone() || !providersGone() {
		t.Fatal("a healthy boot with no update standing left the copy")
	}
}

// PROVIDERS.JSON GOES BACK WITH THE CONFIGURATION, BY THE SAME RULE. The
// owner's rollback puts it back (rollback_config.go, rollbackFiles); an
// identity that shares the program meets the same strict read in the
// older program, which refuses a field it does not know on a capability
// row and starts on no provider. So it is kept once per version beside
// the configuration, put back by an older program with the newer file set
// aside, dropped when the update settles, and left alone under a held
// claim.
func TestAnIdentityThatSharesItsProgramGetsItsProvidersFileBackWhenTheProgramGoesBack(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "data")
	if err := os.Mkdir(record, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"identity":{"ledger_path":"`+strings.ReplaceAll(filepath.Join(record, "ledger.jsonl"), `\`, `\\`)+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	providers := providerFilePath(path)
	copyPath := providers + configKeptUnder + "0.2.1"
	const previous = `{"providers":[{"name":"Hosted","api_type":"openai","url":"https://hosted.example/v1","default_model":"m","default":true}]}`
	if err := os.WriteFile(providers, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string {
		raw, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			return "(none)"
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	anothers := &updates.AnothersUpdateError{Owner: "/home/a/data"}

	// Kept once: the boot of 0.2.1 under another identity's update keeps
	// the file as 0.2.0 left it, and a second boot, after 0.2.1 wrote a
	// field 0.2.0 does not know, does not write over the copy.
	keepConfigUnderUpdate(path, "0.2.1", anothers)
	reg, err := loadProvidersFile(providers)
	if err != nil {
		t.Fatal(err)
	}
	reg.ModelCapabilities = map[string]modelCapability{"m": {ContextLength: 64000}}
	if _, err := saveProvidersFile(providers, reg); err != nil {
		t.Fatal(err)
	}
	newer := read(providers)
	if !strings.Contains(newer, `"context_length"`) {
		t.Fatalf("the newer file holds no field the previous version refuses, so this proves nothing: %s", newer)
	}
	keepConfigUnderUpdate(path, "0.2.1", anothers)
	if got := read(copyPath); got != previous {
		t.Fatalf("THE COPY OF PROVIDERS.JSON IS NOT THE FILE AS THE PREVIOUS VERSION LEFT IT: %s", got)
	}

	// Under a held claim, a start of the older program changes nothing.
	held := func(string) (func(), error) { return nil, errors.New("held") }
	putBackUnderClaim(path, "0.2.0", held)
	if read(providers) != newer || read(copyPath) != previous {
		t.Fatalf("A SECOND START SWAPPED THE PROVIDERS FILE OF A RUNNING IDENTITY: file %s, copy %s", read(providers), read(copyPath))
	}

	// Put back by the older program, with what 0.2.1 wrote set aside.
	free := func(string) (func(), error) { return func() {}, nil }
	putBackUnderClaim(path, "0.2.0", free)
	if read(providers) != previous {
		t.Fatalf("THE OLDER PROGRAM WOULD READ THE PROVIDERS FILE THE NEWER ONE WROTE: %s", read(providers))
	}
	if read(providers+configRolledBack) != newer || read(copyPath) != "(none)" {
		t.Fatalf("the newer providers.json was not set aside, or the copy was left to be put back again: aside %s, copy %s", read(providers+configRolledBack), read(copyPath))
	}

	// Dropped when the update settles.
	keepConfigUnderUpdate(path, "0.2.1", anothers)
	dropConfigKeptUnderSettled(path, "0.2.1", nil)
	if got := keptUnderVersions(providers); len(got) != 0 {
		t.Fatalf("a settled update left its copy of providers.json: %v", got)
	}
}
