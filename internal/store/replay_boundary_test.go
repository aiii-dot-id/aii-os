package store

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func rewriteRecord(t *testing.T, path string, seq uint64, change func(*ledger.Event)) []byte {
	t.Helper()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	events, err := ledger.ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for i := range events {
		if events[i].Seq == seq {
			change(&events[i])
		}
		line, err := events[i].MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	if bytes.Equal(out.Bytes(), before) {
		t.Fatalf("record %d did not change", seq)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return before
}

func replayFile(path string) EventSource {
	return func(yield func(*ledger.Event) error) error { return ledger.Stream(path, yield) }
}

// The replay of a record that is not the verified one is refused as a
// boundary finding, naming the record, and nothing is published; the file
// as verified then opens.
func refusedAtBoundary(t *testing.T, dbPath, ledgerPath string, verified MirrorHead, before []byte, want string) {
	t.Helper()
	opened, err := OpenAndReplay(t.Context(), dbPath, verified, replayFile(ledgerPath))
	if opened != nil {
		opened.Close()
		t.Fatalf("A RECORD THE WALK DID NOT VERIFY WAS REPLAYED and the store published (err %v)", err)
	}
	var boundary *ReplayBoundaryError
	if !errors.As(err, &boundary) || !strings.Contains(err.Error(), want) {
		t.Fatalf("the replay was not refused at the boundary as %q: %v", want, err)
	}
	if err := os.WriteFile(ledgerPath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenAndReplay(t.Context(), dbPath, verified, replayFile(ledgerPath))
	if err != nil {
		t.Fatalf("the record as verified did not open after the refusal: %v", err)
	}
	defer st.Close()
	var content string
	if err := st.db.QueryRow(`SELECT content FROM experiences WHERE id = 'exp_b'`).Scan(&content); err != nil || content != "seen" {
		t.Fatalf("the experience the record holds: %q %v", content, err)
	}
}

// A PAYLOAD THE VERIFIED ENTRY DOES NOT NAME. Between the walk and the
// replay a record's payload is another (its entry, and so every link and
// the head, unchanged). Without the content check it materialized.
func TestReplayTakesOnlyThePayloadTheVerifiedEntryNames(t *testing.T) {
	dbPath, ledgerPath, kp := carryFixture(t)
	verified := carryHead(t, ledgerPath, kp)
	before := rewriteRecord(t, ledgerPath, 3, func(e *ledger.Event) {
		e.Payload = []byte(strings.Replace(string(e.Payload), `"content":"seen"`, `"content":"sewn"`, 1))
	})
	refusedAtBoundary(t, dbPath, ledgerPath, verified, before, "record 3 holds a payload its entry does not name")
}

// AN ENTRY THE VERIFIED CHAIN DOES NOT LINK. Between the walk and the
// replay a record's entry is another (its time moved), its payload its
// own; the record after it still names the entry that was verified, and
// the last record is the verified head. Without the link check it
// materialized.
func TestReplayTakesOnlyRecordsLinkedToTheVerifiedHead(t *testing.T) {
	dbPath, ledgerPath, kp := carryFixture(t)
	verified := carryHead(t, ledgerPath, kp)
	before := rewriteRecord(t, ledgerPath, 2, func(e *ledger.Event) {
		e.Timestamp = "2001-01-01T00:00:00Z"
	})
	refusedAtBoundary(t, dbPath, ledgerPath, verified, before, "record 3 does not link to the record before it")
}
