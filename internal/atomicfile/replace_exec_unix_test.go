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
	if published, err := ReplaceExecutable(src, dst); err != nil || !published {
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
	if published, err := ReplaceExecutable(filepath.Join(dir, "absent"), filepath.Join(dir, "aii")); err == nil || published {
		t.Fatal("publishing a missing source must fail")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestReplaceExecutableReportsWhetherItPublished(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "staged")
	dst := filepath.Join(dir, "aii")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	published, err := ReplaceExecutable(src, dst)
	if err != nil || !published {
		t.Fatalf("a successful publish must report published=true, got %v %v", published, err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "new" {
		t.Fatalf("target holds %q, want the new image", got)
	}

	// .
	// .
	published, err = ReplaceExecutable(filepath.Join(dir, "absent"), dst)
	if err == nil {
		t.Fatal("a missing source must fail")
	}
	if published {
		t.Error("a publish that never happened must report published=false, or the caller keeps recovery state it does not need — and worse, learns to distrust the flag")
	}
	if got, _ := os.ReadFile(dst); string(got) != "new" {
		t.Fatalf("a failed publish disturbed the target: %q", got)
	}
}
