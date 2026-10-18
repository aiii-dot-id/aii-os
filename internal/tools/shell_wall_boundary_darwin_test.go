//go:build darwin && !ios

package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestOnAMacTheBoundaryRefusesWritesOutsideTheHome(t *testing.T) {
	f := newWallFixture(t)
	outside := outsideTheBoundary(t, f.home)
	// Its absolute path split across two variables, so the word check (which
	// would refuse an outside path it could see) passes it to the kernel.
	half := len(outside) / 2
	a, bpart := outside[:half], outside[half:]
	write := `a='` + a + `'; b='` + bpart + `'; echo x > "$a$b/f.txt"`

	// Boundary on: the kernel refuses the write.
	res := modelCall(t, f.reg, "shell", map[string]any{"command": write})
	if res.Refused {
		t.Fatalf("the word check refused the outside write (%q); the kernel boundary is what is tested", res.Error)
	}
	if res.ExitStatus != nil && *res.ExitStatus == 0 {
		t.Fatalf("the boundary should refuse a write outside the home:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(outside, "f.txt")); err == nil {
		t.Fatal("a file was written outside the home with the boundary on")
	}

	// Writes inside the home and the temp directories land.
	allowedByWall(t, f, "home write", `echo hi > in-home.txt && cat in-home.txt`, "hi")
	allowedByWall(t, f, "temp write", `d=$(mktemp -d) && echo hi > "$d/t.txt" && cat "$d/t.txt" && rm -r "$d"`, "hi")

	// Boundary off: the same outside write lands, and a kept place stays
	// refused.
	f.reg.SetSandboxOff(true)
	res = modelCall(t, f.reg, "shell", map[string]any{"command": write})
	if res.ExitStatus == nil || *res.ExitStatus != 0 {
		t.Fatalf("with the boundary off the outside write should land (exit %v):\n%s", res.ExitStatus, res.Output)
	}
	if b, err := os.ReadFile(filepath.Join(outside, "f.txt")); err != nil || !strings.Contains(string(b), "x") {
		t.Fatalf("with the boundary off the outside write did not land: %q %v", b, err)
	}
	refusedByKernel(t, f, "kept place, boundary off", `p=conf; q=ig.json; echo x > "$p$q"`)
}

func TestOnAMacTheBoundaryDoesNotWidenFromTheHostsTMPDIR(t *testing.T) {
	leaves := userTempDirs()
	if len(leaves) == 0 {
		t.Fatal("inconclusive: the account owns no /private/var/folders/*/*/T here")
	}
	leaf := leaves[0]
	container := filepath.Dir(leaf)
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	rowTwo := func(profile string) []string {
		var out []string
		for _, l := range strings.Split(profile, "\n") {
			if l == "(deny file-write*)" || strings.HasPrefix(l, "(allow file-write*") {
				out = append(out, l)
			}
		}
		return out
	}
	baseline, err := shellProfile(home, shellWall{boundary: true}, userTempDirs())
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(rowTwo(baseline), "\n")
	if !strings.Contains(want, `(subpath "`+leaf+`")`) {
		t.Fatalf("inconclusive: the baseline does not allow the account's own temp leaf %s:\n%s", leaf, want)
	}
	for name, tmpdir := range map[string]string{
		"the leaf":                leaf,
		"the per-user container":  container,
		"/private/var/folders":    filepath.Dir(filepath.Dir(container)),
		"/var/folders (the link)": "/var/folders",
	} {
		t.Setenv("TMPDIR", tmpdir)
		p, err := shellProfile(home, shellWall{boundary: true}, userTempDirs())
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(rowTwo(p), "\n"); got != want {
			t.Errorf("TMPDIR=%s (%s) changed the row-2 lines:\n--- got ---\n%s\n--- want ---\n%s", tmpdir, name, got, want)
		}
		for _, wide := range []string{container, filepath.Dir(filepath.Dir(container))} {
			if strings.Contains(p, `(subpath "`+wide+`")`) {
				t.Errorf("TMPDIR=%s (%s) made %s writable", tmpdir, name, wide)
			}
		}
	}
}

func TestOnAMacTheTempLeafIsADirectoryNotALink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "folders")
	real := filepath.Join(root, "aa", "bb", "T")
	elsewhere := filepath.Join(t.TempDir(), "account-home")
	for _, d := range []string{real, elsewhere, filepath.Join(root, "cc", "dd"), filepath.Join(root, "ee", "ff")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, "cc", "dd", "T")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ee", "ff", "T"), []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := userTempDirsIn(root)
	want := []string{firewall.ResolvePath(real)}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the temp leaves taken are %q, want only the real directory %q", got, want)
	}
}

func TestOnAMacTheTempLeafIsTheAccountsOwn(t *testing.T) {
	uid := os.Getuid()
	matches, err := filepath.Glob(filepath.Join(userFolders, "*", "*", "T"))
	if err != nil {
		t.Fatal(err)
	}
	others := 0
	for _, m := range matches {
		if info, err := os.Lstat(m); err == nil && info.IsDir() {
			if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
				others++
			}
		}
	}
	if others == 0 {
		t.Skip("no other account's temp leaf on this host: the ownership check has nothing to refuse")
	}
	taken := userTempDirs()
	if len(taken) == 0 {
		t.Fatal("inconclusive: the account's own temp leaf was not found")
	}
	for _, d := range taken {
		info, err := os.Lstat(d)
		if err != nil {
			t.Fatal(err)
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
			t.Errorf("the boundary took %s, which is not this account's (uid %d); %d other accounts' leaves are here", d, uid, others)
		}
	}
}

func TestOnAMacEverydayToolsWriteTheirTemporariesBehindTheBoundary(t *testing.T) {
	f := newWallFixture(t)
	allowedByWall(t, f, "mktemp", `f=$(mktemp) && echo "$f" && rm "$f"`, "")
	if _, err := exec.LookPath("git"); err == nil {
		allowedByWall(t, f, "git init", `git init -q r && git -C r status --porcelain; echo done`, "done")
	}
	if _, err := exec.LookPath("python3"); err == nil {
		allowedByWall(t, f, "python3 -c", `python3 -c 'print(2+2)'`, "4")
	}
	if _, err := exec.LookPath("clang"); err == nil {
		allowedByWall(t, f, "clang", `printf 'int main(){return 0;}\n' > h.c && clang -o hc h.c && echo built`, "built")
	}
	if _, err := exec.LookPath("make"); err == nil {
		allowedByWall(t, f, "make", `make --version | head -1`, "")
	}
}

func outsideTheBoundary(t *testing.T, home string) string {
	t.Helper()
	allowed := append(darwinShellTemps(home, userTempDirs()), firewall.ResolvePath(home))
	within := func(p string) bool {
		rp := firewall.ResolvePath(p)
		for _, a := range allowed {
			if rp == a || strings.HasPrefix(rp, a+"/") {
				return true
			}
		}
		return false
	}
	beside := filepath.Join(filepath.Dir(home), "outside")
	if !within(beside) {
		if err := os.MkdirAll(beside, 0o755); err != nil {
			t.Fatal(err)
		}
		return beside
	}
	leaves := userTempDirs()
	if len(leaves) == 0 {
		t.Fatal("inconclusive: the account owns no /private/var/folders/*/*/T here")
	}
	container := filepath.Dir(leaves[0])
	if within(container) {
		t.Fatalf("inconclusive: the per-user container %s is a place the boundary allows", container)
	}
	dir, err := os.MkdirTemp(container, "aii-wall-outside-")
	if err != nil {
		t.Fatalf("inconclusive: the per-user container %s is not writable: %v", container, err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
