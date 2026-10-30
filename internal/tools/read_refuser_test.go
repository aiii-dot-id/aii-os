package tools

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAReadTheOperatingSystemDeniedNamesTheRefuser(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "/x/note.txt", Err: fs.ErrPermission}
	if got, want := osRefused(denied), "open /x/note.txt: permission denied — the operating system refused it, not AII's wall"; got != want {
		t.Fatalf("a denied open reads %q, want %q", got, want)
	}
	missing := &fs.PathError{Op: "stat", Path: "/x/none.txt", Err: fs.ErrNotExist}
	if got := osRefused(missing); got != missing.Error() {
		t.Fatalf("a failure that is no refusal reads %q, want it unchanged", got)
	}
}

// The same through the tool, where the account can be refused a file
// (root and Windows are not).
func TestReadOfAFileOfModeZeroNamesTheOperatingSystem(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("mode 000 refuses no read to root, nor on Windows")
	}
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("harmless\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	res, err := (&ReadTool{maxBytes: 1024}).Execute(context.Background(), map[string]any{"file_path": path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.Error, "permission denied — the operating system refused it, not AII's wall") || res.Output != "" {
		t.Fatalf("a read of a mode-000 file returned %+v", res)
	}
}
