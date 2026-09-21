package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
// .
func TestAuditVerifyTailCLI(t *testing.T) {
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	lg, err := ledger.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if _, err := lg.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0, genesis.BirthAttestationPayload{PublicKey: kp.PublicKeyB64(), Fingerprint: kp.Fingerprint()}, kp); err != nil {
		t.Fatal(err)
	}
	var last *ledger.Event
	for i := 0; i < 2; i++ {
		last, err = lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3, map[string]string{"content": "synthetic-tail-fixture"}, kp)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := json.Marshal(witness.LocalTail{LedgerOrdinal: int64(last.Seq), LedgerHash: last.EntryHash(), WitnessedAt: "2026-09-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	run := func(want int, clause string) {
		t.Helper()
		var out, refused bytes.Buffer
		got := runVerify([]string{"-ledger", path}, &out, &refused)
		if got != want {
			t.Fatalf("code=%d want=%d stdout=%s stderr=%s", got, want, &out, &refused)
		}
		if want == 0 {
			if refused.Len() != 0 || !strings.Contains(out.String(), clause) {
				t.Fatalf("success stdout=%s stderr=%s", &out, &refused)
			}
		} else {
			if out.Len() != 0 || !strings.Contains(refused.String(), clause) {
				t.Fatalf("refusal stdout=%s stderr=%s", &out, &refused)
			}
		}
	}
	run(0, "no witness tail beside it")
	if err := os.WriteFile(witness.TailPath(dir), tail, 0600); err != nil {
		t.Fatal(err)
	}
	run(0, "holds record 3 ")
	lines := bytes.SplitAfter(full, []byte("\n"))
	if err := os.WriteFile(path, bytes.Join(lines[:2], nil), 0600); err != nil {
		t.Fatal(err)
	}
	run(1, "NOT VERIFIED: LEDGER TRUNCATION")
	if err := os.WriteFile(path, full, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(witness.TailPath(dir), []byte(`{"ledger_ordinal":3,"ledger_hash":"sha256:wrong"}`), 0600); err != nil {
		t.Fatal(err)
	}
	run(1, "NOT VERIFIED: LEDGER FORK")
	if err := os.WriteFile(witness.TailPath(dir), []byte(`{"ledger_ordinal":`), 0600); err != nil {
		t.Fatal(err)
	}
	run(1, "witness tail file corrupt")
}
