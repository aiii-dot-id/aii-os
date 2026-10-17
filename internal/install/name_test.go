package install

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// The runtime takes the name from the FIRST ring0.genesis event's
// payload.name (store.IdentityName). The CLI must apply the same rule
// against the same authority, or the two disagree about who an
// identity is.
func TestNameReadsTheGenesisEvent(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir,
		record(t, 1, ledger.EventRing0Genesis, `{"model_id":"zai-org/glm-5.2","name":"TestIdentity"}`),
		record(t, 2, ledger.EventExperienceCreate, `{"name":"NotTheIdentityName"}`),
	)
	if got := Name(dir); got != "TestIdentity" {
		t.Fatalf("Name = %q, want %q", got, "TestIdentity")
	}
}

// A later event carrying a name field must not win: only genesis names
// the identity.
func TestNameIgnoresLaterNameFields(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir,
		record(t, 1, ledger.EventExperienceCreate, `{"name":"Impostor"}`),
		record(t, 2, ledger.EventRing0Genesis, `{"name":"TestPeer"}`),
	)
	if got := Name(dir); got != "TestPeer" {
		t.Fatalf("Name = %q, want %q — a non-genesis event named the identity", got, "TestPeer")
	}
}

// An unborn slot is a state, not an error: init creates the slot before
// there is any ledger at all.
func TestNameOfAnUnbornSlotIsEmpty(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := Name(dir); got != "" {
		t.Fatalf("Name of an unborn slot = %q, want empty", got)
	}
}

// Ledger lines carry signatures and can exceed bufio's default 64KiB
// token. A long line must not silently truncate the scan and lose the
// name.
func TestNameSurvivesALongLedgerLine(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	pad := make([]byte, 200*1024)
	for i := range pad {
		pad[i] = 'A'
	}
	writeLedger(t, dir,
		record(t, 1, ledger.EventRing0Genesis, `{"name":"TestIdentity","pad":"`+string(pad)+`"}`),
	)
	if got := Name(dir); got != "TestIdentity" {
		t.Fatalf("Name = %q, want %q — a long signed line defeated the scanner", got, "TestIdentity")
	}
}

// An identity that has lived keeps its first records in sealed segments,
// and the tail begins after them. The name is still the genesis record's.
func TestNameOfASealedRecordIsReadFromItsFirstSegment(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir, record(t, 3, ledger.EventExperienceCreate, `{"name":"NotTheIdentityName"}`))
	var seg bytes.Buffer
	gz := gzip.NewWriter(&seg)
	for _, line := range []string{
		record(t, 1, ledger.EventRing0Genesis, `{"name":"TestIdentity"}`),
		record(t, 2, ledger.EventSystemWitnessed, `{}`),
	} {
		if _, err := gz.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	const name = "segment-1-2.jsonl.gz"
	if !ledger.IsSegmentName(name) {
		t.Fatalf("%s is not a segment's name: the fixture is not a sealed record", name)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.Dir(ledgerRelPath), name), seg.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Name(dir); got != "TestIdentity" {
		t.Fatalf("Name = %q, want %q: the genesis record is in the first segment, not the tail", got, "TestIdentity")
	}
}

// RefreshNames rebuilds by-name from the ledgers, so a link that
// disagrees with the ledger is corrected rather than trusted.
func TestRefreshNamesRebuildsFromTheLedger(t *testing.T) {
	root := t.TempDir()
	d0, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, 1); err != nil {
		t.Fatal(err)
	}
	writeLedger(t, d0, record(t, 1, ledger.EventRing0Genesis, `{"name":"TestPeer"}`))

	// A stale link pointing at the wrong slot, as a hand edit or an
	// older layout would leave.
	if err := LinkName(root, "TestPeer", 1); err != nil {
		t.Fatal(err)
	}

	names := RefreshNames(root)
	if names[SlotName(0)] != "TestPeer" {
		t.Fatalf("RefreshNames = %v, want slot 0 -> TestPeer", names)
	}
	got, err := os.Readlink(filepath.Join(root, ByName, "TestPeer"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("..", SlotName(0)); got != want {
		t.Fatalf("stale link survived refresh: %q, want %q", got, want)
	}
}

// record is one line of a ledger as the ledger writes it. A fixture in any
// other shape tests a reader of something no identity has.
func record(t *testing.T, seq uint64, typ ledger.EventType, payload string) string {
	t.Helper()
	line, err := (&ledger.Event{Seq: seq, Timestamp: "2026-01-01T00:00:00Z", Type: typ, Payload: []byte(payload), Sig: "sig"}).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

func writeLedger(t *testing.T, slotDir string, lines ...string) {
	t.Helper()
	path := filepath.Join(slotDir, ledgerRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}
