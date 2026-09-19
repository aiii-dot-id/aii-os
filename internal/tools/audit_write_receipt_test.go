// .
// .
// .
// .
// .
// .

//go:build linux

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// .
// .
// .
func TestAuditWriteReceiptDoesNotReadARefusedSymlink(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fixture.pipe")
	link := filepath.Join(dir, "output.txt")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(dir, nil, Timeouts{})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		res, err := reg.Execute(ctx, "write", map[string]interface{}{"file_path": link, "content": "replacement"})
		if err != nil {
			res.Error = err.Error()
		}
		done <- res
	}()
	var got Result
	select {
	case got = <-done:
		if !strings.Contains(got.Error, "symlink") {
			t.Fatalf("expected protected write refusal, got %+v", got)
		}
		t.Log("write refused symlink without opening the FIFO")
	case <-time.After(250 * time.Millisecond):
		t.Errorf("write is still blocked in receipt read after context expired: %v", ctx.Err())
		// .
		// .
		fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0600)
		if err != nil {
			t.Fatalf("fixture reader cleanup failed: %v", err)
		}
		if err = syscall.Close(fd); err != nil {
			t.Fatal(err)
		}
		select {
		case got = <-done:
			t.Logf("only returned after fixture writer supplied EOF: %s", got.Error)
		case <-time.After(2 * time.Second):
			t.Fatal("fixture reader did not settle")
		}
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Error("expected expired fixture context")
		}
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal(err)
	}
}
