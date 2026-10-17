package app

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seed_embedded_test.go — the docs on a phone. The tools organ and
// SKILLS.md tell every identity that SKILLS.md and METHOD.md are at its
// root, and only the desktop start (Run) seeded them: the shells' start
// (StartEmbedded, and StartAuthenticatedEmbedded, which the mobile binding
// calls) went straight to the first-boot or live fork, so an identity on a
// phone was pointed at two files that were never there.

// AN EMBEDDED START LEAVES BOTH DOCS AT THE HOME'S ROOT, the identity's
// own read tool reads them there by the name it is given, and their seed
// records are kept in the data directory. The identity lives as the mobile
// binding lays it out: the container is the home and the working
// directory, the configuration at its root, the record in data/.
func TestAnEmbeddedStartSeedsTheDocsAtTheHomeRoot(t *testing.T) {
	for name, start := range map[string]func(*App) error{
		"StartEmbedded":              (*App).StartEmbedded,
		"StartAuthenticatedEmbedded": (*App).StartAuthenticatedEmbedded,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, filepath.Join(home, "data"), "Embedded")
			buildPriorProjection(t, ledgerPath, dbPath)
			t.Chdir(home)
			cfg := safebootConfig(t, home, "Embedded", keyPath, ledgerPath, dbPath)
			cfg.Tools.CWD = "."
			cfg.Dashboard.Host, cfg.Dashboard.Port, cfg.Dashboard.TLS = "127.0.0.1", 0, false
			cfg.Certificate.ServerURL = "none"
			cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
			a := New(cfg)
			if err := start(a); err != nil {
				stopApp(t, a)
				t.Fatal(err)
			}
			defer stopApp(t, a)

			for _, d := range []struct{ doc, phrase string }{{skillsFileName, skillsStampPrefix + BuildIdentity()}, {methodFileName, "OCCAM'S RAZOR"}} {
				doc, phrase := d.doc, d.phrase
				if _, err := os.Stat(filepath.Join(home, doc)); err != nil {
					t.Fatalf("an embedded start left no %s at the home's root: %v", doc, err)
				}
				if got := toolRead(a, doc); !strings.Contains(got, phrase) {
					t.Fatalf("the identity's read of %s is not the seeded doc: %.300s", doc, got)
				}
				if _, err := os.Stat(filepath.Join(home, "data", "seeded-docs", doc+".seedkey")); err != nil {
					t.Fatalf("an embedded start kept no record of the %s it seeded: %v", doc, err)
				}
			}
		})
	}
}

// A REFUSED SECOND START CHANGES NOTHING, THE DOCS AND THEIR RECORD
// INCLUDED. A second start of a running identity is refused at the claim
// "having changed nothing" (claimIdentity), and the docs were seeded before
// the claim: another build's second start — an older one, whose key does not
// name the running build's text — took the live doc for its own by the
// record the running build keeps, replaced it with its older text and
// rewrote the record, and only then was refused. The docs are seeded after
// the claim.
func TestARefusedSecondStartLeavesTheDocsAndTheirRecordAlone(t *testing.T) {
	a, home := stockSlotApp(t, nil)
	a.seedHomeDocs()
	doc := filepath.Join(home, skillsFileName)
	record := filepath.Join(home, "data", seedRecordDirName, skillsFileName+seedRecordSuffix)
	docBefore, recordBefore := mustRead(t, doc), mustRead(t, record)

	cfg := defaultConfig()
	cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = "data/identity.sec", "data/ledger.jsonl", "data/aii.db"
	cfg.SourcePath = filepath.Join(home, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = "."
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
	second := New(cfg)
	older := append(append([]byte(nil), skillsMD...), "\n<!-- an older build -->\n"...)
	kind := seededDocKinds()[0]
	template, shipped := *kind.template, *kind.shipped
	var err error
	func() {
		defer func() { *kind.template, *kind.shipped = template, shipped }()
		*kind.template = older
		*kind.shipped = []string{docSeedKey(normalizeSkillsStamp, older)}
		err = second.StartEmbedded()
	}()
	if err == nil {
		stopApp(t, second)
		t.Fatal("a second start of a running identity was not refused")
	}
	if !errors.Is(err, errIdentityRunning) {
		t.Fatalf("the second start was refused for another reason: %v", err)
	}
	if got := mustRead(t, doc); !bytes.Equal(got, docBefore) {
		t.Errorf("a refused second start rewrote the running identity's SKILLS.md (it now holds the older build's text: %v)", bytes.Contains(got, []byte("an older build")))
	}
	if got := mustRead(t, record); !bytes.Equal(got, recordBefore) {
		t.Errorf("a refused second start rewrote the record: %q", got)
	}
	absent(t, doc+sidecarSuffix)
}

// A FIRST BOOT SEEDS THE DOCS TOO, once it holds the directory and before
// its ceremony: the identity being born reads them.
func TestAFirstBootSeedsTheDocsAtTheHomeRoot(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(home, "config.json")
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(home, "data", "identity.sec"), LedgerPath: filepath.Join(home, "data", "ledger.jsonl"), DBPath: filepath.Join(home, "data", "aii.db")}
	cfg.Tools.CWD = "."
	cfg.Dashboard.Host, cfg.Dashboard.Port, cfg.Dashboard.TLS = "127.0.0.1", 0, false
	cfg.Certificate.ServerURL = "none"
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
	refuse := httptest.NewServer(http.NotFoundHandler()) // the founding servers refuse at once
	t.Cleanup(refuse.Close)
	cfg.Genesis = GenesisConfig{ServerURL: refuse.URL, FirewallURL: refuse.URL, BootstrapURL: refuse.URL}
	a := New(cfg)
	if err := a.StartEmbedded(); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	defer stopApp(t, a)
	for _, doc := range []string{skillsFileName, methodFileName} {
		if _, err := os.Stat(filepath.Join(home, doc)); err != nil {
			t.Errorf("a first boot left no %s at the home's root: %v", doc, err)
		}
		if _, err := os.Stat(filepath.Join(home, "data", seedRecordDirName, doc+seedRecordSuffix)); err != nil {
			t.Errorf("a first boot kept no record of the %s it seeded: %v", doc, err)
		}
	}
}
