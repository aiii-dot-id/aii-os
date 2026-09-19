//go:build linux

// .
// .
// .
// .

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
func TestAuditWriteReopenRefusesSwappedFIFO(t *testing.T) {
	for _, measuredFile := range []bool{true, false} {
		t.Run(map[bool]string{true: "measured-file", false: "measured-absent"}[measuredFile], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "output")
			var measured os.FileInfo
			if measuredFile {
				if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				measured, _, _, err = previousFile(context.Background(), path)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := syscall.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := writeFileNoFollowSame(path, []byte("replacement"), 0600, measured); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("special-file destination was accepted")
				}
				t.Logf("refused swapped FIFO: %v", err)
			case <-time.After(250 * time.Millisecond):
				t.Errorf("final write open blocked beyond caller deadline: %v", ctx.Err())
				// .
				// .
				fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer syscall.Close(fd)
				select {
				case err := <-done:
					t.Logf("settled only after fixture reader opened: %v", err)
				case <-time.After(2 * time.Second):
					t.Fatal("fixture writer did not settle")
				}
			}
		})
	}
}
