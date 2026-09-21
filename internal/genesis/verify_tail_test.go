package genesis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
func witnessBeside(dir string) (genesis.Beside, error) {
	beside, err := witness.LoadBeside(dir, nil)
	if err != nil {
		return nil, err
	}
	return beside, nil
}

// .
func tailFile(t *testing.T, dir string, ordinal uint64, hash string) {
	t.Helper()
	body := fmt.Sprintf(`{"ledger_ordinal":%d,"ledger_hash":%q,"witnessed_at":"2026-09-01T00:00:00Z","witness_key_fingerprint":"fp"}`, ordinal, hash)
	if err := os.WriteFile(filepath.Join(dir, witness.TailFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// .
// .
func cutTo(t *testing.T, path string, keep int) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	if keep >= len(lines)-1 {
		t.Fatalf("rig: asked to keep %d of %d records", keep, len(lines)-1)
	}
	out := filepath.Join(t.TempDir(), "ledger.jsonl")
	if err := os.WriteFile(out, []byte(strings.Join(lines[:keep], "")), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

func written(t *testing.T, b *born, notes int) []ledger.Event {
	t.Helper()
	for i := 0; i < notes; i++ {
		b.note(t, fmt.Sprintf("n%d", i))
	}
	b.lg.Close()
	events, err := ledger.ReadAll(b.path)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// .
// .
// .
func TestACopyCutShortIsRefusedByTheTailBesideIt(t *testing.T) {
	robin := bear(t, "Robin")
	events := written(t, robin, 5)
	last := events[len(events)-1]

	tailFile(t, robin.dir, last.Seq, last.EntryHash())
	out, errOut, code := genesis.VerifyCommand(robin.path, nil, witnessBeside)
	if code != genesis.ExitVerified || !strings.Contains(out, fmt.Sprintf("holds record %d ", last.Seq)) {
		t.Fatalf("the whole ledger, its tail beside it: exit %d\n%s%s", code, out, errOut)
	}

	short := cutTo(t, robin.path, len(events)-2)
	out, errOut, code = genesis.VerifyCommand(short, nil, witnessBeside)
	if code != genesis.ExitVerified || !strings.Contains(out, "no witness tail beside it") {
		t.Fatalf("rig: a cut copy with nothing beside it is a shorter chain that verifies, and the report says nothing was asked: exit %d\n%s%s", code, out, errOut)
	}

	tailFile(t, filepath.Dir(short), last.Seq, last.EntryHash())
	out, errOut, code = genesis.VerifyCommand(short, nil, witnessBeside)
	if code != genesis.ExitRefused || out != "" {
		t.Fatalf("A COPY CUT SHORT WAS REPORTED VERIFIED with the tail beside it naming record %d: exit %d\n%s%s", last.Seq, code, out, errOut)
	}
	for _, want := range []string{"NOT VERIFIED: LEDGER TRUNCATION", fmt.Sprintf("ends at seq %d", len(events)-2), fmt.Sprintf("attested event %d", last.Seq), "witness: ", fmt.Sprintf("names record %d ", last.Seq)} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, errOut)
		}
	}
}

// .
func TestAForkBehindTheTailIsRefused(t *testing.T) {
	robin := bear(t, "Robin")
	events := written(t, robin, 3)
	mid := events[len(events)-2]
	tailFile(t, robin.dir, mid.Seq, "sha256:"+strings.Repeat("f", 64))
	out, errOut, code := genesis.VerifyCommand(robin.path, nil, witnessBeside)
	if code != genesis.ExitRefused || out != "" || !strings.Contains(errOut, "NOT VERIFIED: LEDGER FORK") || !strings.Contains(errOut, mid.EntryHash()) {
		t.Fatalf("a record that is not the one the witness attested: exit %d\n%s%s", code, out, errOut)
	}
}

// .
// .
func TestATailBehindTheChainsEndHolds(t *testing.T) {
	robin := bear(t, "Robin")
	events := written(t, robin, 4)
	mid := events[1]
	tailFile(t, robin.dir, mid.Seq, mid.EntryHash())
	out, errOut, code := genesis.VerifyCommand(robin.path, nil, witnessBeside)
	if code != genesis.ExitVerified || !strings.Contains(out, fmt.Sprintf("holds record %d ", mid.Seq)) {
		t.Fatalf("a tail naming an earlier record of the same chain: exit %d\n%s%s", code, out, errOut)
	}
}

// .
func TestATailThatDoesNotReadRefusesTheVerification(t *testing.T) {
	robin := bear(t, "Robin")
	written(t, robin, 1)
	for name, body := range map[string]string{
		"torn":          `{"ledger_ordinal":`,
		"no ordinal":    `{"ledger_ordinal":0,"ledger_hash":"sha256:aa"}`,
		"no hash":       `{"ledger_ordinal":3,"ledger_hash":""}`,
		"not an object": `[]`,
	} {
		if err := os.WriteFile(filepath.Join(robin.dir, witness.TailFileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		out, errOut, code := genesis.VerifyCommand(robin.path, nil, witnessBeside)
		if code != genesis.ExitRefused || out != "" || !strings.Contains(errOut, "NOT VERIFIED: ") || !strings.Contains(errOut, "witness tail file") {
			t.Errorf("%s: exit %d\n%s%s", name, code, out, errOut)
		}
	}
}

// .
// .
// .
func TestACrossLedgerCutShortIsEvidenceOfNothing(t *testing.T) {
	robin, nova := bear(t, "Robin"), bear(t, "Nova")
	nova.note(t, "early")
	late := nova.note(t, "late")
	novaEvents := written(t, nova, 0)
	robin.note(t, "cites-late", citing(nova, late))
	robin.lg.Close()

	short := cutTo(t, nova.path, len(novaEvents)-1)
	last := novaEvents[len(novaEvents)-1]
	tailFile(t, filepath.Dir(short), last.Seq, last.EntryHash())
	out, errOut, code := genesis.VerifyCommand(robin.path, []string{short}, witnessBeside)
	if code != genesis.ExitRefused || !strings.Contains(errOut, "CROSS-CHECK REFUSED") || !strings.Contains(errOut, "LEDGER TRUNCATION") {
		t.Fatalf("a supplied ledger cut short behind its tail: exit %d\n%s%s", code, out, errOut)
	}
	if strings.Contains(out, "mismatch") || strings.Contains(out, "CROSS-CHECK FAILED") {
		t.Errorf("the cut copy was used as evidence against the citation:\n%s", out)
	}
}
