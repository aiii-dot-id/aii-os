package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// THE BYTES HASHED MUST BE THE BYTES LOADED. The host verifies the
// artifact before every spawn — and it verifies a PATH, then hands that
// path on, and this process reopens it. A same-user adversary (threat
// model A5, named in the supervisor's own spec) can replace the file in
// between, so the host would have attested bytes that never ran. The
// existing tamper tests mutate the artifact before a RESTART and never
// touch this window.
//
// The worker now hashes the buffer it is about to load. There is no
// second read to disagree with the first.
func TestTheWorkerRefusesAModuleThatChangedAfterVerification(t *testing.T) {
	path := fixture("echo.wasm")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("echo fixture must exist for this test to mean anything: %v", err)
	}

	// The host verified SOMETHING ELSE; what is on disk is a perfectly
	// valid module. That is the shape of the real attack, and it is why
	// the swapped bytes must not be corrupt: a malformed module is
	// refused by the compiler for its own reasons, which would let this
	// test pass with no guard at all. Here, without the guard, the
	// worker loads happily and exits 0.
	other := sha256.Sum256([]byte("the artifact the host actually verified"))
	verified := "sha256:" + hex.EncodeToString(other[:])

	w := startWorker(t, "-module-sha256="+verified, path)
	if err := w.stdin.Close(); err != nil { // EOF = disconnect; a loaded worker exits 0
		t.Fatal(err)
	}
	if code := w.exitCode(t); code == 0 {
		t.Fatal("THE WORKER LOADED A MODULE THE HOST NEVER VERIFIED — the swap window is open")
	}
	if got := w.stderr.String(); !strings.Contains(got, "digest mismatch") {
		t.Fatalf("the refusal must name the unmet requirement, got: %q", got)
	}
}

// The control: the verified artifact still loads, or the guard is a wall
// rather than a gate.
func TestTheWorkerLoadsTheModuleItWasHandedTheDigestFor(t *testing.T) {
	path := fixture("echo.wasm")
	good, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("echo fixture unavailable: %v", err)
	}
	sum := sha256.Sum256(good)

	w := startWorker(t, "-module-sha256=sha256:"+hex.EncodeToString(sum[:]), path)
	// A worker that loaded cleanly is alive and waiting for frames; the
	// cleanup kills it. A refusal would have exited before now.
	if err := w.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if code := w.exitCode(t); code != 0 {
		t.Fatalf("the verified module was refused (exit %d): %s", code, w.stderr.String())
	}
}
