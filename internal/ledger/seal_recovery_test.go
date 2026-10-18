package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSealRecoveryAtEveryPublicationBoundary(t *testing.T) {
	for _, step := range sealSteps {
		t.Run(step, func(t *testing.T) {
			path, kp := sealedFixture(t, 20, 10, 20)
			before := entryHashes(t, path)
			l, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			old := sealStep
			defer func() { sealStep = old }()
			sealStep = func(at string) error {
				if at == step {
					return syscall.EIO
				}
				return nil
			}
			err = l.Seal(10)
			sealStep = old
			if !errors.Is(err, syscall.EIO) {
				t.Fatalf("fault missed %s: %v", step, err)
			}
			published := step != "segment-written"
			if errors.Is(err, ErrSealRecoveryRequired) != published {
				t.Fatalf("wrong recovery boundary for %s: %v", step, err)
			}
			if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); err != nil {
				t.Fatal(err)
			}
			if got := entryHashes(t, path); !equalStrings(before, got) {
				t.Fatal("recovery changed signed history")
			}
			if (l.SealedSeq() == 10) != published {
				t.Fatalf("wrong sealed boundary: %d", l.SealedSeq())
			}
			if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"content": "after fault"}, kp); err != nil {
				t.Fatal(err)
			}
			if err := l.Seal(20); err != nil {
				t.Fatalf("next seal failed: %v", err)
			}
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			next, n, err := OpenVerified(path, kp.PublicKeyBytes(), &acceptHeads{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			if n != 21 {
				t.Fatalf("restart boundary: %d", n)
			}
		})
	}
}

func TestSealMatrixRowsWhereTheOperationItselfFails(t *testing.T) {
	pathIsADirectory := func(t *testing.T, l *Ledger) func() {
		saved := l.path + ".retained"
		if err := os.Rename(l.path, saved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(l.path, 0700); err != nil {
			t.Fatal(err)
		}
		return func() {
			if err := os.Remove(l.path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(saved, l.path); err != nil {
				t.Fatal(err)
			}
		}
	}
	segmentNameIsADirectory := func(t *testing.T, l *Ledger) func() {
		final := filepath.Join(l.dir, segmentName(1, 10))
		if err := os.Mkdir(final, 0700); err != nil {
			t.Fatal(err)
		}
		return func() {
			if err := os.Remove(final); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, row := range []struct {
		name      string
		at        string // the step marker that arranges the obstacle; "" arranges it before Seal
		published bool
		obstacle  func(*testing.T, *Ledger) func()
	}{
		{"segment temp write fails", "", false, pathIsADirectory},
		{"segment rename fails", "segment-written", false, segmentNameIsADirectory},
		{"tail temp write fails", "segment-published", true, pathIsADirectory},
	} {
		t.Run(row.name, func(t *testing.T) {
			path, kp := sealedFixture(t, 20, 10, 20)
			dir := filepath.Dir(path)
			before := entryHashes(t, path)
			l, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			var clear func()
			old := sealStep
			defer func() { sealStep = old }()
			if row.at == "" {
				clear = row.obstacle(t, l)
			} else {
				sealStep = func(step string) error {
					if step == row.at && clear == nil {
						clear = row.obstacle(t, l)
					}
					return nil
				}
			}
			err = l.Seal(10)
			sealStep = old
			if clear == nil {
				t.Fatalf("the obstacle was never arranged at %q", row.at)
			}
			clear()
			if err == nil {
				t.Fatal("a seal whose operation failed was acknowledged")
			}
			if errors.Is(err, ErrSealRecoveryRequired) != row.published {
				t.Fatalf("wrong recovery boundary: %v", err)
			}
			if got := tempNames(t, dir); len(got) != 0 {
				t.Fatalf("the failed operation left its temporary file: %v", got)
			}
			wantSegments, wantSealed := 0, uint64(0)
			if row.published {
				wantSegments, wantSealed = 1, 10
				if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"content": "not yet"}, kp); !errors.Is(err, ErrSealRecoveryRequired) {
					t.Fatalf("append before recovery: %v", err)
				}
				if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); err != nil {
					t.Fatal(err)
				}
			}
			if got := segmentNames(t, dir); len(got) != wantSegments || l.SealedSeq() != wantSealed {
				t.Fatalf("segments %v, sealed through %d", got, l.SealedSeq())
			}
			if got := entryHashes(t, path); !equalStrings(before, got) {
				t.Fatal("the failure changed signed history")
			}
			if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"content": "after " + row.name}, kp); err != nil {
				t.Fatalf("appends must continue: %v", err)
			}
			if err := l.Seal(20); err != nil {
				t.Fatalf("next seal: %v", err)
			}
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			next, n, err := OpenVerified(path, kp.PublicKeyBytes(), &acceptHeads{}, nil, nil)
			if err != nil {
				t.Fatalf("next open: %v", err)
			}
			defer next.Close()
			if n != 21 || next.LastSeq() != 21 || next.SealedSeq() != 20 {
				t.Fatalf("next open: count=%d last=%d sealed=%d", n, next.LastSeq(), next.SealedSeq())
			}
		})
	}
}

