package app

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// seed_record_test.go — the record of what the seeder last wrote
// (seeddoc.go): where it is kept, what it holds, when it is written, and
// that it decides nothing it cannot read.

// recordNames are the seeded docs' record names, in seededDocKinds' order.
var recordNames = []string{skillsFileName, methodFileName, overlayRecordName}

// THE RECORD IS KEPT IN THE DATA DIRECTORY, NAMES WHAT WAS WRITTEN, AND IS
// WRITTEN ONLY WHEN IT CHANGES. Each doc's record is one key, the key of
// the doc as seeded, in data/seeded-docs and not in the home beside the
// docs; a second start writes nothing.
func TestTheRecordNamesWhatTheSeederWroteAndIsWrittenOnlyWhenItChanges(t *testing.T) {
	for i, kind := range seededDocKinds() {
		t.Run(kind.name, func(t *testing.T) {
			a, home := editionHome(t)
			kind.seed(a)
			record := a.seedRecordPath(recordNames[i])
			if want := filepath.Join(home, "data", seedRecordDirName, recordNames[i]+seedRecordSuffix); record != want {
				t.Fatalf("the record is at %s, want %s", record, want)
			}
			if got, want := string(mustRead(t, record)), docSeedKey(kind.normalize, kind.current())+"\n"; got != want {
				t.Fatalf("the record holds %q, want the seeded doc's key %q", got, want)
			}
			old := time.Now().Add(-24 * time.Hour)
			if err := os.Chtimes(record, old, old); err != nil {
				t.Fatal(err)
			}
			kind.seed(a)
			if fi, err := os.Stat(record); err != nil || !fi.ModTime().Equal(old) {
				t.Fatalf("an unchanged start rewrote the record: %v", err)
			}
		})
	}
}

// A DOC SEEDED BEFORE THE RECORD EXISTED IS RECORDED AT ITS NEXT START,
// once, when it is the current version. From then its upgrades no longer
// rest on the answer keys.
func TestACurrentDocWithoutARecordIsRecorded(t *testing.T) {
	dir := t.TempDir()
	path, record := filepath.Join(dir, "doc.md"), filepath.Join(dir, "data", seedRecordDirName, "doc.md"+seedRecordSuffix)
	current := []byte("the doc\n")
	writeDoc(t, path, current)
	seedDoc(path, current, nil, []string{docSeedKey(nil, current)}, record, "[test] seed")
	if got := string(mustRead(t, record)); got != docSeedKey(nil, current)+"\n" {
		t.Fatalf("a current doc seeded before the record existed was not recorded: %q", got)
	}
}

// A DOC BEING REPLACED IS THE PLATFORM'S EITHER WAY. The record names
// both the version the doc holds and the one replacing it before the doc
// moves, and only the new one after: so a start that stops between the two
// writes leaves a record that names the doc whichever it holds. Here the
// doc cannot be written, so the start stops between them, and the next
// start, able to write, finishes the upgrade of a version no key names.
func TestADocBeingReplacedIsThePlatformsEitherWay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not keep a file from being made in it on Windows")
	}
	root := t.TempDir()
	docs := filepath.Join(root, "home")
	path, record := filepath.Join(docs, "doc.md"), filepath.Join(root, "data", seedRecordDirName, "doc.md"+seedRecordSuffix)
	branch, current := []byte("what a branch build wrote\n"), []byte("the current doc\n")
	if err := os.Mkdir(docs, 0o700); err != nil {
		t.Fatal(err)
	}
	seedDoc(path, branch, nil, []string{docSeedKey(nil, branch)}, record, "[test] seed")

	if err := os.Chmod(docs, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(docs, 0o700) })
	if f, err := os.CreateTemp(docs, "probe-"); err == nil {
		f.Close()
		os.Remove(f.Name())
		t.Skip("this account writes into a directory its mode closes")
	}
	seedDoc(path, current, nil, []string{docSeedKey(nil, current)}, record, "[test] seed")
	if got := mustRead(t, path); !bytes.Equal(got, branch) {
		t.Fatalf("fixture: the doc was written in a directory closed to writes: %q", got)
	}
	if got, want := string(mustRead(t, record)), docSeedKey(nil, branch)+"\n"+docSeedKey(nil, current)+"\n"; got != want {
		t.Fatalf("while the doc is replaced the record names both versions: got %q, want %q", got, want)
	}

	if err := os.Chmod(docs, 0o700); err != nil {
		t.Fatal(err)
	}
	seedDoc(path, current, nil, []string{docSeedKey(nil, current)}, record, "[test] seed")
	if got := mustRead(t, path); !bytes.Equal(got, current) {
		t.Fatalf("the interrupted upgrade did not finish at the next start: %q", got)
	}
	if got := string(mustRead(t, record)); got != docSeedKey(nil, current)+"\n" {
		t.Fatalf("after the upgrade the record names only the new version: %q", got)
	}
}

