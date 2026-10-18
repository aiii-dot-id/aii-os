package ledger

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// buildLedger writes n real signed events and returns path + key.
func buildLedger(t *testing.T, n int) (string, *crypto.KeyPair) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := l.Append("experience.create", 4, map[string]any{"i": i}, kp); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	return path, kp
}

func TestTornTrailingLineIsForgiven(t *testing.T) {
	path, kp := buildLedger(t, 5)
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	f.WriteString(`{"seq":6,"prev_hash":"abc","type":"experience.create","content_ha`) // torn, no newline
	f.Close()

	l, n, err := OpenVerified(path, kp.PublicKeyBytes(), nil, nil, nil) // reopen as boot does
	if err != nil {
		t.Fatalf("torn tail must be survivable, got: %v", err)
	}
	if n != 5 || l.LastSeq() != 5 {
		t.Fatalf("verified %d, lastSeq=%d, want 5 (torn line dropped)", n, l.LastSeq())
	}
	// The file now ends clean, and a fresh Append + VerifyChain succeed.
	if _, err := l.Append("experience.create", 4, map[string]any{"after": true}, kp); err != nil {
		t.Fatalf("append after torn-tail recovery: %v", err)
	}
	l.Close()
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), nil); err != nil {
		t.Fatalf("chain must verify after recovery: %v", err)
	}
}

func TestCompleteJSONWithoutNewlineIsTorn(t *testing.T) {
	path, kp := buildLedger(t, 2)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.TrimSuffix(data, []byte("\n")), 0600); err != nil {
		t.Fatal(err)
	}

	l, n, err := OpenVerified(path, kp.PublicKeyBytes(), nil, nil, nil)
	if err != nil {
		t.Fatalf("complete JSON without its framing newline must recover: %v", err)
	}
	if n != 1 || l.LastSeq() != 1 {
		t.Fatalf("verified %d, lastSeq=%d, want 1 (unterminated event dropped)", n, l.LastSeq())
	}
	if _, err := l.Append("experience.create", 4, map[string]any{"after": true}, kp); err != nil {
		t.Fatalf("append after recovery: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), nil); err != nil {
		t.Fatalf("chain must verify after recovery: %v", err)
	}
	matches, err := filepath.Glob(path + ".torn-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("torn event quarantine = %v, %v; want one sidecar", matches, err)
	}
}

func TestNewRefusesATornTail(t *testing.T) {
	for _, kind := range []string{"partial", "signed-no-newline"} {
		t.Run(kind, func(t *testing.T) {
			path, kp := buildLedger(t, 3)
			whole, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			input := append(append([]byte(nil), whole...), `{"seq":4,"prev_hash":"abc","type":"experience.create","content_ha`...)
			want := 3
			if kind == "signed-no-newline" {
				input, want = bytes.TrimSuffix(whole, []byte("\n")), 2
			}
			if err := os.WriteFile(path, input, 0600); err != nil {
				t.Fatal(err)
			}

			l, err := New(path)
			if l != nil {
				l.Close()
				t.Fatal("New opened a torn tail")
			}
			if !errors.Is(err, ErrRecordUnreadable) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "OpenVerified") {
				t.Fatalf("refusal must be ErrRecordUnreadable naming the file and OpenVerified: %v", err)
			}
			if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, input) {
				t.Fatalf("the refusal changed the tail (%v)", err)
			}
			if sidecars, err := filepath.Glob(path + ".torn-*"); err != nil || len(sidecars) != 0 {
				t.Fatalf("the refusal wrote a sidecar: %v %v", sidecars, err)
			}

			v, n, err := OpenVerified(path, kp.PublicKeyBytes(), nil, nil, nil)
			if err != nil {
				t.Fatalf("the verified opener must recover what New refused: %v", err)
			}
			defer v.Close()
			if n != want || v.LastSeq() != uint64(want) {
				t.Fatalf("recovered count=%d last=%d, want %d", n, v.LastSeq(), want)
			}
		})
	}
}

// TestInteriorCorruptionStaysFatal: a garbled line that is NOT last is
// real corruption — boot must refuse (the operator sees SAFE), never
// silently skip it.
func TestInteriorCorruptionStaysFatal(t *testing.T) {
	path, _ := buildLedger(t, 5)
	data, _ := os.ReadFile(path)
	lines := strings.SplitAfter(string(data), "\n")
	lines[2] = "{garbage not json}\n" // corrupt an interior line
	os.WriteFile(path, []byte(strings.Join(lines, "")), 0600)

	if _, err := New(path); err == nil {
		t.Fatal("interior corruption must fail readChainState, not be forgiven")
	}
}

// TestKillMidAppendRecovers is the real OS-kill test: a child appends in
// a loop and is SIGKILLed; the parent reopens and proves the chain is
// intact up to some seq, with at most one torn tail dropped — never
// corrupt, never a dead runtime.
func TestKillMidAppendRecovers(t *testing.T) {
	if os.Getenv("LEDGER_CRASH_CHILD") == "1" {
		// child: append forever until killed
		path := os.Getenv("LEDGER_CRASH_PATH")
		kp, _ := crypto.GenerateKeyPair()
		os.WriteFile(os.Getenv("LEDGER_CRASH_KEY"), []byte(kp.Fingerprint()), 0600)
		l, err := New(path)
		if err != nil {
			os.Exit(2)
		}
		writeKeyBytes(os.Getenv("LEDGER_CRASH_PUB"), kp)
		for i := 0; ; i++ {
			if _, err := l.Append("experience.create", 4, map[string]any{"i": i}, kp); err != nil {
				os.Exit(3)
			}
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	pubPath := filepath.Join(dir, "pub.bin")
	cmd := exec.Command(os.Args[0], "-test.run", "TestKillMidAppendRecovers")
	cmd.Env = append(os.Environ(),
		"LEDGER_CRASH_CHILD=1", "LEDGER_CRASH_PATH="+path,
		"LEDGER_CRASH_KEY="+filepath.Join(dir, "fp"), "LEDGER_CRASH_PUB="+pubPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Let it write, then SIGKILL mid-flight (no cleanup, the hard case).
	waitForLines(t, path, 20)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()

	pub, err := os.ReadFile(pubPath)
	if err != nil || len(pub) == 0 {
		t.Skip("child did not record its key before the kill")
	}
	// Reopen as boot does: must NOT error (torn tail at most).
	l, verified, err := OpenVerified(path, pub, nil, nil, nil)
	if err != nil {
		t.Fatalf("reopen after SIGKILL must survive: %v", err)
	}
	survived := l.LastSeq()
	l.Close()
	if uint64(verified) != survived {
		t.Fatalf("verified %d records but the writable boundary is %d", verified, survived)
	}
	if survived < 10 {
		t.Fatalf("only %d events survived a kill after 20+ writes — durability suspect", survived)
	}
	// And the surviving chain verifies end to end.
	if _, err := VerifyChain(path, pub, nil); err != nil {
		t.Fatalf("chain after SIGKILL must verify (seq 1..%d): %v", survived, err)
	}
	t.Logf("SIGKILL survived: %d events intact and verified", survived)
}

func writeKeyBytes(path string, kp *crypto.KeyPair) {
	os.WriteFile(path, kp.PublicKeyBytes(), 0600)
}

func waitForLines(t *testing.T, path string, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if strings.Count(string(data), "\n") >= n {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("child never reached " + strconv.Itoa(n) + " events")
}
