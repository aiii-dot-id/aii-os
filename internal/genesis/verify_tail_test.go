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

// verify_tail_test.go — a verify command holds the chain it verified to
// the witness tail beside it.
//
// A prev-hash chain cut after record N is a shorter chain that verifies;
// the only thing that can say it is short is a fact kept outside it, and
// the witness's local tail is that fact, one file beside the ledger. Boot
// asked it. The verify commands did not, so a copy cut short — with the
// file saying so in the same directory — was reported VERIFIED.

// witnessBeside is the loader both commands use, over ledgers with no
// persisted keys (no platform root is needed to load none).
func witnessBeside(dir string) (genesis.Beside, error) {
	beside, err := witness.LoadBeside(dir, nil)
	if err != nil {
		return nil, err
	}
	return beside, nil
}

// tailFile writes the tail as a reader meets it: the file's own format.
func tailFile(t *testing.T, dir string, ordinal uint64, hash string) {
	t.Helper()
	body := fmt.Sprintf(`{"ledger_ordinal":%d,"ledger_hash":%q,"witnessed_at":"2026-09-01T00:00:00Z","witness_key_fingerprint":"fp"}`, ordinal, hash)
	if err := os.WriteFile(filepath.Join(dir, witness.TailFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// cutTo copies the first keep records of a ledger into a directory of its
// own and returns the copy: a shorter chain, every link of it intact.
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

// A COPY CUT SHORT IS NOT VERIFIED WHEN THE TAIL BESIDE IT SAYS SO — and
// the same copy with no tail beside it is the attack: it verifies, and the
// report says that nothing outside the chain was asked.
func TestACopyCutShortIsRefusedByTheTailBesideIt(t *testing.T) {
	rue := bear(t, "Rue")
	events := written(t, rue, 5)
	last := events[len(events)-1]

	tailFile(t, rue.dir, last.Seq, last.EntryHash())
	out, errOut, code := genesis.VerifyCommand(rue.path, nil, witnessBeside)
	if code != genesis.ExitVerified || !strings.Contains(out, fmt.Sprintf("holds record %d ", last.Seq)) {
		t.Fatalf("the whole ledger, its tail beside it: exit %d\n%s%s", code, out, errOut)
	}

	short := cutTo(t, rue.path, len(events)-2)
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

// THE SAME ORDINAL, ANOTHER RECORD is a fork, and it is refused as one.
func TestAForkBehindTheTailIsRefused(t *testing.T) {
	rue := bear(t, "Rue")
	events := written(t, rue, 3)
	mid := events[len(events)-2]
	tailFile(t, rue.dir, mid.Seq, "sha256:"+strings.Repeat("f", 64))
	out, errOut, code := genesis.VerifyCommand(rue.path, nil, witnessBeside)
	if code != genesis.ExitRefused || out != "" || !strings.Contains(errOut, "NOT VERIFIED: LEDGER FORK") || !strings.Contains(errOut, mid.EntryHash()) {
		t.Fatalf("a record that is not the one the witness attested: exit %d\n%s%s", code, out, errOut)
	}
}

// RECORDS WRITTEN SINCE THE LAST ANCHOR ARE THE ORDINARY CASE: the tail
// names a record in the middle, the chain holds it, and the chain is held.
func TestATailBehindTheChainsEndHolds(t *testing.T) {
	rue := bear(t, "Rue")
	events := written(t, rue, 4)
	mid := events[1]
	tailFile(t, rue.dir, mid.Seq, mid.EntryHash())
	out, errOut, code := genesis.VerifyCommand(rue.path, nil, witnessBeside)
	if code != genesis.ExitVerified || !strings.Contains(out, fmt.Sprintf("holds record %d ", mid.Seq)) {
		t.Fatalf("a tail naming an earlier record of the same chain: exit %d\n%s%s", code, out, errOut)
	}
}

// A TAIL THAT IS PRESENT AND DOES NOT READ IS NEVER AN ABSENT ONE.
func TestATailThatDoesNotReadRefusesTheVerification(t *testing.T) {
	rue := bear(t, "Rue")
	written(t, rue, 1)
	for name, body := range map[string]string{
		"torn":          `{"ledger_ordinal":`,
		"no ordinal":    `{"ledger_ordinal":0,"ledger_hash":"sha256:aa"}`,
		"no hash":       `{"ledger_ordinal":3,"ledger_hash":""}`,
		"not an object": `[]`,
	} {
		if err := os.WriteFile(filepath.Join(rue.dir, witness.TailFileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		out, errOut, code := genesis.VerifyCommand(rue.path, nil, witnessBeside)
		if code != genesis.ExitRefused || out != "" || !strings.Contains(errOut, "NOT VERIFIED: ") || !strings.Contains(errOut, "witness tail file") {
			t.Errorf("%s: exit %d\n%s%s", name, code, out, errOut)
		}
	}
}

// A LEDGER SUPPLIED TO A CROSS-CHECK IS HELD TO THE TAIL BESIDE IT TOO. Cut
// short, it would answer "no such record" for a citation of what was cut —
// a mismatch laid at the citing identity's door for a fault in the copy.
func TestACrossLedgerCutShortIsEvidenceOfNothing(t *testing.T) {
	rue, mira := bear(t, "Rue"), bear(t, "Mira")
	mira.note(t, "early")
	late := mira.note(t, "late")
	miraEvents := written(t, mira, 0)
	rue.note(t, "cites-late", citing(mira, late))
	rue.lg.Close()

	short := cutTo(t, mira.path, len(miraEvents)-1)
	last := miraEvents[len(miraEvents)-1]
	tailFile(t, filepath.Dir(short), last.Seq, last.EntryHash())
	out, errOut, code := genesis.VerifyCommand(rue.path, []string{short}, witnessBeside)
	if code != genesis.ExitRefused || !strings.Contains(errOut, "CROSS-CHECK REFUSED") || !strings.Contains(errOut, "LEDGER TRUNCATION") {
		t.Fatalf("a supplied ledger cut short behind its tail: exit %d\n%s%s", code, out, errOut)
	}
	if strings.Contains(out, "mismatch") || strings.Contains(out, "CROSS-CHECK FAILED") {
		t.Errorf("the cut copy was used as evidence against the citation:\n%s", out)
	}
}
