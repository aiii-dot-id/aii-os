package store

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func addTurns(t *testing.T, s *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.AddConversationTurn("operator", "a turn the record never holds"); err != nil {
			t.Fatal(err)
		}
	}
}

func dbPathOf(t *testing.T, s *Store) string {
	t.Helper()
	var seq int
	var name, file string
	if err := s.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

// .
// .
func noReaderLeft(t *testing.T, s *Store, why string) {
	t.Helper()
	var busy, logged, checkpointed int
	if err := s.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logged, &checkpointed); err != nil {
		t.Fatal(err)
	}
	if busy != 0 {
		t.Fatalf("%s: a read snapshot is still held — the write-ahead log cannot be reset", why)
	}
}

// .
// .
// .
func TestACopyHoldsWhatIsStillInTheWriteAheadLog(t *testing.T) {
	s := testStore(t)
	addTurns(t, s, 7)
	path := dbPathOf(t, s)
	if st, err := os.Stat(path + "-wal"); err != nil || st.Size() == 0 {
		t.Fatalf("fixture: the turns are not in a write-ahead log (%v)", err)
	}

	byHand := filepath.Join(t.TempDir(), "by-hand.db")
	if err := copyFileSynced(path, byHand); err != nil {
		t.Fatal(err)
	}
	if _, held, err := InspectCopy(t.Context(), byHand); err != nil {
		t.Fatal(err)
	} else if held.Ephemeral["conversations"] != 0 {
		t.Fatalf("fixture: the file alone already holds %d turns — they were checkpointed, and this test proves nothing", held.Ephemeral["conversations"])
	}

	pin, err := s.PinAt(t.Context(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Release()
	dst := filepath.Join(t.TempDir(), "aii.db")
	if err := pin.BackupTo(t.Context(), dst); err != nil {
		t.Fatal(err)
	}
	pin.Release()
	_, held, err := InspectCopy(t.Context(), dst)
	if err != nil {
		t.Fatal(err)
	}
	if held.Ephemeral["conversations"] != 7 || held.LastTurnSeq != 7 {
		t.Fatalf("THE COPY LOST WHAT WAS IN THE WRITE-AHEAD LOG: %+v", held)
	}
}

// .
// .
// .
func TestAPinIsTheInstantItWasTaken(t *testing.T) {
	s := testStore(t)
	addTurns(t, s, 3)
	pin, err := s.PinAt(t.Context(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Release()
	addTurns(t, s, 40)

	facts, err := pin.Facts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if facts.Ephemeral["conversations"] != 3 {
		t.Fatalf("THE PIN READ THE LIVE DATABASE: %d turns, pinned at 3", facts.Ephemeral["conversations"])
	}
	dst := filepath.Join(t.TempDir(), "aii.db")
	if err := pin.BackupTo(t.Context(), dst); err != nil {
		t.Fatal(err)
	}
	_, held, err := InspectCopy(t.Context(), dst)
	if err != nil {
		t.Fatal(err)
	}
	if d := facts.Differs(held); d != "" {
		t.Fatalf("THE COPY IS NOT THE PINNED INSTANT: %s", d)
	}
	pin.Release()
	pin.Release()
	noReaderLeft(t, s, "after release")
}

// .
// .
func TestAPinIsRefusedAwayFromTheBoundary(t *testing.T) {
	s := testStore(t)
	addTurns(t, s, 1)
	pin, err := s.PinAt(t.Context(), 7, "00ff")
	if !errors.Is(err, ErrNotAtBoundary) || pin != nil {
		t.Fatalf("a pin was taken away from the captured boundary: pin=%v err=%v", pin, err)
	}
	noReaderLeft(t, s, "after a refused pin")
}

// .
// .
// .
func TestABackupNeverOverwritesAndLeavesOnlyItsCopy(t *testing.T) {
	s := testStore(t)
	addTurns(t, s, 2)
	pin, err := s.PinAt(t.Context(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Release()

	dir := t.TempDir()
	taken := filepath.Join(dir, "taken.db")
	if err := os.WriteFile(taken, []byte("someone else's bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pin.BackupTo(t.Context(), taken); err == nil {
		t.Fatal("a backup overwrote an existing file")
	}
	if raw, _ := os.ReadFile(taken); string(raw) != "someone else's bytes" {
		t.Fatal("the existing file was changed or removed")
	}
	os.Remove(taken)

	dst := filepath.Join(dir, "aii.db")
	if err := pin.BackupTo(t.Context(), dst); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InspectCopy(t.Context(), dst); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "aii.db" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the copy and its inspection left more than the copy: %v", names)
	}
}

func TestABackupRefusesAnExistingSidecarWithoutRemovingIt(t *testing.T) {
	s := testStore(t)
	pin, err := s.PinAt(t.Context(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Release()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		dst := filepath.Join(t.TempDir(), "copy.db")
		if err := os.WriteFile(dst+suffix, []byte("existing sidecar"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := pin.BackupTo(t.Context(), dst); err == nil {
			t.Fatal("existing sidecar accepted")
		}
		if b, err := os.ReadFile(dst + suffix); err != nil || string(b) != "existing sidecar" {
			t.Fatalf("foreign sidecar changed: %v", err)
		}
		if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("refused copy created a destination: %v", err)
		}
	}
}

func TestABackupReservesItsDestinationExclusively(t *testing.T) {
	s := testStore(t)
	first, err := s.PinAt(t.Context(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := s.PinAt(t.Context(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	dir := t.TempDir()
	for round := 0; round < 12; round++ {
		dst := filepath.Join(dir, strconv.Itoa(round)+".db")
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, pin := range []*Pin{first, second} {
			go func(p *Pin) { <-start; results <- p.BackupTo(t.Context(), dst) }(pin)
		}
		close(start)
		one, two := <-results, <-results
		if (one == nil) == (two == nil) {
			t.Fatalf("want exactly one copy, got %v and %v", one, two)
		}
		if _, _, err := InspectCopy(t.Context(), dst); err != nil {
			t.Fatalf("winning copy is not intact: %v", err)
		}
	}
}
