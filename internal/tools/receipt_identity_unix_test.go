//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
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
	// .
	if same, err := writeFileNoFollowSame(path, []byte("x"), 0o644, measured); err != nil || !same {
		t.Fatalf("the file that was measured was replaced, and the write says otherwise: same=%v err=%v", same, err)
	}
	// .
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte("a different file entirely\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, path); err != nil {
		t.Fatal(err)
	}
	if same, err := writeFileNoFollowSame(path, []byte("y"), 0o644, measured); err != nil || same {
		t.Fatalf("a different file was replaced and the write claims the measured one: same=%v err=%v", same, err)
	}
	// .
	fresh := filepath.Join(dir, "fresh.txt")
	if same, err := writeFileNoFollowSame(fresh, []byte("z"), 0o644, nil); err != nil || !same {
		t.Fatalf("a file this write created: same=%v err=%v", same, err)
	}
	if same, err := writeFileNoFollowSame(fresh, []byte("zz"), 0o644, nil); err != nil || same {
		t.Fatalf("a file that appeared after the measurement was called new: same=%v err=%v", same, err)
	}
	// .
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFileNoFollowSame(link, []byte("q"), 0o644, nil); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("a write through a symlink: %v", err)
	}
}

func TestTheWriteToolsReceiptThroughTheRegistry(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root, nil, Timeouts{})
	path := filepath.Join(root, "notes.txt")
	res, err := reg.Execute(context.Background(), "write", map[string]interface{}{"file_path": path, "content": "one\ntwo\nthree\n"})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "(new file)") {
		t.Fatalf("a new file: %+v %v", res, err)
	}
	res, err = reg.Execute(context.Background(), "write", map[string]interface{}{"file_path": path, "content": "one\n"})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "replaced 14 bytes") || !strings.Contains(res.Output, "−2 lines") {
		t.Fatalf("a replacement: %+v %v", res, err)
	}
}
