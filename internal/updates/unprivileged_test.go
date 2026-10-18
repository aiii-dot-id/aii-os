//go:build !windows

package updates

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"testing"
)

func ranUnprivileged(t *testing.T) bool {
	t.Helper()
	if os.Geteuid() != 0 {
		return false
	}
	if os.Getenv(unprivilegedChild) != "" {
		t.Fatal("the run that was to be unprivileged is still root")
	}
	who, err := user.Lookup("nobody")
	if err != nil {
		t.Fatalf("this test must run as an unprivileged user and root cannot find one: %v", err)
	}
	uid, uerr := strconv.ParseUint(who.Uid, 10, 32)
	gid, gerr := strconv.ParseUint(who.Gid, 10, 32)
	if uerr != nil || gerr != nil || uid == 0 {
		t.Fatalf("the unprivileged user's ids are %q and %q", who.Uid, who.Gid)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The test binary's own directory and t.TempDir() belong to root alone;
	// the child needs the binary where it may run it and a directory it owns.
	dir, err := os.MkdirTemp("", "aii-unprivileged-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	bin, work := filepath.Join(dir, "test"), filepath.Join(dir, "work")
	if err := os.Link(self, bin); err != nil {
		if err := copyExecutable(self, bin); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []error{os.Chmod(dir, 0o755), os.Chmod(bin, 0o755), os.Mkdir(work, 0o700), os.Chown(work, int(uid), int(gid))} {
		if step != nil {
			t.Fatal(step)
		}
	}
	cmd := exec.Command(bin, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.count=1", "-test.v")
	cmd.Dir = work
	cmd.Env = []string{"TMPDIR=" + work, "HOME=" + work, "PATH=" + os.Getenv("PATH"), unprivilegedChild + "=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("--- PASS: "+t.Name()+" (")) {
		t.Fatalf("run again as %s (uid %d), because root cannot observe it, the test did not run and pass there (%v; the directories above %s must let that user through):\n%s", who.Username, uid, err, dir, out)
	}
	t.Logf("root cannot observe this, so it ran again as %s (uid %d) and passed there", who.Username, uid)
	return true
}

// unprivilegedChild marks the second run, so that one which is somehow
// still root says so instead of starting a third.
const unprivilegedChild = "AII_TEST_UNPRIVILEGED_CHILD"

func copyExecutable(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
