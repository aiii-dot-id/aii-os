package test

import (
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
// .
// .
// .
// .
// .
const conformanceRing0 = `# Constitution`

func buildVerify(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "verify")
	if runtime.GOOS == "windows" {
		// .
		// .
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "../cmd/verify")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cmd/verify: %v\n%s", err, out)
	}
	return bin
}

func runVerify(t *testing.T, bin string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run verify: %v", err)
	}
	return code, string(out)
}

func TestVerifyBinaryAcceptsAGoodChainAndRefusesADamagedOne(t *testing.T) {
	bin := buildVerify(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	// .
	// .
	// .
	// .
	// .
	root := genesistest.NewRoot(t)
	if _, err := genesis.Birth(&genesis.BirthConfig{
		Name:        "VerifyCmdTest",
		Ring0Bundle: root.MintRing0Bundle(t, conformanceRing0),
		Root:        root.Env,
		KeyPath:     filepath.Join(dir, "identity.sec"),
		LedgerPath:  path,
		DBPath:      filepath.Join(dir, "aii.db"),
	}); err != nil {
		t.Fatalf("birth: %v", err)
	}

	code, out := runVerify(t, bin, "-ledger", path)
	if code != 0 {
		t.Fatalf("a good chain exited %d: %s", code, out)
	}
	if !strings.Contains(out, "VERIFIED") {
		t.Fatalf("a verified chain did not say so: %s", out)
	}

	// .
	// .
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	damaged := filepath.Join(dir, "damaged.jsonl")
	b := append([]byte(nil), raw...)
	for i := len(b) / 2; i < len(b); i++ {
		if b[i] >= 'a' && b[i] <= 'y' {
			b[i]++
			break
		}
	}
	if err := os.WriteFile(damaged, b, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out = runVerify(t, bin, "-ledger", damaged)
	if code != 1 {
		t.Fatalf("A DAMAGED CHAIN EXITED %d — the conformance check passed a chain that was altered: %s", code, out)
	}
	if !strings.Contains(out, "NOT VERIFIED") {
		t.Fatalf("the refusal does not say what happened: %s", out)
	}
}

// .
// .
// .
func TestVerifyBinaryDistinguishesMisuseFromFailure(t *testing.T) {
	bin := buildVerify(t)

	code, out := runVerify(t, bin)
	if code != 2 {
		t.Fatalf("no arguments exited %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "usage") {
		t.Fatalf("misuse did not print usage: %s", out)
	}

	code, _ = runVerify(t, bin, "-ledger", filepath.Join(t.TempDir(), "absent.jsonl"))
	if code != 1 {
		t.Fatalf("a missing ledger exited %d, want 1 — an absent chain is unverified, not misuse", code)
	}
}
