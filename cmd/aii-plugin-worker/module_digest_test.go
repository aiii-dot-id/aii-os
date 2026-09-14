package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
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
func TestTheWorkerRefusesAModuleThatChangedAfterVerification(t *testing.T) {
	path := fixture("echo.wasm")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("echo fixture must exist for this test to mean anything: %v", err)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	other := sha256.Sum256([]byte("the artifact the host actually verified"))
	verified := "sha256:" + hex.EncodeToString(other[:])

	w := startWorker(t, "-module-sha256="+verified, path)
	if err := w.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if code := w.exitCode(t); code == 0 {
		t.Fatal("THE WORKER LOADED A MODULE THE HOST NEVER VERIFIED — the swap window is open")
	}
	if got := w.stderr.String(); !strings.Contains(got, "digest mismatch") {
		t.Fatalf("the refusal must name the unmet requirement, got: %q", got)
	}
}

// .
// .
func TestTheWorkerLoadsTheModuleItWasHandedTheDigestFor(t *testing.T) {
	path := fixture("echo.wasm")
	good, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("echo fixture unavailable: %v", err)
	}
	sum := sha256.Sum256(good)

	w := startWorker(t, "-module-sha256=sha256:"+hex.EncodeToString(sum[:]), path)
	// .
	// .
	if err := w.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if code := w.exitCode(t); code != 0 {
		t.Fatalf("the verified module was refused (exit %d): %s", code, w.stderr.String())
	}
}