func TestSealRecoveryResumesAfterRelockFailureAndKeepsDirectoryOwnership(t *testing.T) {
	path, kp := sealedFixture(t, 20, 10, 20)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	old := sealStep
	defer func() { sealStep = old }()
	var blocker *os.File
	sealStep = func(at string) error {
		if at != "tail-published" {
			return nil
		}
		var err error
		blocker, err = os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := lockLedgerFile(blocker); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	err = l.Seal(10)
	sealStep = old
	if blocker == nil {
		t.Fatal("relock fault not reached")
	}
	defer blocker.Close()
	if !errors.Is(err, ErrSealRecoveryRequired) {
		t.Fatalf("relock failure lost: %v", err)
	}
	if other, err := New(path); err == nil {
		other.Close()
		t.Fatal("directory ownership was released by failed seal")
	}
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); err == nil || errors.Is(err, ErrTailIntegrity) {
		t.Fatalf("continued relock contention is not corruption or success: %v", err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"content": "relocked"}, kp); err != nil {
		t.Fatal(err)
	}
}

func TestSealRecoveryCannotDiscardAnAcknowledgedFinalFrame(t *testing.T) {
	path, kp := sealedFixture(t, 11, 10)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	interruptedSeal(t, l, 10)
	before := openingBytes(t, path)
	if len(before) == 0 || before[len(before)-1] != '\n' {
		t.Fatal("fixture lacks final frame")
	}
	broken := before[:len(before)-1]
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, ErrTailIntegrity) {
		t.Fatalf("lost acknowledgment treated as torn disposable bytes: %v", err)
	}
	if !bytes.Equal(broken, openingBytes(t, path)) {
		t.Fatal("acknowledged bytes were removed")
	}
	if sidecars, err := filepath.Glob(path + ".torn-*"); err != nil || len(sidecars) != 0 {
		t.Fatalf("refusal quarantined an acknowledged record: %v %v", sidecars, err)
	}
}

// Force a real rename failure AFTER segment publication, with no timing race
// and no reliance on permissions (tests also run as root). Restore the original
// file after Seal returns; only the operation's transient obstacle is removed.
func interruptedSeal(t *testing.T, l *Ledger, head uint64) {
	t.Helper()
	old := sealStep
	defer func() { sealStep = old }()
	var hit bool
	saved := l.path + ".retained"
	sealStep = func(step string) error {
		if step != "tail-written" || hit {
			return nil
		}
		hit = true
		if err := os.Rename(l.path, saved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(l.path, 0700); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	err := l.Seal(head)
	if !hit {
		t.Fatal("fault did not reach tail publication")
	}
	if err == nil {
		t.Fatal("failed rename was acknowledged")
	}
	if err := os.Remove(l.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, l.path); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(err, ErrSealRecoveryRequired) {
		t.Fatalf("publication failure was not recoverable: %v", err)
	}
}

func TestSealRecoveryPreservesHistoryAndResumesAppendAndCapture(t *testing.T) {
	path, kp := sealedFixture(t, 30, 10, 20, 30)
	before := entryHashes(t, path)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	interruptedSeal(t, l, 10)
	if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"id": "blocked", "content": "not yet"}, kp); !errors.Is(err, ErrSealRecoveryRequired) {
		t.Fatalf("unrecovered append: %v", err)
	}
	if _, err := l.Capture(filepath.Join(t.TempDir(), "tail")); !errors.Is(err, ErrSealRecoveryRequired) {
		t.Fatalf("unrecovered capture: %v", err)
	}
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); err != nil {
		t.Fatal(err)
	}
	if got := entryHashes(t, path); !equalStrings(before, got) {
		t.Fatal("recovery changed acknowledged history")
	}
	if l.SealedSeq() != 10 || l.LastSeq() != 30 || firstTailSeq(t, path) != 11 {
		t.Fatal("recovery published inconsistent container state")
	}
	if _, err := l.Capture(filepath.Join(t.TempDir(), "tail")); err != nil {
		t.Fatalf("capture after recovery: %v", err)
	}
	if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"id": "after", "content": "continued"}, kp); err != nil {
		t.Fatal(err)
	}
	// The next sealing must not create an overlapping segment set.
	if err := l.Seal(20); err != nil {
		t.Fatalf("next seal: %v", err)
	}
	if n, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err != nil || n != 31 {
		t.Fatalf("continued record: %d %v", n, err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	next, n, err := OpenVerified(path, kp.PublicKeyBytes(), &acceptHeads{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if n != 31 {
		t.Fatalf("restart lost events: %d", n)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSealRecoveryNeverClearsGenuineFreeze(t *testing.T) {
	path, kp := sealedFixture(t, 10, 10)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	interruptedSeal(t, l, 10)
	before := openingBytes(t, path)
	l.SetFrozen("genuine integrity failure")
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, ErrFrozen) {
		t.Fatalf("freeze cleared: %v", err)
	}
	if !bytes.Equal(before, openingBytes(t, path)) {
		t.Fatal("frozen recovery mutated evidence")
	}
}

func TestSealRecoveryVerifiesBeforeRemovingOverlap(t *testing.T) {
	path, kp := sealedFixture(t, 10, 10)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	interruptedSeal(t, l, 10)
	before := openingBytes(t, path)
	seg := filepath.Join(filepath.Dir(path), "segment-1-10.jsonl.gz")
	lines := gunzipLines(t, seg)
	var head Event
	if err := json.Unmarshal(lines[len(lines)-1], &head); err != nil {
		t.Fatal(err)
	}
	head.Sig = "AAAA"
	lines[len(lines)-1], err = json.Marshal(head)
	if err != nil {
		t.Fatal(err)
	}
	gzipLines(t, seg, lines)
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, ErrTailIntegrity) {
		t.Fatalf("bad proof accepted: %v", err)
	}
	if !bytes.Equal(before, openingBytes(t, path)) {
		t.Fatal("bad segment proof destroyed the signed overlap")
	}
	if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"content": "no"}, kp); !errors.Is(err, ErrFrozen) {
		t.Fatalf("bad proof did not freeze writer: %v", err)
	}
}

func TestSealRecoveryWaitsForProofWithoutDeclaringCorruption(t *testing.T) {
	path, kp := sealedFixture(t, 10, 10)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	interruptedSeal(t, l, 10)
	before := openingBytes(t, path)
	if err := l.RecoverSeal(kp.PublicKeyBytes(), nil); !errors.Is(err, ErrSealedWithoutWitness) || errors.Is(err, ErrTailIntegrity) {
		t.Fatalf("missing proof was misclassified: %v", err)
	}
	if !bytes.Equal(before, openingBytes(t, path)) {
		t.Fatal("missing proof changed history")
	}
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); err != nil {
		t.Fatalf("proof restored but recovery latched off: %v", err)
	}
}

