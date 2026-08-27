package ledger

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

const (
	lockChildEnv = "AII_OS_LEDGER_LOCK_CHILD"
	lockPathEnv  = "AII_OS_LEDGER_LOCK_PATH"
)

func TestLedgerLockExcludesProcessAndReleasesOnDeath(t *testing.T) {
	if os.Getenv(lockChildEnv) == "1" {
		lg, err := New(os.Getenv(lockPathEnv))
		if err != nil {
			fmt.Fprintf(os.Stdout, "ERROR %v\n", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "LOCKED")
		for {
			time.Sleep(time.Hour)
			_ = lg.Path() // keep the locked handle live until the parent kills us
		}
	}

	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLedgerLockExcludesProcessAndReleasesOnDeath$")
	cmd.Env = append(os.Environ(), lockChildEnv+"=1", lockPathEnv+"="+path)
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
	if err != nil || strings.TrimSpace(line) != "LOCKED" {
		t.Fatalf("child did not acquire ledger lock: line=%q err=%v stderr=%s", line, err, stderr.String())
	}
	if second, err := New(path); !errors.Is(err, ErrLedgerInUse) {
		if err == nil {
			second.Close()
		}
		t.Fatalf("second process must receive typed contention, got %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait() // the parent deliberately killed the lock owner
	waited = true

	lg, err := New(path)
	if err != nil {
		t.Fatalf("process death left stale lock state: %v", err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
}
