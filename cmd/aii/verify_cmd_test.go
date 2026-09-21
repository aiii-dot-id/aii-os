package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// .
// .
// .
// .
func TestAiiVerifySaysWhatWasProvedAndHowFarTheWitnessReaches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	root := genesistest.NewRoot(t)
	if _, err := genesis.Birth(&genesis.BirthConfig{
		Name: "AiiVerify", Ring0Bundle: root.Ring0Bundle(t), Root: root.Env,
		KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: path, DBPath: filepath.Join(dir, "aii.db"),
	}); err != nil {
		t.Fatalf("birth: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runVerify([]string{"-ledger", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("a good chain exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "VERIFIED") || !strings.Contains(stdout.String(), "no record witnessed") {
		t.Fatalf("success line: %s", stdout.String())
	}

	// .
	// .
	// .
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b := append([]byte(nil), raw...)
	for i := len(b) / 2; i < len(b); i++ {
		if b[i] >= 'a' && b[i] <= 'y' {
			b[i]++
			break
		}
	}
	damaged := filepath.Join(t.TempDir(), "ledger.jsonl")
	if err := os.WriteFile(damaged, b, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runVerify([]string{"-ledger", damaged}, &stdout, &stderr); code != 1 {
		t.Fatalf("a damaged chain exited %d: %s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"NOT VERIFIED: ", "witness: 0 witness heads verified under 0 persisted keys, no record witnessed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q: %s", want, out)
		}
	}
	if !strings.Contains(out, "proved through record") && !strings.Contains(out, "nothing proved") && !strings.Contains(out, "nothing was established") {
		t.Errorf("the refusal does not say what was proved: %s", out)
	}
	if strings.Contains(out, "event ") {
		t.Errorf("the refusal still counts events instead of naming the record: %s", out)
	}
}

// .
// .
// .
func TestAiiVerifyRefusesALedgerThatEndsBeforeItsWitnessTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	root := genesistest.NewRoot(t)
	if _, err := genesis.Birth(&genesis.BirthConfig{
		Name: "AiiVerifyTail", Ring0Bundle: root.Ring0Bundle(t), Root: root.Env,
		KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: path, DBPath: filepath.Join(dir, "aii.db"),
	}); err != nil {
		t.Fatalf("birth: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runVerify([]string{"-ledger", path}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no witness tail beside it") {
		t.Fatalf("no tail beside it: exit %d: %s%s", code, stdout.String(), stderr.String())
	}
	tail := `{"ledger_ordinal":100000,"ledger_hash":"sha256:` + strings.Repeat("a", 64) + `","witnessed_at":"2026-09-01T00:00:00Z","witness_key_fingerprint":"fp"}`
	if err := os.WriteFile(filepath.Join(dir, "witness-tail.json"), []byte(tail), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runVerify([]string{"-ledger", path}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "NOT VERIFIED: LEDGER TRUNCATION") {
		t.Fatalf("a ledger that ends before the record its tail names: exit %d: %s%s", code, stdout.String(), stderr.String())
	}
}
