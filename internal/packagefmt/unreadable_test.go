package packagefmt

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// UNREADABLE IS NOT INVALID (A5). A package whose open or read the
// operating system refused was not judged, and VerifyFile answers with
// that refusal, typed, never with a verdict: on Windows a sharing
// violation on the open was ENVELOPE_MALFORMED [open], and the host kept
// it as the package's verdict. The
// seam is the reader verification reads through; the fault is the
// *fs.PathError an *os.File returns, which is what a Windows sharing or
// lock violation is. A Windows machine's own proof is separate and owed.
//
// This file needs the seam verifyRead, which the verifier before A5 does
// not have: on that parent it does not compile. VerifyFile's own cases on
// a missing path and a directory are in verify_test.go, and do.

var errLocked = errors.New("The process cannot access the file because another process has locked a portion of the file.")

// failingRead yields the first n bytes of what it holds, then fails as the
// operating system fails a read.
type failingRead struct {
	r io.Reader
	n int
}

func (f *failingRead) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, &fs.PathError{Op: "read", Path: "one.aiiospkg", Err: errLocked}
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	n, err := f.r.Read(p)
	f.n -= n
	return n, err
}

func notAVerdict(t *testing.T, err error, cause error) {
	t.Helper()
	var verdict *Error
	var pathErr *fs.PathError
	if err == nil || errors.As(err, &verdict) || !errors.As(err, &pathErr) || !errors.Is(err, cause) {
		t.Fatalf("A PACKAGE THAT COULD NOT BE READ WAS JUDGED: want the operating system's typed error, got %T %v", err, err)
	}
}

// Control 1, at the verifier: a read that fails partway is the read's
// error, and the same bytes read whole verify — nothing about them changed.
func TestAReadThatFailsIsNotAVerdictAndTheSameBytesVerify(t *testing.T) {
	f := fixtures(t)
	pkg := f.t1Pkg(t)
	for _, n := range []int{0, 1, len(pkg) / 2, len(pkg) - 1} {
		_, err := verifyRead(&failingRead{r: bytes.NewReader(pkg), n: n}, f.roots)
		notAVerdict(t, err, errLocked)
	}
	res, err := verifyRead(bytes.NewReader(pkg), f.roots)
	if err != nil || res.Tier != TierT1 {
		t.Fatalf("the same bytes, read whole, verify: %v", err)
	}
	// From disk, through the contention-aware open.
	path := filepath.Join(t.TempDir(), "one.aiiospkg")
	if err := os.WriteFile(path, pkg, 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := VerifyFile(path, f.roots); err != nil || res.Tier != TierT1 {
		t.Fatalf("VerifyFile through the contention-aware open: %v", err)
	}
}

// Control 3, at the verifier: what was read whole and is wrong is still a
// verdict. A truncated file read to its end is malformed, not unreadable —
// the end of the file is no refusal of the read — and a tampered or revoked
// package refuses with its reason, whatever reader it came through.
func TestWhatWasReadWholeAndIsWrongIsStillAVerdict(t *testing.T) {
	f := fixtures(t)
	pkg := f.t1Pkg(t)
	_, err := verifyRead(bytes.NewReader(pkg[:len(pkg)/2]), f.roots)
	expectReason(t, err, ReasonEnvelopeMalformed)
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		t.Fatalf("a truncated package is said as a refused read: %v", err)
	}

	tampered := bytes.Clone(pkg)
	tampered[len(tampered)/2] ^= 0xff
	_, err = verifyRead(bytes.NewReader(tampered), f.roots)
	var verdict *Error
	if !errors.As(err, &verdict) || errors.As(err, &pathErr) {
		t.Fatalf("a tampered package is a verdict: %T %v", err, err)
	}

	revoking := f.rootsRevoking(t, statusFileCertifier, f.statusRevokeManifest)
	_, err = verifyRead(bytes.NewReader(pkg), revoking)
	expectReason(t, err, ReasonTrustPayloadRevoked)
	path := filepath.Join(t.TempDir(), "one.aiiospkg")
	if err := os.WriteFile(path, pkg, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyFile(path, revoking)
	expectReason(t, err, ReasonTrustPayloadRevoked)
}
