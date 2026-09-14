package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestWriteThroughSymlinkedParentRejected(t *testing.T) {
	sandbox := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(sandbox, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("no symlink support here: %v", err)
	}

	r := NewRegistry(sandbox, nil, Timeouts{})
	res, err := r.Execute(context.Background(), "write", map[string]interface{}{
		"file_path": filepath.Join(sandbox, "link", "escape.txt"),
		"content":   "escaped",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "outside sandbox") {
		t.Fatalf("new-file write under a symlinked parent must be denied as outside the sandbox, got %+v", res)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.txt")); statErr == nil {
		t.Fatal("the write LANDED outside the sandbox — symlink escape")
	}
}

// .
// .
// .
func TestNewFileInNestedNewDirsStillAllowed(t *testing.T) {
	sandbox := t.TempDir()
	r := NewRegistry(sandbox, nil, Timeouts{})

	// .
	deep := filepath.Join(sandbox, "a", "b")
	if err := os.MkdirAll(deep, 0750); err != nil {
		t.Fatal(err)
	}
	res, err := r.Execute(context.Background(), "write", map[string]interface{}{
		"file_path": filepath.Join(deep, "new.txt"),
		"content":   "hello",
	})
	if err != nil || res.Error != "" {
		t.Fatalf("nested new file inside the sandbox must be allowed: %v %+v", err, res)
	}

	// .
	// .
	res, err = r.Execute(context.Background(), "write", map[string]interface{}{
		"file_path": filepath.Join(sandbox, "x", "y", "new.txt"),
		"content":   "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Error, "outside sandbox") {
		t.Fatalf("new file under new sandbox dirs must not be denied as outside, got %+v", res)
	}
}

// .
// .
// .
// .
func TestExistingSymlinkReadBehaviorUnchanged(t *testing.T) {
	sandbox := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sandbox, "alias.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("no symlink support here: %v", err)
	}

	r := NewRegistry(sandbox, nil, Timeouts{})
	res, err := r.Execute(context.Background(), "read", map[string]interface{}{"file_path": link})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("read through an escaping symlink must stay denied")
	}

	real := filepath.Join(sandbox, "real.txt")
	if err := os.WriteFile(real, []byte("fine"), 0600); err != nil {
		t.Fatal(err)
	}
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": real})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "fine") {
		t.Fatalf("plain sandbox read must keep working: %v %+v", err, res)
	}
}

// .
// .
// .
// .
func TestWriteToFinalComponentSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("O_NOFOLLOW hardening is unix-only")
	}
	sandbox := t.TempDir()
	real := filepath.Join(sandbox, "real.txt")
	if err := os.WriteFile(real, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sandbox, "alias.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlink support here: %v", err)
	}

	r := NewRegistry(sandbox, nil, Timeouts{})
	res, err := r.Execute(context.Background(), "write", map[string]interface{}{
		"file_path": link,
		"content":   "y",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("write to a symlink final component must be refused (O_NOFOLLOW)")
	}
}
