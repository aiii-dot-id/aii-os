package continuity

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

// restore_test.go — the restore's own parts, below the boot that performs
// one: the files a boot decides by, and what a source is held to before
// anything is staged. The journeys through real boots are internal/app's
// (restore_test.go there).

// fileIdentity is the identity of the file at path now, read through an
// open handle and closed at once (an open handle would block the rename
// on Windows). os.Stat will not do there: its FileInfo loads the file ID
// lazily, BY PATH, when SameFile first asks — so two of them for one path
// both name whatever stands there then, and a replacement is never seen.
// (*os.File).Stat reads the ID from the handle when it is taken.
func fileIdentity(t *testing.T, path string) os.FileInfo {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// WHAT A BOOT DECIDES BY IS PUBLISHED WHOLE OR NOT AT ALL. The journal, the
// request and the rest were written by truncating the real name and writing
// into it, so a crash in between left half a file a boot would then read. A
// file published by rename is a different file from the one it replaced.
func TestFilesABootDecidesByArePublishedByRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, RestoreJournalName)
	if err := os.WriteFile(path, []byte("what was here before"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := fileIdentity(t, path)
	if err := writeFileDurably(path, []byte(`{"whole":true}`+"\n")); err != nil {
		t.Fatal(err)
	}
	after := fileIdentity(t, path)
	if os.SameFile(before, after) {
		t.Fatal("THE REAL NAME WAS WRITTEN INTO: a crash mid-write leaves half a file under it")
	}
	if got, _ := os.ReadFile(path); string(got) != `{"whole":true}`+"\n" {
		t.Fatalf("published %q", got)
	}
	if runtime.GOOS != "windows" && after.Mode().Perm() != 0o600 {
		t.Errorf("published with mode %v, want 0600", after.Mode().Perm())
	}
	if ok, err := fileperm.IsRestrictedToOwner(path); err != nil || !ok {
		t.Errorf("published readable beyond its owner: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*")); len(left) != 0 {
		t.Errorf("a publication left its working file behind: %v", left)
	}
	// A publication that cannot happen leaves what was there, whole.
	if err := writeFileDurably(filepath.Join(dir, "no-such-dir", "x.json"), []byte("x")); err == nil {
		t.Error("a write into a directory that does not exist reported success")
	}
}

// Receipt-backed snapshots have an independent pre-replay mirror check.
// This exercises the other real caller: a set kept aside before any witness
// anchor, with a valid birth and signatures but no snapshot receipt. Stop at
// admission; do not request, install, or boot a restored identity.
func TestSchemaUpgradeSetAsideAdmissionPreservesAcknowledgment(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	prefix, err := os.ReadFile(cfg.Identity.LedgerPath)
	if err != nil {
		t.Fatal(err)
	}
	olderSeq := a.ledger.LastSeq()
	pin, err := a.store.PinAt(t.Context(), olderSeq, a.ledger.LastHash())
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Release()
	olderPath := filepath.Join(t.TempDir(), "older.db")
	if err := pin.BackupTo(t.Context(), olderPath); err != nil {
		t.Fatal(err)
	}
	pin.Release()
	olderDatabase, err := os.ReadFile(olderPath)
	if err != nil {
		t.Fatal(err)
	}
	appendFixtureEvent(t, a, keyPath, "proof_acknowledged_tail")
	want := a.ledger.LastSeq()
	// Closed here to read their bytes, so they are the test's now, not the
	// App's to close again at teardown.
	if err := a.ledger.Close(); err != nil {
		t.Fatal(err)
	}
	a.ledger = nil
	if err := a.store.Close(); err != nil {
		t.Fatal(err)
	}
	a.store = nil
	history, err := os.ReadFile(cfg.Identity.LedgerPath)
	if err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(cfg.Identity.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name             string
		database, record []byte
		through          uint64
		refuse           bool
	}{
		{name: "matching_set_control", database: database, record: history, through: want},
		// An intentionally older, internally consistent pair is not a
		// lost tail. Compare within the candidate, not with the live head.
		{name: "older_consistent_pair_is_allowed", database: olderDatabase, record: prefix, through: olderSeq},
		{name: "valid_prefix_behind_mirror", database: database, record: prefix, refuse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, staged := t.TempDir(), t.TempDir()
			for file, raw := range map[string][]byte{SnapshotDB: tc.database, "ledger.jsonl": tc.record} {
				if err := os.WriteFile(filepath.Join(source, file), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			through, err := a.continuity.stageAndProve(t.Context(), cfg, a.door.kp, source, nil,
				filepath.Join(staged, "set"), filepath.Join(staged, "proof"))
			if tc.refuse {
				if err == nil {
					t.Fatalf("set-aside admission accepted record %d despite the mirror acknowledging %d", through, want)
				}
			} else if err != nil || through != tc.through {
				t.Fatalf("consistent set failed admission: through=%d want=%d err=%v", through, tc.through, err)
			}
		})
	}
}

// AN INSTALL WHOSE STAGED FILE IS GONE IS MADE ONLY WHERE NOTHING BUT IT
// COULD HAVE PUT THE FILE THAT STANDS IN ITS PLACE (journalMade). That is
// the question that decides whether a journal is rolled forward or
// withdrawn, asked of the disk before anything moves.
func TestAnInstallIsMadeOnlyWhereNothingButItCouldHavePutTheFile(t *testing.T) {
	for _, tc := range []struct {
		what               string
		original, preserve bool   // the live file was there and went aside (preserve), or stays (copy)
		kind               string // of the move that keeps the original, when there was one
		aside, staged, put bool   // what is on disk now: the kept original, the staged file, the file in place
		made, performable  bool
	}{
		{what: "its original went aside and a file stands in its place", original: true, kind: MovePreserve, aside: true, put: true, made: true, performable: true},
		{what: "its original never went aside", original: true, kind: MovePreserve, put: true},
		{what: "nothing stood there and a file does now", put: true, made: true, performable: true},
		{what: "nothing stood there and nothing does"},
		{what: "a witness key that stays live was in its place", original: true, kind: MoveCopy, aside: true, put: true},
		{what: "its staged file is still there", original: true, kind: MovePreserve, staged: true, put: true, performable: true},
	} {
		t.Run(tc.what, func(t *testing.T) {
			dir := t.TempDir()
			aside, set := filepath.Join(dir, "set-aside", "x"), filepath.Join(dir, ".restore-staging-1", "set")
			live, kept, staged := filepath.Join(dir, "ledger.jsonl"), filepath.Join(aside, "ledger.jsonl"), filepath.Join(set, "ledger.jsonl")
			j := RestoreJournal{Aside: aside}
			if tc.original {
				j.Moves = append(j.Moves, RestoreMove{Kind: tc.kind, From: live, To: kept})
			}
			j.Moves = append(j.Moves, RestoreMove{Kind: MoveInstall, From: staged, To: live})
			for path, there := range map[string]bool{kept: tc.aside, staged: tc.staged, live: tc.put} {
				if !there {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			made, performable, err := journalMade(j)
			if err != nil {
				t.Fatal(err)
			}
			if got := made[len(made)-1]; got != tc.made || performable != tc.performable {
				t.Errorf("the install: made=%v performable=%v, want %v and %v", got, performable, tc.made, tc.performable)
			}
		})
	}
}