// A RECORD THAT CANNOT BE READ, OR HOLDS NO KEY, DECIDES NOTHING: the
// answer keys decide as they did before records were kept, an edit stays
// the identity's, and a shipped version upgrades.
func TestARecordThatCannotBeReadDecidesNothing(t *testing.T) {
	old, current := []byte("v1\n"), []byte("v2\n")
	shipped := []string{docSeedKey(nil, old), docSeedKey(nil, current)}
	for name, plant := range map[string]func(record string) error{
		"not a key": func(record string) error {
			return os.WriteFile(record, []byte("not a key\n"+strings.ToUpper(docSeedKey(nil, []byte("edited\n")))+"\n"), 0o600)
		},
		"a directory": func(record string) error { return os.Mkdir(record, 0o700) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path, record := filepath.Join(dir, "doc.md"), filepath.Join(dir, "doc.md"+seedRecordSuffix)
			if err := plant(record); err != nil {
				t.Fatal(err)
			}
			writeDoc(t, path, []byte("edited\n"))
			seedDoc(path, current, nil, shipped, record, "[test] seed")
			if got := mustRead(t, path); string(got) != "edited\n" {
				t.Fatalf("an unreadable record made an edit the platform's: %q", got)
			}
			writeDoc(t, path, old)
			seedDoc(path, current, nil, shipped, record, "[test] seed")
			if got := mustRead(t, path); !bytes.Equal(got, current) {
				t.Fatalf("a shipped version did not upgrade beside an unreadable record: %q", got)
			}
		})
	}
}

// AN APP WITH NO LEDGER KEEPS NO RECORD: it has no data directory, and a
// record derived from an empty ledger path would land in the working
// directory. The answer keys alone decide there.
func TestAnAppWithNoLedgerKeepsNoRecord(t *testing.T) {
	a := &App{cfg: &Config{SourcePath: filepath.Join(t.TempDir(), "config.json")}}
	if got := a.seedRecordPath(skillsFileName); got != "" {
		t.Fatalf("an App with no ledger keeps its record at %s", got)
	}
}

// THE IDENTITY'S OWN TOOLS DO NOT REACH THE RECORD. In the stock layout
// the data directory is kept whole, so a read or a write of the record is
// refused as substrate and the record stays as the seeder wrote it. In a
// layout that keeps its data in the home itself, where the directory
// cannot be kept whole, the record's directory is kept by name.
func TestTheIdentitysToolsDoNotReachTheRecord(t *testing.T) {
	app, home := stockSlotApp(t, nil)
	rel := filepath.Join("data", seedRecordDirName, overlayRecordName+seedRecordSuffix)
	before := mustRead(t, filepath.Join(home, rel))
	if len(before) == 0 {
		t.Fatal("the started identity's overlay README has no record")
	}
	if out := toolRead(app, rel); !strings.Contains(out, "access denied: protected path") || strings.Contains(out, string(before[:16])) {
		t.Fatalf("the identity's read of its seed record must be refused as substrate, got %q", out)
	}
	if out := toolWrite(app, rel, strings.Repeat("0", 64)+"\n"); !strings.Contains(out, "access denied: protected path") {
		t.Fatalf("the identity's write of its seed record must be refused as substrate, got %q", out)
	}
	if after := mustRead(t, filepath.Join(home, rel)); !bytes.Equal(after, before) {
		t.Fatalf("the record changed under the identity's write: %q", after)
	}

	inHome := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(inHome, "config.json")
	cfg.Identity = IdentityConfig{LedgerPath: filepath.Join(inHome, "ledger.jsonl"), KeyPath: filepath.Join(inHome, "identity.sec"), DBPath: filepath.Join(inHome, "aii.db")}
	found := false
	for _, o := range substrateObjects(*cfg, inHome) {
		if o.ID == "sub.data."+seedRecordDirName && o.Path == realPath(filepath.Join(inHome, seedRecordDirName)) {
			found = true
		}
	}
	if !found {
		t.Fatal("where the data is the home, the record's directory is not kept by name")
	}
}
