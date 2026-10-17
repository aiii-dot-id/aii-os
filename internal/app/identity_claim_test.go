package app

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

func TestASecondStartOfARunningIdentityIsRefusedAndChangesNothing(t *testing.T) {
	dir := t.TempDir()
	key, path, db := birthFixture(t, dir, "Claimed")
	cfg := safebootConfig(t, dir, "Claimed", key, path, db)
	first := New(cfg)
	if err := startLiveForTest(first); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, first) })

	request := filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), continuity.RestoreRequestName)
	if err := os.WriteFile(request, []byte("a restore someone asked for"), 0o600); err != nil {
		t.Fatal(err)
	}
	var record, backup string
	if hostcap.Can(hostcap.SelfReplace).Available {
		record, backup = pendingUpdateBesideTheProgram(t, dir)
	}
	if err := os.Remove(filepath.Join(dir, ".boot_completed")); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		got := map[string]string{}
		for _, p := range []string{request, record, backup, path, db, filepath.Join(dir, continuity.RestoreFailedName)} {
			if p == "" {
				continue
			}
			b, err := os.ReadFile(p)
			if errors.Is(err, os.ErrNotExist) {
				got[p] = "<absent>"
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			got[p] = string(b)
		}
		return got
	}
	before := snapshot()

	second := New(cfg)
	t.Cleanup(func() { stopApp(t, second) })
	err := second.startLive()
	if !errors.Is(err, errIdentityRunning) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("a second start of a running identity must be refused, naming its directory: %v", err)
	}
	for p, want := range before {
		if got := snapshot()[p]; got != want {
			t.Errorf("THE REFUSED START CHANGED %s:\n got %q\nwant %q", filepath.Base(p), got, want)
		}
	}
	if record != "" {
		var p struct{ Attempts int }
		if raw, err := os.ReadFile(record); err != nil || json.Unmarshal(raw, &p) != nil || p.Attempts != 0 {
			t.Errorf("the update's record must stay at attempt 0: %+v %v", p, err)
		}
		_ = os.Remove(record)
		_ = os.Remove(backup)
	}
	if err := os.Remove(request); err != nil {
		t.Fatal(err)
	}

	// The refused start held nothing, and the claim goes with the process.
	stopApp(t, first)
	again := New(cfg)
	if err := startLiveForTest(again); err != nil {
		t.Fatalf("once the running process stopped, the identity must start: %v", err)
	}
	stopApp(t, again)
}

// pendingUpdateBesideTheProgram writes, beside this test's own program, an
// update record of the identity at dir at attempt 0 and its backup — what
// a finished swap leaves, named as internal/updates names a bare binary's
// (targetAt). Its backup hash is one no backup has, so no start can ever
// restore it over the test binary: a rollback refuses a backup that does
// not hash to its record.
func pendingUpdateBesideTheProgram(t *testing.T, dir string) (record, backup string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		t.Fatal(err)
	}
	owner, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if owner, err = filepath.Abs(owner); err != nil {
		t.Fatal(err)
	}
	bin, base := filepath.Split(exe)
	record = filepath.Join(bin, "."+base+".update.json")
	backup = filepath.Join(bin, "."+base+".previous")
	raw, err := json.Marshal(map[string]any{"attempts": 0, "backup_sha256": "no backup hashes to this", "new_sha256": "nor to this", "owner": owner})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, raw, 0o644); err != nil {
		t.Skipf("cannot write beside this test's program: %v", err)
	}
	t.Cleanup(func() { os.Remove(record) })
	if err := os.WriteFile(backup, []byte("the image the swap displaced"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(backup) })
	return record, backup
}

// A START THAT FAILS PARTWAY GIVES BACK WHAT IT TOOK. The claim is taken
// first; a start that then fails (here, at the identity key, on a home that
// holds no record: with a record the start stops at the page that puts the
// key back, and keeps the claim that page is served under) releases it
// after its resources close, so the next start is not refused by a process
// that is not running the identity. A live start inside a first boot — a
// birth's, a new-machine restore's — took nothing: when it fails, the
// first boot that still serves the directory keeps the claim.
func TestAFailedStartReleasesTheIdentity(t *testing.T) {
	dir := t.TempDir()
	_, path, db := birthFixture(t, dir, "Failing")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	cfg := safebootConfig(t, dir, "Failing", filepath.Join(dir, "no-such-key.sec"), path, db)
	a := New(cfg)
	t.Cleanup(func() { stopApp(t, a) })
	if err := a.startLive(); err == nil || errors.Is(err, errIdentityRunning) {
		t.Fatalf("the start must fail at the missing key: %v", err)
	}
	next := &App{}
	if _, err := next.claimIdentity(dir); err != nil {
		t.Fatalf("a failed start kept the identity: %v", err)
	}
	next.releaseIdentityClaim()

	firstboot := New(cfg)
	t.Cleanup(func() { stopApp(t, firstboot) })
	if _, err := firstboot.claimIdentity(dir); err != nil { // as startFirstboot does
		t.Fatal(err)
	}
	if err := firstboot.startLive(); err == nil || errors.Is(err, errIdentityRunning) {
		t.Fatalf("the birth's start must fail at the missing key: %v", err)
	}
	if _, err := (&App{}).claimIdentity(dir); !errors.Is(err, errIdentityRunning) {
		t.Fatalf("a failed start inside a first boot gave the first boot's claim away: %v", err)
	}
	stopApp(t, firstboot)
	if _, err := next.claimIdentity(dir); err != nil {
		t.Fatalf("the first boot's stop kept the identity: %v", err)
	}
	next.releaseIdentityClaim()
}

