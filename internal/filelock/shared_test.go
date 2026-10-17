package filelock

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A SHARED LOCK IS HELD BY MANY AND KEEPS THE EXCLUSIVE ONE OUT. Two
// readers of one entry hold it at once; the one writer that would remove
// the entry asks for the exclusive lock without waiting and is refused
// with the typed contention while either reader remains; once both close,
// it is taken.
func TestSharedHoldersCoexistAndKeepTheExclusiveOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	open := func() *os.File {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	a, b := open(), open()
	defer a.Close()
	defer b.Close()
	if err := RLock(a); err != nil {
		t.Fatalf("an unheld shared lock must be taken: %v", err)
	}
	if err := RLock(b); err != nil {
		t.Fatalf("a second shared holder must be admitted beside the first: %v", err)
	}
	w, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := Lock(w); !errors.Is(err, ErrHeld) {
		t.Fatalf("the exclusive lock while two share it: want ErrHeld, got %v", err)
	}
	a.Close()
	if err := Lock(w); !errors.Is(err, ErrHeld) {
		t.Fatalf("the exclusive lock while one still shares it: want ErrHeld, got %v", err)
	}
	b.Close()
	if err := Lock(w); err != nil {
		t.Fatalf("the exclusive lock once nobody shares it: %v", err)
	}
	// And the other way: a shared lock is refused while the exclusive one
	// is held.
	c := open()
	defer c.Close()
	if err := RLock(c); !errors.Is(err, ErrHeld) {
		t.Fatalf("a shared lock while the exclusive one is held: want ErrHeld, got %v", err)
	}
}

const (
	sharedChildEnv = "AII_FILELOCK_SHARED_CHILD"
	sharedPathEnv  = "AII_FILELOCK_SHARED_PATH"
)

// THE CROSS-PROCESS HALF, as the ledger's lock test does it: another process
// holds the shared lock; this one's exclusive try is refused; the holder's
// death releases it.
func TestASharedHolderInAnotherProcessKeepsTheExclusiveOutUntilItDies(t *testing.T) {
	if os.Getenv(sharedChildEnv) == "1" {
		f, err := os.Open(os.Getenv(sharedPathEnv))
		if err == nil {
			err = RLock(f)
		}
		if err != nil {
			fmt.Fprintf(os.Stdout, "ERROR %v\n", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "SHARED")
		for {
			time.Sleep(time.Hour)
			_ = f.Name() // keep the locked handle live until the parent kills us
		}
	}
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestASharedHolderInAnotherProcessKeepsTheExclusiveOutUntilItDies$")
	cmd.Env = append(os.Environ(), sharedChildEnv+"=1", sharedPathEnv+"="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "SHARED" {
		t.Fatalf("the child did not take the shared lock: line=%q err=%v stderr=%s", line, err, stderr.String())
	}
	// A shared lock of this process's own still comes in beside it.
	r, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RLock(r); err != nil {
		t.Fatalf("a shared lock beside another process's: %v", err)
	}
	r.Close()
	w, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := Lock(w); !errors.Is(err, ErrHeld) {
		t.Fatalf("the exclusive lock while another process shares it: want ErrHeld, got %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait() // the parent deliberately killed the holder
	waited = true
	if err := Lock(w); err != nil {
		t.Fatalf("the holder's death must release its shared lock: %v", err)
	}
}
