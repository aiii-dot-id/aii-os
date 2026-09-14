package test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// .
// .
// .
// .
// .
// .
// .
// .

func buildRewrap(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rewrap")
	if runtime.GOOS == "windows" {
		// .
		// .
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "../cmd/rewrap")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cmd/rewrap: %v\n%s", err, out)
	}
	return bin
}

// .
// .
// .
func bornIdentity(t *testing.T) (ledgerPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	ledgerPath = filepath.Join(dir, "ledger.jsonl")
	keyPath = filepath.Join(dir, "identity.sec")
	root := genesistest.NewRoot(t)
	// .
	// .
	// .
	// .
	result, err := genesis.Birth(&genesis.BirthConfig{
		Name:        "RewrapTest",
		Ring0Bundle: root.MintRing0Bundle(t, "# Constitution"),
		Root:        root.Env,
		KeyPath:     keyPath,
		LedgerPath:  ledgerPath,
		DBPath:      filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatalf("birth: %v", err)
	}
	if result.Ledger != nil {
		result.Ledger.Close()
	}
	return ledgerPath, keyPath
}

// .
// .
// .
// .
func TestRewrapRefusesALedgerAnotherProcessHolds(t *testing.T) {
	bin := buildRewrap(t)
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "ledger.jsonl")
	keyPath := filepath.Join(dir, "identity.sec")
	root := genesistest.NewRoot(t)
	result, err := genesis.Birth(&genesis.BirthConfig{
		Name:        "HeldLedger",
		Ring0Bundle: root.MintRing0Bundle(t, "# Constitution"),
		Root:        root.Env,
		KeyPath:     keyPath,
		LedgerPath:  ledgerPath,
		DBPath:      filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatalf("birth: %v", err)
	}
	defer result.Ledger.Close()

	before, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	code, out := run(t, bin, "-ledger", ledgerPath, "-key", keyPath)
	if code == 0 {
		t.Fatal("rewrap rewrote a ledger that a live identity was holding")
	}
	if !strings.Contains(out, "already open") {
		t.Fatalf("the refusal does not name the cause: %s", out)
	}
	after, err := os.ReadFile(ledgerPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("a refused rewrap still altered the ledger")
	}
}

func run(t *testing.T, bin string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run rewrap: %v", err)
	}
	return code, string(out)
}

// .
// .
func TestRewrapRefusesToRunWithoutBothPaths(t *testing.T) {
	bin := buildRewrap(t)
	for _, args := range [][]string{
		{},
		{"-ledger", "x.jsonl"},
		{"-key", "identity.sec"},
	} {
		code, out := run(t, bin, args...)
		if code != 2 {
			t.Fatalf("args %v exited %d, want 2: %s", args, code, out)
		}
	}
}

// .
// .
// .
func TestRewrapLeavesTheLedgerUntouchedWhenTheKeyIsBad(t *testing.T) {
	bin := buildRewrap(t)
	ledgerPath, _ := bornIdentity(t)
	before, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}

	bad := filepath.Join(t.TempDir(), "not-a-key")
	if err := os.WriteFile(bad, []byte("this is not an identity key"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := run(t, bin, "-ledger", ledgerPath, "-key", bad)
	if code == 0 {
		t.Fatalf("rewrap accepted a key that is not one: %s", out)
	}
	after, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("THE LEDGER IS GONE after a refused key: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("THE LEDGER WAS MODIFIED by a run that failed to load its key")
	}
}

// .
// .
// .
func TestRewrapWithOutLeavesTheOriginalByteIdentical(t *testing.T) {
	bin := buildRewrap(t)
	ledgerPath, keyPath := bornIdentity(t)
	before, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "rewrapped.jsonl")

	code, stdout := run(t, bin, "-ledger", ledgerPath, "-key", keyPath, "-out", out)
	if code != 0 {
		t.Fatalf("rewrap -out exited %d: %s", code, stdout)
	}
	after, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("-out MODIFIED THE ORIGINAL — the safe mode is not safe")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("-out wrote nothing: %v", err)
	}
	// .
	if n, _, err := genesis.VerifySelfContained(out); err != nil {
		t.Fatalf("the rewrapped chain does not verify: %v", err)
	} else if n == 0 {
		t.Fatal("the rewrapped chain carries no events")
	}
	if !strings.Contains(stdout, "rewrapped") {
		t.Fatalf("rewrap said nothing about what it did: %s", stdout)
	}
}

// .
// .
// .
// .
func TestRewrapInPlaceProducesAChainThatStillVerifies(t *testing.T) {
	bin := buildRewrap(t)
	ledgerPath, keyPath := bornIdentity(t)
	beforeN, beforeFP, err := genesis.VerifySelfContained(ledgerPath)
	if err != nil {
		t.Fatalf("the ledger did not verify before rewrap: %v", err)
	}

	code, out := run(t, bin, "-ledger", ledgerPath, "-key", keyPath)
	if code != 0 {
		t.Fatalf("in-place rewrap exited %d: %s", code, out)
	}

	afterN, afterFP, err := genesis.VerifySelfContained(ledgerPath)
	if err != nil {
		t.Fatalf("THE LEDGER NO LONGER VERIFIES AFTER AN IN-PLACE REWRAP: %v", err)
	}
	if afterN != beforeN {
		t.Fatalf("event count changed across rewrap: %d -> %d", beforeN, afterN)
	}
	if afterFP != beforeFP {
		t.Fatalf("THE IDENTITY CHANGED across rewrap: %s -> %s", beforeFP, afterFP)
	}
}
