// .
// .
// .
// .

//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// .
// .
// .
func TestReviewWriteReceiptMustNotReadASymlinkFIFO(t *testing.T) {
	root := t.TempDir()
	fifo, link := filepath.Join(root, "fifo"), filepath.Join(root, "alias")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(root, nil, Timeouts{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = reg.Execute(ctx, "write", map[string]interface{}{"file_path": link, "content": "replacement"})
	}()
	select {
	case <-done:
		return
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
		return
	case <-time.After(100 * time.Millisecond):
	}
	// .
	fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_ = syscall.Close(fd)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not release write")
	}
	t.Fatal("write followed the final symlink and blocked in receipt generation; cancellation did not stop it")
}
