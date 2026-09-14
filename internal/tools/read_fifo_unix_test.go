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
// .
// .
// .
// .
func TestReviewReadOfFIFOIsRefusedOrCancellable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tool := &ReadTool{maxBytes: 51200}
		_, _ = tool.Execute(ctx, map[string]interface{}{"file_path": p})
		close(done)
	}()
	time.Sleep(25 * time.Millisecond)
	cancel()
	select {
	case <-done:
		return
	case <-time.After(200 * time.Millisecond):
		// .
		go func() {
			f, err := os.OpenFile(p, os.O_WRONLY, 0)
			if err == nil {
				_, _ = f.WriteString("release\n")
				_ = f.Close()
			}
		}()
		<-done
		t.Fatal("read remained blocked on a FIFO after its context was cancelled")
	}
}
