//go:build darwin

package broker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A PLUGIN'S FILES DO NOT REACH A FILE NAMED BY ITS VOLUME AND INODE. On
// macOS /.vol/<volume>/<inode> opens a file or directory by what it is,
// and the wall cannot judge it by place: the key named by its directory's
// inode was refused only as outside the sandbox, and a file of the home
// named by the home's inode was read. Every operation now answers the
// wall's refusal, FS_PROTECTED, saying what the path is, and carries
// nothing of the file. The control half: the same file by its path in the
// home is read.
func TestAPluginsFilesDoNotReachAFileNamedByItsVolumeAndInode(t *testing.T) {
	home, key, _, b := hardLinkWorld(t)
	notes := filepath.Join(home, "notes", "one.txt")
	if err := os.WriteFile(notes, []byte("ONE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	byInode := func(path string) string {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		st := info.Sys().(*syscall.Stat_t)
		at := fmt.Sprintf("/.vol/%d/%d", st.Dev, st.Ino)
		if other, err := os.Stat(at); err != nil || !os.SameFile(info, other) {
			t.Skipf("this volume does not open %s by its inode (%v)", path, err)
		}
		return at
	}
	for _, path := range []string{byInode(filepath.Dir(key)) + "/identity.sec", byInode(key), byInode(home) + "/notes/one.txt"} {
		for what, params := range map[string]string{
			"fs.read":    fsParams("fs.read", "sandbox", path, ""),
			"fs.write":   fsParams("fs.write", "sandbox", path, `{"data":"FORGED"}`),
			"fs.publish": fsParams("fs.publish", "sandbox", path, `{"data":"FORGED"}`),
			"fs.delete":  fsParams("fs.delete", "sandbox", path, ""),
		} {
			m := dispatch(t, b, params)
			raw, ok := refusedProtected(m)
			if !ok || strings.Contains(raw, "ONE") || !strings.Contains(raw, "names a file by the number of its volume and of its inode") {
				t.Errorf("%s of %s: denied, FS_PROTECTED, saying what the path is, nothing of the file carried: %s", what, path, raw)
			}
		}
	}
	for path, want := range map[string]string{key: "INSIDE\n", notes: "ONE\n"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s was changed: %q %v", path, got, err)
		}
	}
	m := wantResult(t, dispatch(t, b, fsParams("fs.read", "sandbox", "notes/one.txt", "")), statusSucceeded, "")
	if raw, _ := json.Marshal(m); strings.Contains(string(raw), "names a file by") {
		t.Errorf("the file by its path was refused: %s", raw)
	}
}
