package ledger

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

func TestBirthOpeningChecksForRecordsUnderTheDirectoryLock(t *testing.T) {
	for _, artifact := range []string{"ledger.jsonl", "segment-1-4.jsonl.gz"} {
		t.Run(artifact, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ledger.jsonl")
			if err := os.WriteFile(filepath.Join(dir, artifact), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			l, err := NewForBirth(path)
			if l != nil {
				l.Close()
			}
			if !errors.Is(err, ErrRecordExists) {
				t.Fatalf("birth opening did not refuse the existing record: %v", err)
			}
			if body, err := os.ReadFile(filepath.Join(dir, artifact)); err != nil || string(body) != "original" {
				t.Fatalf("birth opening changed the record: %q %v", body, err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l, err := NewForBirth(path)
	if err != nil {
		t.Fatalf("empty birth opening refused: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRewrapCannotReplaceARecordUnderTheOpeningDirectoryLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventRing0Genesis, 0, genesisPayload(kp, "lock-test"), kp); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockLedgerDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	_, refused := Rewrap(path, kp, "", acceptReplay)
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(refused, ErrLedgerInUse) {
		t.Fatalf("rewrap escaped opening's directory lock: %v", refused)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("refused rewrap changed the record: %v", err)
	}
	if _, err := Rewrap(path, kp, "", acceptReplay); err != nil {
		t.Fatalf("rewrap remained blocked after opening released its lock: %v", err)
	}
	output := filepath.Join(t.TempDir(), "ledger.jsonl")
	outputLock, err := lockLedgerDir(filepath.Dir(output))
	if err != nil {
		t.Fatal(err)
	}
	_, refused = Rewrap(path, kp, output, acceptReplay)
	if err := outputLock.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(refused, ErrLedgerInUse) {
		t.Fatalf("rewrap escaped output opening's directory lock: %v", refused)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused rewrap published output: %v", err)
	}
	if _, err := Rewrap(path, kp, output, acceptReplay); err != nil {
		t.Fatalf("rewrap remained blocked after output opening released its lock: %v", err)
	}
}
