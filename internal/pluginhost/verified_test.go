package pluginhost

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
// .
// .
func TestAVerifiedImageRunsTheBytesItVerified(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("descriptor-bound exec is a linux capability; %s reports: %s", runtime.GOOS, bindingFor(t))
	}
	dir := t.TempDir()
	artifact := filepath.Join(dir, "artifact")
	original := []byte("#!/bin/sh\necho VERIFIED-ORIGINAL\n")
	if err := os.WriteFile(artifact, original, 0o700); err != nil {
		t.Fatal(err)
	}

	image, err := OpenVerified(artifact, digestOf(original))
	if err != nil {
		t.Fatalf("the artifact we just wrote did not verify: %v", err)
	}
	defer image.Close()

	// .
	// .
	evil := filepath.Join(dir, "evil")
	if err := os.WriteFile(evil, []byte("#!/bin/sh\necho ATTACKER-SWAPPED\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(evil, artifact); err != nil {
		t.Fatal(err)
	}

	// .
	// .
	byPath, _ := exec.Command(artifact).CombinedOutput()
	if !strings.Contains(string(byPath), "ATTACKER-SWAPPED") {
		t.Fatalf("the swap did not take, so this test cannot prove the binding: %q", byPath)
	}

	argv := image.Argv()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.ExtraFiles = image.ExtraFiles()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the bound image did not run: %v (%s)", err, out)
	}
	if !strings.Contains(string(out), "VERIFIED-ORIGINAL") {
		t.Fatalf("THE CHILD RAN BYTES THE HOST NEVER VERIFIED: %q", out)
	}
}

// .
func TestOpenVerifiedRefusesAnArtifactThatDoesNotMatch(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "artifact")
	if err := os.WriteFile(artifact, []byte("the real bytes"), 0o700); err != nil {
		t.Fatal(err)
	}
	image, err := OpenVerified(artifact, digestOf([]byte("what the host expected")))
	if err == nil {
		image.Close()
		t.Fatal("an artifact that does not match its verified digest must be refused")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("the refusal must name the unmet requirement: %v", err)
	}
}

// .
// .
// .
// .
func TestEveryPlatformDeclaresItsBinding(t *testing.T) {
	b := bindingFor(t)
	if b == "" {
		t.Fatal("a platform that reports no binding lets the containment line claim whatever it likes")
	}
	bound := strings.Contains(b, "descriptor-bound") || strings.Contains(b, "share-locked")
	if !bound && !strings.Contains(b, "NOT bound") {
		t.Fatalf("a platform that does not bind must say NOT bound, plainly: %q", b)
	}
}

func bindingFor(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	artifact := filepath.Join(dir, "artifact")
	body := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(artifact, body, 0o700); err != nil {
		t.Fatal(err)
	}
	image, err := OpenVerified(artifact, digestOf(body))
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	return image.Binding()
}

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
// .
// .
// .
func TestTheDigestFollowsTheDescriptorNotThePathname(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")

	attacker := []byte("attacker bytes — this inode is the one that would run")
	legit := []byte("legitimate bytes — restored at the pathname to fool a second read")
	if err := os.WriteFile(path, attacker, 0o700); err != nil {
		t.Fatal(err)
	}

	// .
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// .
	// .
	// .
	staging := filepath.Join(dir, "staging")
	if err := os.WriteFile(staging, legit, 0o700); err != nil {
		t.Fatal(err)
	}
	err = os.Rename(staging, path)
	if runtime.GOOS == "windows" {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if err == nil {
			t.Fatal("Windows allowed a file with an open handle to be replaced — the share-mode assumption in bindImage no longer holds")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}

	// .
	// .
	if onDisk, err := os.ReadFile(path); err != nil || string(onDisk) != string(legit) {
		t.Fatalf("the pathname was not restored, so the swap is not being tested: %v", err)
	}

	got, err := digestOfHandle(f)
	if err != nil {
		t.Fatal(err)
	}
	attackerSum := sha256.Sum256(attacker)
	wantAttacker := "sha256:" + hex.EncodeToString(attackerSum[:])
	legitSum := sha256.Sum256(legit)
	wantLegit := "sha256:" + hex.EncodeToString(legitSum[:])

	if got == wantLegit {
		t.Fatal("the digest followed the PATHNAME: it hashed the restored legitimate file while the descriptor still refers to the attacker inode — the bytes that would execute are unverified")
	}
	if got != wantAttacker {
		t.Fatalf("digest = %s, want the descriptor's own bytes %s", got, wantAttacker)
	}
}

// .
// .
func TestTheVerifiedImageCarriesTheHandleThatWasHashed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	body := []byte("the artifact")
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	img, err := OpenVerified(path, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()

	// .
	// .
	// .
	again, err := digestOfHandle(img.handle())
	if err != nil {
		t.Fatal(err)
	}
	if again != digest {
		t.Fatalf("the image carries a handle that does not hash to what was verified: %s", again)
	}
}

// .
// .
func TestAMismatchedDigestRefusesAndReleasesTheHandle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	if err := os.WriteFile(path, []byte("real bytes"), 0o700); err != nil {
		t.Fatal(err)
	}
	img, err := OpenVerified(path, "sha256:"+strings.Repeat("00", 32))
	if err == nil {
		img.Close()
		t.Fatal("a digest mismatch must refuse")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("the refusal must name the cause: %v", err)
	}
	if img != nil {
		t.Fatal("a refused verification must not return an image")
	}
}
