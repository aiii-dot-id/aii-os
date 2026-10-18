package genesis

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// NamedKey is the key the key door puts back by (internal/continuity,
// key_back.go): the key a record's birth names. It reads record 1 alone and
// gives that key and its fingerprint, or the refusal the stranger's path
// gives for the same first record (VerifySelfContained asks it first).
func TestTheKeyARecordNamesIsItsBirthsKey(t *testing.T) {
	dir := t.TempDir()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	born := write(t, dir, "born.jsonl",
		genesisLine(t, "ring0.genesis", base64.StdEncoding.EncodeToString(kp.PublicKeyBytes()), kp.Fingerprint()),
		genesisLine(t, "belief.add", "not read", "not read"))
	pub, fp, err := NamedKey(born)
	if err != nil || !bytes.Equal(pub, kp.PublicKeyBytes()) || fp != kp.Fingerprint() {
		t.Fatalf("the key a birth names is not the one given: %x %q %v, want %s", pub, fp, err, kp.Fingerprint())
	}
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(dir, "absent.jsonl"), "read ledger"},
		{write(t, dir, "empty.jsonl"), "empty ledger"},
		{write(t, dir, "notbirth.jsonl", genesisLine(t, "belief.add", "AAAA", "ff")), "not a birth-headed chain"},
		{write(t, dir, "nokey.jsonl", genesisLine(t, "ring0.genesis", "", "ff")), "not self-contained"},
		{write(t, dir, "wrongfp.jsonl", genesisLine(t, "ring0.genesis", base64.StdEncoding.EncodeToString(kp.PublicKeyBytes()), "00")), "does not match its claimed fingerprint"},
	} {
		if pub, fp, err := NamedKey(tc.path); err == nil || !strings.Contains(err.Error(), tc.want) || pub != nil || fp != "" {
			t.Errorf("%s: A RECORD THAT NAMES NO KEY GAVE ONE, or its refusal does not name why: %x %q %v (want %q)", filepath.Base(tc.path), pub, fp, err, tc.want)
		}
	}
}
