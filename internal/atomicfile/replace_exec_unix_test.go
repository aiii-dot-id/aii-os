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

	// Nothing moved: the target still holds what it held, and the
	// caller is free to retire the swap's state.
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

func TestAProgramWithAnotherNameIsUpdatedAndNotEmptied(t *testing.T) {
	for _, mode := range []os.FileMode{0o555, 0o755} {
		dir := t.TempDir()
		src := filepath.Join(dir, "aii.new")
		dst := filepath.Join(dir, "aii")
		other := filepath.Join(dir, "aii-elsewhere")
		if err := os.WriteFile(src, []byte("the new program"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("the earlier program"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(dst, other); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		if published, err := ReplaceExecutable(src, dst); err != nil || !published {
			t.Errorf("mode %o: the update of a program with another name lands: published=%v err=%v", mode, published, err)
			continue
		}
		if got, _ := os.ReadFile(dst); string(got) != "the new program" {
			t.Errorf("mode %o: the program's path holds the new program: %q", mode, got)
		}
		if got, _ := os.ReadFile(other); string(got) != "the earlier program" {
			t.Errorf("mode %o: the other name keeps the earlier image, which is not emptied: %q", mode, got)
		}
	}
}
