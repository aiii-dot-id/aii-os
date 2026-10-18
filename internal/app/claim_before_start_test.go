package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// A REFUSED SECOND START LEAVES THE RUNNING IDENTITY'S LOG AND DOCUMENTS
// ALONE. Run installed the log tee, which renames the live log, and seeded
// the two documents, and only then reached the claim inside the start: a
// second start of a running identity was refused with the running
// process's live log already renamed under it. The claim is now the first
// thing a start takes, and a start refused it has done nothing else.
func TestARefusedSecondStartLeavesTheLogAndTheDocumentsAlone(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "data", "identity.sec")
	cfg.Identity.LedgerPath = filepath.Join(dir, "data", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(dir, "data", "aii.db")
	cfg.Logs.Dir = filepath.Join(dir, "log")
	live := filepath.Join(cfg.Logs.Dir, logsink.LiveName)
	const running = "a line of the running identity\n"
	if err := os.MkdirAll(cfg.Logs.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte(running), 0o640); err != nil {
		t.Fatal(err)
	}
	logFiles := func() []string {
		entries, err := os.ReadDir(cfg.Logs.Dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}

	// The running identity: it holds the claim on the record's directory.
	first := New(cfg)
	held, err := first.claimIdentity(filepath.Dir(cfg.Identity.LedgerPath))
	if err != nil {
		t.Fatal(err)
	}

	second := New(cfg)
	if _, err := second.prepareStart(); !errors.Is(err, errIdentityRunning) {
		t.Fatalf("a second start of a running identity must be refused: %v", err)
	}
	if got, err := os.ReadFile(live); err != nil || string(got) != running || len(logFiles()) != 1 || second.logSink != nil {
		t.Fatalf("THE REFUSED START TOOK THE RUNNING IDENTITY'S LIVE LOG: it holds %q (%v), the directory %v", got, err, logFiles())
	}
	for _, doc := range []string{second.skillsPath(), second.seededDocPath(methodFileName)} {
		if _, err := os.Stat(doc); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("THE REFUSED START SEEDED %s: %v", filepath.Base(doc), err)
		}
	}

	// Once the first has stopped, a start does all of it: the earlier
	// log is set aside under another name. Documents wait for the chosen
	// start, after the live path has allowed any rollback to finish.
	held()
	third := New(cfg)
	release, err := third.prepareStart()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		third.logSink.Close()
		release()
	})
	if len(logFiles()) != 2 {
		t.Fatalf("the start that holds the claim did not set the earlier log aside: %v", logFiles())
	}
	for _, doc := range []string{third.skillsPath(), third.seededDocPath(methodFileName)} {
		if _, err := os.Stat(doc); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("preparation seeded %s before the rollback decision: %v", filepath.Base(doc), err)
		}
	}
	third.seedHomeDocs() // the chosen start, after any rollback, owns this step
	for _, doc := range []string{third.skillsPath(), third.seededDocPath(methodFileName)} {
		if _, err := os.Stat(doc); err != nil {
			t.Fatalf("the start that holds the claim did not seed %s: %v", filepath.Base(doc), err)
		}
	}
}

// THE EMBEDDED START PREPARES AS RUN DOES. StartEmbedded, the phone shells'
// entry, went straight to the choice of FIRSTBOOT or LIVE: no log tee, no
// boot lines, no seeded documents. The tools verb tells every identity
// that SKILLS.md and METHOD.md are at their root, and on a phone they were
// not; the Logs page had no file to show.
func TestTheEmbeddedStartSeedsTheDocumentsAndKeepsALog(t *testing.T) {
	dir := t.TempDir()
	refuse := httptest.NewServer(http.NotFoundHandler()) // genesis refuses at once: no retry backoff
	t.Cleanup(refuse.Close)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.sec")
	cfg.Identity.LedgerPath = filepath.Join(dir, "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(dir, "aii.db")
	cfg.Logs.Dir = filepath.Join(dir, "log")
	cfg.Tools.CWD = dir
	cfg.Dashboard.Port = 0
	cfg.Genesis = GenesisConfig{ServerURL: refuse.URL, FirewallURL: refuse.URL, BootstrapURL: refuse.URL}

	a := New(cfg)
	t.Cleanup(func() { stopApp(t, a) })
	if err := a.StartEmbedded(); err != nil {
		t.Fatal(err)
	}
	for _, doc := range []string{a.skillsPath(), a.seededDocPath(methodFileName)} {
		if _, err := os.Stat(doc); err != nil {
			t.Errorf("THE EMBEDDED START DID NOT SEED %s, which the tools verb says is at the identity's root: %v", filepath.Base(doc), err)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Logs.Dir, logsink.LiveName)); err != nil {
		t.Errorf("the embedded start keeps no log file: %v", err)
	}
	if files, err := a.listLogs(); err != nil || len(files) == 0 {
		t.Errorf("the Logs page of an embedded start has nothing to show: %v %v", files, err)
	}
}
