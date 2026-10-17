//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheWriteSaysWhetherItReplacedWhatWasMeasured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	measured, size, lines, err := previousFile(context.Background(), path)
	if err != nil || size != 8 || lines != 2 || measured == nil {
		t.Fatalf("measure: %v %d %d %v", measured != nil, size, lines, err)
	}
	// Unchanged in between: the same file.
	if same, made, err := writeFileNoFollowSame(path, []byte("x"), 0o644, measured, nil); err != nil || !same || made != nil {
		t.Fatalf("the file that was measured was replaced, and the write says otherwise: same=%v made=%v err=%v", same, made != nil, err)
	}
	// Swapped in between — another file now sits at the path.
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte("a different file entirely\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, path); err != nil {
		t.Fatal(err)
	}
	if same, made, err := writeFileNoFollowSame(path, []byte("y"), 0o644, measured, nil); err != nil || same || made != nil {
		t.Fatalf("a different file was replaced and the write claims the measured one, or made it: same=%v made=%v err=%v", same, made != nil, err)
	}
	// Nothing was measured: "new" only if this write created it.
	fresh := filepath.Join(dir, "fresh.txt")
	if same, made, err := writeFileNoFollowSame(fresh, []byte("z"), 0o644, nil, nil); err != nil || !same || made == nil {
		t.Fatalf("a file this write created: same=%v made=%v err=%v", same, made != nil, err)
	}
	if same, made, err := writeFileNoFollowSame(fresh, []byte("zz"), 0o644, nil, nil); err != nil || same || made != nil {
		t.Fatalf("a file that appeared after the measurement was called new, or made: same=%v made=%v err=%v", same, made != nil, err)
	}
	// And a symlink at the final component is still refused.
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writeFileNoFollowSame(link, []byte("q"), 0o644, nil, nil); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("a write through a symlink: %v", err)
	}
}

func TestTheWriteToolsReceiptThroughTheRegistry(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root, nil, Timeouts{})
	path := filepath.Join(root, "notes.txt")
	res, err := reg.Execute(context.Background(), "write", map[string]any{"file_path": path, "content": "one\ntwo\nthree\n"})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "(new file)") {
		t.Fatalf("a new file: %+v %v", res, err)
	}
	res, err = reg.Execute(context.Background(), "write", map[string]any{"file_path": path, "content": "one\n"})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "replaced 14 bytes") || !strings.Contains(res.Output, "−2 lines") {
		t.Fatalf("a replacement: %+v %v", res, err)
	}
}
