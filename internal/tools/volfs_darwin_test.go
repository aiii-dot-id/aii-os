//go:build darwin

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// byInode is the path macOS opens path's file by, by the number of its
// volume and of its inode (/.vol/<volume>/<inode>), asked of the system
// and never written down. The test stops where the volume does not open
// it as the same file.
func byInode(t *testing.T, path string) string {
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

func TestAPathByVolumeAndInodeIsRefusedWhateverTheSandboxsState(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(body string, parts ...string) string {
		t.Helper()
		p := filepath.Join(parts...)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	home, elsewhere := filepath.Join(base, "home"), filepath.Join(base, "elsewhere")
	notes := put("home MARK\n", home, "notes.txt")
	credential := put("KEPT credential\n", home, "data", "credentials", "k.key")
	open := put("outside MARK\n", elsewhere, "open.txt")
	ownVol := put("own MARK\n", home, ".vol", "notes.txt")
	if err := os.Symlink(elsewhere, filepath.Join(home, "lnk")); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.data", Kind: firewall.KindSubstrate, Path: filepath.Join(home, "data"), Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})

	credentials := filepath.Dir(credential)
	dirInKept := byInode(t, credentials)
	paths := map[string]string{
		"a file in a directory inside a kept one, by the directory's inode": dirInKept + "/k.key",
		"a file inside a kept directory, by its own inode":                  byInode(t, credential),
		"a file a link in the home leads to, by the home's inode":           byInode(t, home) + "/lnk/open.txt",
		"a file of the home, by the home's inode":                           byInode(t, home) + "/notes.txt",
	}
	fresh := dirInKept + "/forged.json"
	unplaced := func(res Result) bool {
		said := res.Error + res.Output
		return res.Refused && strings.Contains(said, "names a file by the number of its volume and of its inode") &&
			!strings.Contains(said, "KEPT") && !strings.Contains(said, "MARK")
	}

	for _, off := range []bool{false, true} {
		r.SetSandboxOff(off)
		for what, path := range paths {
			for tool, args := range map[string]map[string]any{
				"read":   {"file_path": path},
				"grep":   {"pattern": "KEPT|MARK", "path": path},
				"ls":     {"path": path},
				"write":  {"file_path": path, "content": "forged"},
				"delete": {"file_path": path, "action": "delete"},
				"copy":   {"file_path": notes, "action": "copy", "to": path},
				"edit":   {"file_path": path, "old_string": "KEPT", "new_string": "OPEN"},
			} {
				name := tool
				if tool == "delete" || tool == "copy" {
					name = "write"
				}
				if res := modelCall(t, r, name, args); !unplaced(res) {
					t.Errorf("sandbox off=%v: %s of %s (%s): %+v", off, tool, what, path, res)
				}
			}
			if res := shellIn(t, r, nil, "cat "+inQuotes(path)); !unplaced(res) {
				t.Errorf("sandbox off=%v: the shell's word %s (%s): %+v", off, what, path, res)
			}
			if _, _, err := r.Admit("org.example.reader fs.read", path); !isUnplaced(err) {
				t.Errorf("sandbox off=%v: a plugin's file %s (%s): %v", off, what, path, err)
			}
		}
		if res := write(t, r, map[string]any{"file_path": fresh, "content": "forged"}); !unplaced(res) || !gone(filepath.Join(credentials, "forged.json")) {
			t.Errorf("sandbox off=%v: a new file in a directory inside a kept one, by the directory's inode: %+v", off, res)
		}
		if res := shellIn(t, r, dirInKept, "cat k.key"); !unplaced(res) {
			t.Errorf("sandbox off=%v: the shell started in a directory inside a kept one, by its inode: %+v", off, res)
		}
		if res := shellIn(t, r, nil, "cd "+inQuotes(dirInKept)+" && cat k.key"); !unplaced(res) {
			t.Errorf("sandbox off=%v: the shell's cd to a directory inside a kept one, by its inode: %+v", off, res)
		}
		if why := r.RootRejectionReason(dirInKept); !strings.Contains(why, "names a file by the number of its volume") {
			t.Errorf("sandbox off=%v: a directory inside a kept one, by its inode, granted as a root: %q", off, why)
		}
		r.SetExtraRoots([]string{dirInKept})
		if _, extra := r.Roots(); len(extra) != 0 {
			t.Errorf("sandbox off=%v: a root named by its inode was granted: %v", off, extra)
		}

		// The control half: by their paths, as they were.
		if res := modelCall(t, r, "read", map[string]any{"file_path": notes}); res.Error != "" || !strings.Contains(res.Output, "home MARK") {
			t.Errorf("sandbox off=%v: a file of the home by its path: %+v", off, res)
		}
		if res := modelCall(t, r, "read", map[string]any{"file_path": ".vol/notes.txt"}); res.Error != "" || !strings.Contains(res.Output, "own MARK") {
			t.Errorf("sandbox off=%v: a folder of the home named .vol: %+v", off, res)
		}
		if res := shellIn(t, r, nil, "cat .vol/notes.txt"); res.Error != "" || !strings.Contains(res.Output, "own MARK") {
			t.Errorf("sandbox off=%v: the shell's word for a folder of the home named .vol: %+v", off, res)
		}
		if res := modelCall(t, r, "read", map[string]any{"file_path": credential}); !res.Refused || !strings.Contains(res.Error, "sub.data") {
			t.Errorf("sandbox off=%v: the credential by its path is refused as the place's: %+v", off, res)
		}
		res := modelCall(t, r, "read", map[string]any{"file_path": open})
		if off && (res.Error != "" || !strings.Contains(res.Output, "outside MARK")) {
			t.Errorf("with the sandbox off a file outside every root by its path is read: %+v", res)
		}
		if !off && (!res.Refused || !strings.Contains(res.Error, ErrOutsideSandbox.Error())) {
			t.Errorf("with the sandbox on a file outside every root by its path is outside: %+v", res)
		}
	}
	for _, f := range []string{credential, open, notes, ownVol} {
		if got := body(t, f); !strings.HasSuffix(got, "MARK\n") && got != "KEPT credential\n" {
			t.Errorf("%s was changed: %q", f, got)
		}
	}
}

// isUnplaced reports the wall's refusal of a path named by its volume and
// inode.
func isUnplaced(err error) bool {
	denied, ok := err.(*SubstrateError)
	return ok && denied.Rule.Kind == firewall.KindUnplaced
}