// THE HAND-OVER FINDS THE IDENTITY FREE. After a rollback the restored
// binary starts this identity — on Windows as another process begun while
// this one still exists — so the claim is released before the re-exec, on
// every platform; and a re-exec that fails afterwards still fails the boot.
func TestAfterRollbackReleasesTheIdentityBeforeTheHandOver(t *testing.T) {
	for _, fails := range []bool{false, true} {
		dir := t.TempDir()
		a := &App{}
		if _, err := a.claimIdentity(dir); err != nil {
			t.Fatal(err)
		}
		reexec := func() error {
			successor := &App{}
			if _, err := successor.claimIdentity(dir); err != nil {
				t.Errorf("the restored binary's start was refused by the process handing over: %v", err)
				return nil
			}
			successor.releaseIdentityClaim()
			if fails {
				return errors.New("exec unavailable")
			}
			return nil
		}
		err := afterRollback("/data/aii.previous", func() error { return nil }, a.releaseIdentityClaim, reexec)
		if fails != (err != nil) {
			t.Errorf("re-exec failing=%v: the boot returned %v", fails, err)
		}
		a.releaseIdentityClaim()
	}
	// No rollback: nothing is released and nothing re-executed.
	dir := t.TempDir()
	a := &App{}
	if _, err := a.claimIdentity(dir); err != nil {
		t.Fatal(err)
	}
	defer a.releaseIdentityClaim()
	if err := afterRollback("", func() error { return nil }, a.releaseIdentityClaim, func() error { t.Error("re-exec without a rollback"); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := (&App{}).claimIdentity(dir); !errors.Is(err, errIdentityRunning) {
		t.Fatalf("a boot with no rollback must keep its claim: %v", err)
	}
}

// A SECOND FIRST BOOT OF ONE DIRECTORY WRITES NOTHING. Two first boots of
// one empty directory each served the birth: each mints a key and a record,
// and the second's key could replace the first's under a record it did not
// sign; the new-machine restore runs on the same page. A first boot claims
// the directory as a live start does, so the second is refused and the
// directory is exactly as it was — no key, no record, no dashboard
// credentials. Once the first stops, a first boot proceeds.
func TestASecondFirstBootOfOneDirectoryIsRefusedAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	refuse := httptest.NewServer(http.NotFoundHandler()) // genesis refuses at once: no retry backoff
	t.Cleanup(refuse.Close)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Logs.Dir = filepath.Join(dir, "log") // beside the rig's data, not in the package directory
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.sec")
	cfg.Identity.LedgerPath = filepath.Join(dir, "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(dir, "aii.db")
	cfg.Tools.CWD = dir
	cfg.Dashboard.Port = 0
	cfg.Genesis = GenesisConfig{ServerURL: refuse.URL, FirewallURL: refuse.URL, BootstrapURL: refuse.URL}

	first := New(cfg)
	if err := first.StartEmbedded(); err != nil {
		stopApp(t, first)
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, first) })
	if first.live.Load() {
		t.Fatal("the fixture was to boot FIRSTBOOT")
	}
	tree := func() map[string]string {
		got := map[string]string{}
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() && p == cfg.Logs.Dir {
				// The first boot's own log: both starts share this
				// process's log stream, so a line of the refused one
				// lands in it here, as it would not between two
				// processes. That a refused start leaves the log
				// alone is held by
				// TestARefusedSecondStartLeavesTheLogAndTheDocumentsAlone.
				return filepath.SkipDir
			}
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			got[p] = string(b)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	before := tree()

	second := New(cfg)
	t.Cleanup(func() { stopApp(t, second) })
	err := second.StartEmbedded()
	if !errors.Is(err, errIdentityRunning) || !strings.Contains(err.Error(), dir) {
		t.Errorf("a second first boot of the directory must be refused, naming it: %v", err)
	}
	// No page serves its birth or new-machine restore: those are what
	// write the key and the record.
	if url := second.DashboardURL(); url != "" {
		t.Errorf("the refused first boot serves the birth page at %s", url)
	}
	if after := tree(); !maps.Equal(after, before) {
		t.Errorf("THE REFUSED FIRST BOOT WROTE INTO THE DIRECTORY:\n got %d files %v\nwant %d files %v", len(after), keys(after), len(before), keys(before))
	}
	for _, p := range []string{cfg.Identity.KeyPath, cfg.Identity.LedgerPath} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after two first boots and no birth: %v", filepath.Base(p), err)
		}
	}

	stopApp(t, first)
	again := New(cfg)
	if err := again.StartEmbedded(); err != nil {
		t.Fatalf("once the first boot stopped, a first boot must proceed: %v", err)
	}
	stopApp(t, again)
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, filepath.Base(k))
	}
	slices.Sort(out)
	return out
}
