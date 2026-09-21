package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
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
		Ring0Bundle: root.Ring0Bundle(t),
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
	if !strings.Contains(out, "no record witnessed; 0 tail heads unverified") {
		t.Fatalf("a verified chain does not say how far the witness reaches: %s", out)
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
	// .
	// .
	// .
	established := strings.Contains(out, "proved through record") || strings.Contains(out, "nothing proved") || strings.Contains(out, "nothing was established")
	if !established {
		t.Fatalf("the refusal does not say what was proved: %s", out)
	}
	if !strings.Contains(out, "witness: 0 witness heads verified under 0 persisted keys, no record witnessed") {
		t.Fatalf("the refusal does not say how far the witness reaches: %s", out)
	}
	if strings.Contains(out, "event ") {
		t.Fatalf("the refusal still counts events instead of naming the record: %s", out)
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

// .
// .
// .
// .
func TestTheVerifyCommandsKeepNoWordingOfTheirOwn(t *testing.T) {
	for _, src := range []string{"../cmd/verify/main.go", "../cmd/aii/verify_cmd.go"} {
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "VERIFIED") {
			t.Errorf("%s words the verdict itself — it must print what genesis.VerifyCommand returns", src)
		}
		for _, want := range []string{"genesis.VerifyCommand(", "witness.LoadBeside("} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("%s does not call %s", src, want)
			}
		}
	}
}

// .
// .
// .
func TestVerifyBinaryCrossCheckExitsThreeWhenSomethingCouldNotBeChecked(t *testing.T) {
	bin := buildVerify(t)
	birth := func(name string) (dir, fp string) {
		dir = t.TempDir()
		root := genesistest.NewRoot(t)
		res, err := genesis.Birth(&genesis.BirthConfig{
			Name: name, Ring0Bundle: root.Ring0Bundle(t), Root: root.Env,
			KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		})
		if err != nil {
			t.Fatalf("birth: %v", err)
		}
		kp, err := crypto.LoadKeyPair(filepath.Join(dir, "identity.sec"))
		if err != nil {
			t.Fatal(err)
		}
		if name == "Citing" {
			if _, err := res.Ledger.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{
				"id": "exp_cites", "content": "cites someone absent", "category": "observation", "provenance": "self",
				"cites": []ledger.Citation{{Identity: strings.Repeat("cd", 32), Seq: 4, EntryHash: strings.Repeat("ab", 32)}},
			}, kp); err != nil {
				t.Fatal(err)
			}
		}
		res.Ledger.Close()
		return dir, kp.Fingerprint()
	}
	citing, _ := birth("Citing")
	peer, _ := birth("Peer")

	code, out := runVerify(t, bin, "-ledger", filepath.Join(citing, "ledger.jsonl"))
	if code != 0 || strings.Contains(out, "CROSS") {
		t.Fatalf("without -cross nothing changes: %d %s", code, out)
	}
	code, out = runVerify(t, bin, "-ledger", filepath.Join(citing, "ledger.jsonl"), "-cross", filepath.Join(peer, "ledger.jsonl"))
	if code != 3 || !strings.Contains(out, "CROSS-CHECK INCOMPLETE") || !strings.Contains(out, "VERIFIED: ") {
		t.Fatalf("AN INCOMPLETE CROSS-CHECK DID NOT EXIT 3: %d %s", code, out)
	}
}

// .
// .
// .
// .
func TestVerifyBinaryRefusesACopyCutShortBehindItsWitnessTail(t *testing.T) {
	bin := buildVerify(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	root := genesistest.NewRoot(t)
	res, err := genesis.Birth(&genesis.BirthConfig{
		Name: "VerifyTail", Ring0Bundle: root.Ring0Bundle(t), Root: root.Env,
		KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: path, DBPath: filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatalf("birth: %v", err)
	}
	kp, err := crypto.LoadKeyPair(filepath.Join(dir, "identity.sec"))
	if err != nil {
		t.Fatal(err)
	}
	lg := res.Ledger
	var last *ledger.Event
	for _, id := range []string{"exp_a", "exp_b"} {
		if last, err = lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{
			"id": id, "content": "observed", "category": "observation", "provenance": "self",
		}, kp); err != nil {
			t.Fatal(err)
		}
	}
	lg.Close()
	tail := `{"ledger_ordinal":` + strconv.FormatUint(last.Seq, 10) + `,"ledger_hash":"` + last.EntryHash() + `","witnessed_at":"2026-09-01T00:00:00Z","witness_key_fingerprint":"fp"}`
	if err := os.WriteFile(filepath.Join(dir, "witness-tail.json"), []byte(tail), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := runVerify(t, bin, "-ledger", path); code != 0 || !strings.Contains(out, "as the witness tail beside it names it") {
		t.Fatalf("the whole ledger, its tail beside it: exit %d: %s", code, out)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	cutDir := t.TempDir()
	short := filepath.Join(cutDir, "ledger.jsonl")
	if err := os.WriteFile(short, []byte(strings.Join(lines[:len(lines)-2], "")), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := runVerify(t, bin, "-ledger", short); code != 0 || !strings.Contains(out, "no witness tail beside it") {
		t.Fatalf("rig: the cut copy alone is a chain that verifies, and says nothing was asked: exit %d: %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(cutDir, "witness-tail.json"), []byte(tail), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := runVerify(t, bin, "-ledger", short)
	if code != 1 || !strings.Contains(out, "NOT VERIFIED: LEDGER TRUNCATION") || strings.Contains(out, "\nVERIFIED") || strings.HasPrefix(out, "VERIFIED") {
		t.Fatalf("A COPY CUT SHORT WAS NOT REFUSED: exit %d: %s", code, out)
	}
}
