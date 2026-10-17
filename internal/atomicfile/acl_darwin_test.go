//go:build darwin

package atomicfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAFileAnACLKeepsFromBeingWrittenIsStillEmptied(t *testing.T) {
	for _, deny := range []string{"write", "read,write"} {
		t.Run("everyone deny "+deny, func(t *testing.T) { anACLKeepsFromBeingWritten(t, deny) })
	}
}

func anACLKeepsFromBeingWritten(t *testing.T, deny string) {
	dir := t.TempDir()
	for _, how := range []string{"Remove", "WriteReplace", "Replace"} {
		token, other := filepath.Join(dir, "token.json"), filepath.Join(dir, "second")
		os.Remove(other)
		os.Remove(token)
		if err := os.WriteFile(token, []byte("TOKEN"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(token, other); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		if out, err := exec.Command("/bin/chmod", "+a", "everyone deny "+deny, token).CombinedOutput(); err != nil {
			t.Skipf("no ACL is set here: %v %s", err, out)
		}
		if f, err := os.OpenFile(token, os.O_WRONLY, 0); err == nil {
			f.Close()
			t.Skip("the ACL does not deny writing here")
		}
		var err error
		switch how {
		case "Remove":
			err = Remove(token)
		case "WriteReplace":
			_, err = WriteReplace(token, []byte("NEW"), 0o600)
		default:
			staged := filepath.Join(dir, "staged")
			if werr := os.WriteFile(staged, []byte("NEW"), 0o600); werr != nil {
				t.Fatal(werr)
			}
			_, err = Replace(staged, token)
		}
		if err != nil {
			t.Errorf("%s of a file an ACL keeps from being written: %v", how, err)
		}
		if info, err := os.Lstat(other); err != nil || info.Size() != 0 {
			t.Errorf("%s: the other name holds nothing of the file: %v", how, sizeOrError(info, err))
		}
		exec.Command("/bin/chmod", "-N", other).Run()
	}
}

func TestARefusalSaysHowToFindTheOtherName(t *testing.T) {
	dir := t.TempDir()
	token, other := filepath.Join(dir, "token.json"), filepath.Join(dir, "second")
	if err := os.WriteFile(token, []byte("TOKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(token, other); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if err := unix.Chflags(token, unix.UF_IMMUTABLE); err != nil {
		t.Skipf("no immutable flag here: %v", err)
	}
	t.Cleanup(func() { unix.Chflags(token, 0) })
	err := Remove(token)
	if err == nil || !strings.Contains(err.Error(), "has another name") || !strings.Contains(err.Error(), "-samefile "+token) || !strings.Contains(err.Error(), "inode ") {
		t.Errorf("the refusal names how to find the other name: %v", err)
	}
	if _, serr := os.Lstat(token); serr != nil {
		t.Errorf("a file that could not be emptied is not removed: %v", serr)
	}
}

// sizeOrError says what a test's Lstat found: the size, or the error.
func sizeOrError(info os.FileInfo, err error) string {
	if err != nil {
		return err.Error()
	}
	return strconv.FormatInt(info.Size(), 10) + " bytes"
}

func TestASaveTheSwapIsRefusedIsMadeByTheRename(t *testing.T) {
	dir := t.TempDir()
	if !swaps(t, dir) {
		t.Skip("this system or file system makes no swap: the save renames already")
	}
	token, other := filepath.Join(dir, "token.json"), filepath.Join(dir, "second")
	if err := os.WriteFile(token, []byte("TOKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(token, other); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if out, err := exec.Command("/bin/chmod", "+a", "everyone deny write,delete", token).CombinedOutput(); err != nil {
		t.Skipf("no ACL is set here: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("/bin/chmod", "-N", token, other).Run() })
	if published, err := WriteReplace(token, []byte("NEW"), 0o600); err != nil || !published {
		t.Fatalf("a save the swap is refused lands by the rename: published=%v err=%v", published, err)
	}
	if got, _ := os.ReadFile(token); string(got) != "NEW" {
		t.Errorf("the name holds the save: %q", got)
	}
	if info, err := os.Lstat(other); err != nil || info.Size() != 0 {
		t.Errorf("the other name holds nothing of the replaced file: %v", sizeOrError(info, err))
	}
}

// innocentFile is another actor's file at path: mode 0400 and an ACL that
// denies writing, with its own bytes.
func innocentFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("INNOCENT"), 0o400); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/chmod", "+a", "everyone deny write", path).CombinedOutput(); err != nil {
		t.Skipf("no ACL is set here: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("/bin/chmod", "-N", path).Run() })
}

// untouched fails unless the file at path still has mode 0400, the ACL
// that denies writing and its own bytes.
func untouched(t *testing.T, path, what string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Errorf("%s: the other actor's file keeps its mode: %v", what, info.Mode())
	}
	out, _ := exec.Command("/bin/ls", "-led", path).CombinedOutput()
	if !strings.Contains(string(out), "deny write") {
		t.Errorf("%s: the other actor's file keeps its ACL: %s", what, out)
	}
	if got, _ := os.ReadFile(path); string(got) != "INNOCENT" {
		t.Errorf("%s: the other actor's file keeps its bytes: %q", what, got)
	}
}

// judgedToken is a token with another name at dir/token.json, its mode
// 0400 and, when deny is not "", an ACL that denies it; its Lstat as the
// emptying judged it.
func judgedToken(t *testing.T, dir, deny string) (string, os.FileInfo) {
	t.Helper()
	token := filepath.Join(dir, "token.json")
	if err := os.WriteFile(token, []byte("TOKEN"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(token, filepath.Join(dir, "second")); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if deny != "" {
		if out, err := exec.Command("/bin/chmod", "+a", "everyone deny "+deny, token).CombinedOutput(); err != nil {
			t.Skipf("no ACL is set here: %v %s", err, out)
		}
		t.Cleanup(func() { exec.Command("/bin/chmod", "-N", filepath.Join(dir, "second")).Run() })
	}
	info, err := os.Lstat(token)
	if err != nil {
		t.Fatal(err)
	}
	return token, info
}

// swapIn stands another actor in at stage: at that moment it renames its
// own file (or a link to it, when link is set) onto the path.
func swapIn(t *testing.T, stage, innocent string, link bool) {
	t.Helper()
	attrWindow = func(at, path string) {
		if at != stage {
			return
		}
		if link {
			tmp := path + ".link"
			if err := os.Symlink(innocent, tmp); err != nil {
				t.Fatal(err)
			}
			innocent = tmp
		}
		if err := os.Rename(innocent, path); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { attrWindow = nil })
}

func TestASwappedInFileIsNotTouchedWhereAHandleOpens(t *testing.T) {
	dir := t.TempDir()
	token, _ := judgedToken(t, dir, "")
	innocent := filepath.Join(dir, "innocent")
	innocentFile(t, innocent)
	swapIn(t, "call", innocent, false)
	if err := Remove(token); err == nil {
		t.Error("a removal whose file was swapped for another is refused")
	}
	untouched(t, token, "swapped in before the call, a handle open")
}

func TestWhereNoHandleOpensASwapBeforeTheCheckIsRefusedUntouched(t *testing.T) {
	dir := t.TempDir()
	token, info := judgedToken(t, dir, "read,write")
	innocent := filepath.Join(dir, "innocent")
	innocentFile(t, innocent)
	swapIn(t, "judged", innocent, false)
	if err := ownerWritable(token, info); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Errorf("a file swapped in before the check is refused: %v", err)
	}
	untouched(t, token, "swapped in before the check")
}

func TestWhereNoHandleOpensASwapAtTheCallIsRefusedAfterIt(t *testing.T) {
	dir := t.TempDir()
	token, info := judgedToken(t, dir, "read,write")
	innocent := filepath.Join(dir, "innocent")
	innocentFile(t, innocent)
	swapIn(t, "call", innocent, false)
	if err := ownerWritable(token, info); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Errorf("a file swapped in at the call is refused after it: %v", err)
	}
}

func TestTheCallByPathDoesNotFollowALink(t *testing.T) {
	dir := t.TempDir()
	token, info := judgedToken(t, dir, "read,write")
	innocent := filepath.Join(dir, "innocent")
	innocentFile(t, innocent)
	swapIn(t, "call", innocent, true)
	if err := ownerWritable(token, info); err == nil {
		t.Error("a link at the path at the call is refused")
	}
	untouched(t, innocent, "a link to it put at the call")
}
