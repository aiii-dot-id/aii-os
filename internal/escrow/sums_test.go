package escrow

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// signedList is a small snapshot set whose checksum list kp has signed,
// with the record head it names.
func signedList(t *testing.T, kp *crypto.KeyPair) string {
	t.Helper()
	dir, names, hashes := provedSet(t)
	var list strings.Builder
	for _, n := range names {
		list.WriteString(hashes[n] + "  " + n + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, SumsName), []byte(list.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SignSumsIn(dir, kp, 42, "head-hash"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeSig(t *testing.T, dir string, s SumsSig) {
	t.Helper()
	raw, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, SumsSigName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// THE SIGNATURE HOLDS THE LIST TO ONE KEY, ONE HEAD AND ITS OWN BYTES, and
// the list names it, as it names every file, as its last line: a reader
// that knows nothing of signatures (0.1.23's CheckSums is this one) takes
// the signed list and every file it names.
func TestTheChecksumListsSignatureVerifiesOnlyForItsKeyItsHeadAndItsBytes(t *testing.T) {
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	dir := signedList(t, kp)
	signed, err := VerifySumsSig(dir, kp.PublicKeyBytes())
	if err != nil || signed.Identity != kp.Fingerprint() || signed.LastSeq != 42 || signed.LastHash != "head-hash" {
		t.Fatalf("a list its own key signed: %+v %v", signed, err)
	}
	present := []string{"ledger.jsonl", "aii.db", "witness-keys/k.json", SumsSigName}
	if err := CheckSums(dir, present); err != nil {
		t.Fatalf("the signature beside the list is refused by the list's check: %v", err)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, SumsName))
	if _, named := SignedPart(sums); !named {
		t.Fatalf("THE LIST DOES NOT NAME ITS SIGNATURE, so an earlier program refuses the snapshot:\n%s", sums)
	}
	if err := SignSumsIn(dir, kp, 42, "head-hash"); err == nil {
		t.Fatal("a signed list was signed again")
	}
	if err := os.WriteFile(filepath.Join(dir, "stray"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckSums(dir, append(present, "stray")); err == nil || !strings.Contains(err.Error(), "stray") {
		t.Fatalf("a file the list does not name passed because a signature may: %v", err)
	}

	other, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySumsSig(dir, other.PublicKeyBytes()); !errors.Is(err, ErrSumsSig) {
		t.Fatalf("verified under another identity's key: %v", err)
	}

	// The head is under the signature: a different record named is a
	// signature that does not verify.
	var s SumsSig
	raw, _ := os.ReadFile(filepath.Join(dir, SumsSigName))
	json.Unmarshal(raw, &s)
	moved := s
	moved.LastSeq = 43
	writeSig(t, dir, moved)
	if _, err := VerifySumsSig(dir, kp.PublicKeyBytes()); !errors.Is(err, ErrSumsSig) {
		t.Fatalf("a signature moved to another record verified: %v", err)
	}
	bad := s
	sig, _ := base64.StdEncoding.DecodeString(s.Sig)
	sig[len(sig)/2] ^= 1
	bad.Sig = base64.StdEncoding.EncodeToString(sig)
	writeSig(t, dir, bad)
	if _, err := VerifySumsSig(dir, kp.PublicKeyBytes()); !errors.Is(err, ErrSumsSig) {
		t.Fatalf("a damaged signature verified: %v", err)
	}
	writeSig(t, dir, s)

	// The list rewritten — one line, any line — is not the list signed.
	sumsPath := filepath.Join(dir, SumsName)
	sums, _ = os.ReadFile(sumsPath)
	if err := os.WriteFile(sumsPath, append(sums, []byte("0000  extra\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySumsSig(dir, kp.PublicKeyBytes()); !errors.Is(err, ErrSumsSig) || !strings.Contains(err.Error(), "not the list that was signed") {
		t.Fatalf("a rewritten list verified: %v", err)
	}

	// None at all is its own answer, never a pass: the list names none.
	unsigned, _ := SignedPart(sums)
	if err := os.WriteFile(sumsPath, unsigned, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySumsSig(dir, kp.PublicKeyBytes()); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("no signature: %v", err)
	}
	// A signature the list does not name is a file the list does not name.
	if err := CheckSums(dir, present); err == nil || !strings.Contains(err.Error(), SumsSigName) {
		t.Fatalf("a signature its list does not name passed the list's check: %v", err)
	}
}