func TestSealRecoveryPreservesUnacknowledgedTornSuffixBeforeTrimming(t *testing.T) {
	path, kp := sealedFixture(t, 10, 10)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	interruptedSeal(t, l, 10)
	suffix := []byte(`{"seq":11,"unfinished":`)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(suffix)
	if err = errors.Join(err, f.Close()); err != nil {
		t.Fatal(err)
	}
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); err != nil {
		t.Fatal(err)
	}
	sidecars, err := filepath.Glob(path + ".torn-*")
	if err != nil || len(sidecars) != 1 {
		t.Fatalf("suffix was not preserved: %v %v", sidecars, err)
	}
	got, err := os.ReadFile(sidecars[0])
	if err != nil || !bytes.Equal(got, suffix) {
		t.Fatalf("suffix changed: %q %v", got, err)
	}
	if len(openingBytes(t, path)) != 0 || l.LastSeq() != 10 || l.SealedSeq() != 10 {
		t.Fatal("bounded recovery lost its verified boundary")
	}
	if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"content": "after torn suffix"}, kp); err != nil {
		t.Fatal(err)
	}
}

func TestSealRecoveryRetriesOperationalReadFailureWithoutFreezing(t *testing.T) {
	path, kp := sealedFixture(t, 10, 10)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	interruptedSeal(t, l, 10)
	before := openingBytes(t, path)
	saved := path + ".unavailable"
	if err := os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrTailIntegrity) {
		t.Fatalf("I/O refusal: %v", err)
	}
	if err := os.Rename(saved, path); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, openingBytes(t, path)) {
		t.Fatal("read failure modified history")
	}
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); err != nil {
		t.Fatalf("I/O failure latched: %v", err)
	}
}

func TestSealRecoveryCannotReopenAClosedLedger(t *testing.T) {
	path, kp := sealedFixture(t, 10, 10)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	interruptedSeal(t, l, 10)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	before := openingBytes(t, path)
	if err := l.RecoverSeal(kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("reopened after ownership ended: %v", err)
	}
	if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"content": "closed"}, kp); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("append after close: %v", err)
	}
	if err := l.Seal(10); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("seal after close: %v", err)
	}
	if _, err := l.Capture(filepath.Join(t.TempDir(), "tail")); !errors.Is(err, ErrCaptureRefused) {
		t.Fatalf("capture after close: %v", err)
	}
	if !bytes.Equal(before, openingBytes(t, path)) {
		t.Fatal("closed ledger changed its former files")
	}
	next, _, err := OpenVerified(path, kp.PublicKeyBytes(), &acceptHeads{}, nil, nil)
	if err != nil {
		t.Fatalf("successor cannot recover: %v", err)
	}
	defer next.Close()
}
