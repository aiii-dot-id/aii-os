//go:build !windows

package tools

import (
	"context"
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
// .
// .
// .
func TestEditRefusesAFIFOWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan Result, 1)
	go func() {
		res, _ := (&EditTool{}).Execute(context.Background(), map[string]interface{}{
			"file_path": path, "old_string": "a", "new_string": "b"})
		done <- res
	}()
	select {
	case res := <-done:
		if res.Error == "" || !strings.Contains(res.Error, "not a regular file") {
			t.Fatalf("a FIFO must be refused as what it is: %+v", res)
		}
	case <-time.After(2 * time.Second):
		// .
		if fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			defer syscall.Close(fd)
		}
		t.Fatal("edit parked on a FIFO: the turn is stranded with nothing to interrupt it")
	}
}

// .
// .
// .
// .
// .
func TestEditWriteBackTouchesOnlyTheFileItRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("alpha beta"), 0600); err != nil {
		t.Fatal(err)
	}
	f, read, err := openRegular(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	t.Run("another file", func(t *testing.T) {
		other := filepath.Join(dir, "other.txt")
		if err := os.WriteFile(other, []byte("somebody else's work"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(other, path); err != nil {
			t.Fatal(err)
		}
		err := rewriteSameFile(path, []byte("alpha gamma"), read)
		if err == nil || !strings.Contains(err.Error(), "no longer the file this edit read") {
			t.Fatalf("the swap must be refused and named: %v", err)
		}
		if got, _ := os.ReadFile(path); string(got) != "somebody else's work" {
			t.Fatalf("the file that was NOT read was touched: %q", got)
		}
	})

	t.Run("a FIFO", func(t *testing.T) {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- rewriteSameFile(path, []byte("alpha gamma"), read) }()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("a FIFO was accepted as the file that was read")
			}
		case <-time.After(2 * time.Second):
			if fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
				defer syscall.Close(fd)
			}
			t.Fatal("the write-back parked on a FIFO swapped in after the read")
		}
	})
}

// .
// .
func TestEditStillEditsAndKeepsTheFilesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("alpha beta"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	res, _ := (&EditTool{}).Execute(context.Background(), map[string]interface{}{
		"file_path": path, "old_string": "beta", "new_string": "gamma"})
	if res.Error != "" {
		t.Fatal(res.Error)
	}
	after, _ := os.Stat(path)
	if got, _ := os.ReadFile(path); string(got) != "alpha gamma" {
		t.Fatalf("content: %q", got)
	}
	if !os.SameFile(before, after) || after.Mode().Perm() != 0600 {
		t.Fatalf("the edit replaced the file or changed its mode: same=%v mode=%v", os.SameFile(before, after), after.Mode().Perm())
	}

	// .
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ = (&EditTool{}).Execute(ctx, map[string]interface{}{
		"file_path": path, "old_string": "gamma", "new_string": "delta"})
	if res.Error == "" || !strings.Contains(res.Error, "cancelled") {
		t.Fatalf("a cancelled edit must say so: %+v", res)
	}
	if got, _ := os.ReadFile(path); string(got) != "alpha gamma" {
		t.Fatalf("a cancelled edit wrote: %q", got)
	}
}
