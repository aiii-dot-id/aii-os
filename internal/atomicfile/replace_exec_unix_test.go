//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceExecutablePublishesOverExistingTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "aii.new")
	dst := filepath.Join(dir, "aii")
	if err := os.WriteFile(src, []byte("restored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("failed image"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceExecutable(src, dst); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "restored" {
		t.Fatalf("target holds %q (err %v), want the restored bytes", got, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source must be consumed by the publish")
	}
}

func TestReplaceExecutableMissingSourceFails(t *testing.T) {
	dir := t.TempDir()
	if err := ReplaceExecutable(filepath.Join(dir, "absent"), filepath.Join(dir, "aii")); err == nil {
		t.Fatal("publishing a missing source must fail")
	}
}
