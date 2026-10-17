package test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// cmd/rewrap REWRITES THE ONLY IRREPLACEABLE ARTIFACT, and by default
// it replaces the ledger in place. Every projection in this system is
// f(ledger) and can be deleted and replayed; a destroyed ledger cannot
// be un-destroyed. The wrapper had no tests at all — ledger.Rewrap is
// covered, but the wrapper is what decides whether -out is honoured,
// what a bad key does, and whether the original survives a refusal.
//
// The destructive case runs LAST and only ever against t.TempDir().

func buildRewrap(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rewrap")
	if runtime.GOOS == "windows" {
		// Windows will not execute a file without a recognised
		// extension, so the probe binary is unreachable without it.
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "../cmd/rewrap")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cmd/rewrap: %v\n%s", err, out)
	}
	return bin
}

// bornIdentity returns a real ledger and its key. VerifySelfContained
// is the acceptance test for the result, so the chain must be one a
// stranger could verify — a hand-minted chain would not be.
func bornIdentity(t *testing.T) (ledgerPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	ledgerPath = filepath.Join(dir, "ledger.jsonl")
	keyPath = filepath.Join(dir, "identity.sec")
	root := genesistest.NewRoot(t)
	// BIRTH HANDS BACK AN OPEN LEDGER. Leaving it open makes rewrap
	// refuse — correctly, see TestRewrapRefusesALedgerAnotherProcessHolds
	// — so the fixture must close it or every test below fails on a
	// lock rather than on what it means to assert.
	result, err := genesis.Birth(&genesis.BirthConfig{
		Name:        "RewrapTest",
		Ring0Bundle: root.Ring0Bundle(t),
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

// A LEDGER ANOTHER PROCESS HOLDS IS NOT REWRAPPED. Found by tripping
// over it: the lock is what stops a rewrap racing a running identity,
// which would rewrite the record from underneath it. It refuses and
// says so, and the original is untouched.
func TestRewrapRefusesALedgerAnotherProcessHolds(t *testing.T) {
	bin := buildRewrap(t)
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "ledger.jsonl")
	keyPath := filepath.Join(dir, "identity.sec")
	root := genesistest.NewRoot(t)
	result, err := genesis.Birth(&genesis.BirthConfig{
		Name:        "HeldLedger",
		Ring0Bundle: root.Ring0Bundle(t),
		Root:        root.Env,
		KeyPath:     keyPath,
		LedgerPath:  ledgerPath,
		DBPath:      filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatalf("birth: %v", err)
	}
	defer result.Ledger.Close() // deliberately still held

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

// MISSING FLAGS EXIT 2, distinct from a failure. A script must tell "I
// called this wrong" from "this ledger is broken".
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

// A BAD KEY MUST NOT TOUCH THE LEDGER. The load happens before Rewrap
// is called; if that order ever inverted, a typo in -key would cost the
// record. The bytes are compared, not merely the file's existence.
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

// -out WRITES ELSEWHERE AND LEAVES THE ORIGINAL BYTE-IDENTICAL. This is
// the safe mode an operator reaches for first, and it is only safe if
// the original is genuinely untouched.
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
	// The result must be a chain a stranger can verify, not merely a file.
	if n, _, err := genesis.VerifySelfContained(out); err != nil {
		t.Fatalf("the rewrapped chain does not verify: %v", err)
	} else if n == 0 {
		t.Fatal("the rewrapped chain carries no events")
	}
	if !strings.Contains(stdout, "rewrapped") {
		t.Fatalf("rewrap said nothing about what it did: %s", stdout)
	}
}

// THE DESTRUCTIVE PATH, RUN LAST. Default replaces in place, and the
// replacement must be a chain a stranger can still verify — a rewrap
// that produced an unverifiable ledger would have destroyed the
// identity while reporting success.
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

// runSplit runs the binary and keeps its two streams apart.
func runSplit(t *testing.T, bin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run rewrap: %v", err)
	}
	return code, o.String(), e.String()
}

func TestRewrapAnswersByteForByte(t *testing.T) {
	bin := buildRewrap(t)
	const usage = "usage: rewrap -ledger ledger.jsonl -key identity.sec [-out new.jsonl] [-db aii.db]\n"
	defaults := "Usage of " + bin + ":\n" +
		"  -db string\n    \tthe identity's projection (aii.db) to carry across the re-wrap: the mirror is rewritten from the new record, derived tables rebuild, every ephemeral table keeps every row\n" +
		"  -key string\n    \tpath to identity.sec (required)\n" +
		"  -ledger string\n    \tpath to ledger.jsonl (required)\n" +
		"  -out string\n    \toutput path (default: replace -ledger atomically)\n"
	check := func(name string, code int, stdout, stderr string, wantCode int, wantOut, wantErr string) {
		t.Helper()
		if code != wantCode || stdout != wantOut || stderr != wantErr {
			t.Errorf("%s:\n exit %d, want %d\n stdout %q\n   want %q\n stderr %q\n   want %q", name, code, wantCode, stdout, wantOut, stderr, wantErr)
		}
	}
	code, o, e := runSplit(t, bin)
	check("no flags", code, o, e, 2, "", usage)
	code, o, e = runSplit(t, bin, "-ledger", "x.jsonl")
	check("no key", code, o, e, 2, "", usage)
	code, o, e = runSplit(t, bin, "-h")
	check("-h", code, o, e, 0, "", defaults)
	code, o, e = runSplit(t, bin, "-bogus")
	check("-bogus", code, o, e, 2, "", "flag provided but not defined: -bogus\n"+defaults)

	ledgerPath, keyPath := bornIdentity(t)
	bad := filepath.Join(t.TempDir(), "not-a-key")
	if err := os.WriteFile(bad, []byte("this is not an identity key"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, o, e = runSplit(t, bin, "-ledger", ledgerPath, "-key", bad)
	if code != 1 || o != "" || !strings.HasPrefix(e, "rewrap: load key: ") || strings.Count(e, "\n") != 1 {
		t.Errorf("a bad key: exit %d, stdout %q, stderr %q — want 1, nothing, one \"rewrap: load key: \" line", code, o, e)
	}

	n, _, err := genesis.VerifySelfContained(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "rewrapped.jsonl")
	code, o, e = runSplit(t, bin, "-ledger", ledgerPath, "-key", keyPath, "-out", out)
	check("-out", code, o, e, 0, "rewrapped "+strconv.Itoa(n)+" events as the record → "+out+" (verified and replayable)\n", "")

	code, o, e = runSplit(t, bin, "-ledger", ledgerPath, "-key", keyPath, "-db", filepath.Join(filepath.Dir(ledgerPath), "aii.db"))
	head := "rewrapped " + strconv.Itoa(n) + " events as the record → " + ledgerPath + " (verified and replayable)\n"
	report := regexp.MustCompile(`^carried \d+ ephemeral tables unchanged:[^\n]*\nrebuilt \d+ derived tables from the record:[^\n]*(\nconverted [^\n]*)*\n$`)
	if code != 0 || e != "" || !strings.HasPrefix(o, head) || !report.MatchString(strings.TrimPrefix(o, head)) {
		t.Errorf("in place with -db: exit %d\n stdout %q\n stderr %q\n want the re-wrap line, then the carry report", code, o, e)
	}
}
