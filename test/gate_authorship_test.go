package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// THE GATE READS WHO MADE EACH COMMIT IT WOULD LET MAIN TAKE. The real gate's authorship stage over a fixture
// repository: a commit since main whose author or committer is the
// operator's address is refused, named by commit and field and never by the
// address; a range with none passes and says how many it read; with the
// address unset, or no main to measure from, the stage says it checked
// nothing.
func TestTheGateRefusesACommitCarryingTheOperatorsAddress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the gate is a POSIX shell script")
	}
	for _, command := range []string{"sh", "git"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("no %s", command)
		}
	}
	const operator = "operator@example.invalid"
	fixture := func(t *testing.T, branch string) string {
		t.Helper()
		root := t.TempDir()
		for _, name := range []string{"gate.sh", "gate-lock.sh", "toolchain.sh", "evidence-seal.sh"} {
			raw, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			write(t, root, "test/"+name, string(raw))
			if err := os.Chmod(filepath.Join(root, "test", name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		git(t, root, "init", "-q", "-b", branch)
		git(t, root, "add", "-A")
		git(t, root, "commit", "-qm", "base")
		git(t, root, "checkout", "-q", "-b", "work")
		return root
	}
	commitAs := func(t *testing.T, root, file, author, committer string) {
		t.Helper()
		write(t, root, file, file)
		git(t, root, "add", file)
		cmd := exec.Command("git", "commit", "-qm", file)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL="+author, "GIT_COMMITTER_NAME=c", "GIT_COMMITTER_EMAIL="+committer)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit: %v\n%s", err, out)
		}
	}
	// What the gate printed, and then what its stage wrote to its log.
	gate := func(t *testing.T, root string, env ...string) (string, error) {
		t.Helper()
		evidence := t.TempDir()
		cmd := exec.Command("sh", "test/gate.sh")
		cmd.Dir = root
		cmd.Env = append(append(os.Environ(), "GATE_ONLY=authorship", "EVIDENCE_DIR="+evidence, "AII_OPERATOR_ADDRESS="), env...)
		out, err := cmd.CombinedOutput()
		stageLog, _ := os.ReadFile(filepath.Join(evidence, "authorship.log"))
		return string(out) + string(stageLog), err
	}

	t.Run("a commit the operator's address authored or committed is refused", func(t *testing.T) {
		root := fixture(t, "main")
		commitAs(t, root, "clean", "agent@example.invalid", "agent@example.invalid")
		commitAs(t, root, "authored", operator, "agent@example.invalid")
		authored := strings.TrimSpace(git(t, root, "rev-parse", "--short", "HEAD"))
		commitAs(t, root, "committed", "agent@example.invalid", operator)
		committed := strings.TrimSpace(git(t, root, "rev-parse", "--short", "HEAD"))
		out, err := gate(t, root, "AII_OPERATOR_ADDRESS="+operator)
		if err == nil || !strings.Contains(out, "GATE_RESULT_FAIL") {
			t.Fatalf("the gate passed a range that carries the operator's address:\n%s", out)
		}
		for _, want := range []string{authored + " author", committed + " committer"} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not name %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, operator) {
			t.Errorf("the refusal printed the address it refuses:\n%s", out)
		}
	})
	t.Run("a range with none of it passes and says what it read", func(t *testing.T) {
		root := fixture(t, "main")
		commitAs(t, root, "one", "agent@example.invalid", "agent@example.invalid")
		commitAs(t, root, "two", "other@example.invalid", "other@example.invalid")
		out, err := gate(t, root, "AII_OPERATOR_ADDRESS="+operator)
		if err != nil || !strings.Contains(out, "authorship: 2 commits since main") {
			t.Fatalf("a clean range: %v\n%s", err, out)
		}
	})
	t.Run("a main that shares no history with the tree is refused, not read as clean", func(t *testing.T) {
		root := fixture(t, "main")
		git(t, root, "checkout", "-q", "--orphan", "unrelated")
		commitAs(t, root, "orphan", "agent@example.invalid", "agent@example.invalid")
		out, err := gate(t, root, "AII_OPERATOR_ADDRESS="+operator)
		if err == nil || !strings.Contains(out, "HEAD shares no history with main") {
			t.Fatalf("a tree unrelated to main was let through: %v\n%s", err, out)
		}
	})
	// THE ADDRESS IS ONE MAILBOX IN ANY CASE: a
	// commit whose author writes it in capitals is the operator's address.
	t.Run("the address is matched without its case", func(t *testing.T) {
		root := fixture(t, "main")
		commitAs(t, root, "shouted", strings.ToUpper(operator), "agent@example.invalid")
		shouted := strings.TrimSpace(git(t, root, "rev-parse", "--short", "HEAD"))
		out, err := gate(t, root, "AII_OPERATOR_ADDRESS="+operator)
		if err == nil || !strings.Contains(out, shouted+" author") {
			t.Fatalf("the address in capitals was let through: %v\n%s", err, out)
		}
	})
	// A WALK THAT FAILS IS NOT A CLEAN RANGE: a git
	// whose log fails, and only its log, stands first on the path.
	t.Run("a log that cannot be read is refused, not read as clean", func(t *testing.T) {
		real, err := exec.LookPath("git")
		if err != nil {
			t.Fatal(err)
		}
		shim := t.TempDir()
		write(t, shim, "git", "#!/bin/sh\nif [ \"$1\" = log ]; then echo 'fatal: injected' >&2; exit 128; fi\nexec '"+real+"' \"$@\"\n")
		if err := os.Chmod(filepath.Join(shim, "git"), 0o700); err != nil {
			t.Fatal(err)
		}
		root := fixture(t, "main")
		commitAs(t, root, "authored", operator, operator)
		out, err := gate(t, root, "AII_OPERATOR_ADDRESS="+operator, "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
		if err == nil || !strings.Contains(out, "could not be read") {
			t.Fatalf("a failed log was read as a clean range: %v\n%s", err, out)
		}
	})
	// A COMPARISON THAT DOES NOT RUN IS NOT A CLEAN RANGE: an awk that fails on the stage's comparison, and on
	// nothing else, stands first on the path.
	t.Run("a comparison that does not run is refused, not read as clean", func(t *testing.T) {
		root := fixture(t, "main")
		commitAs(t, root, "authored", operator, operator)
		out, err := gate(t, root, "AII_OPERATOR_ADDRESS="+operator, "PATH="+failingTool(t, "awk", "tolower", 2)+string(os.PathListSeparator)+os.Getenv("PATH"))
		if err == nil || !strings.Contains(out, "could not be compared") {
			t.Fatalf("a failed comparison was read as a clean range: %v\n%s", err, out)
		}
	})
	// A COMPARISON THAT READ FEWER LINES THAN THERE ARE COMMITS IS NOT A
	// CLEAN RANGE: an awk that exits 0
	// having printed nothing.
	t.Run("a comparison that compared nothing is refused, not read as clean", func(t *testing.T) {
		root := fixture(t, "main")
		commitAs(t, root, "authored", operator, operator)
		out, err := gate(t, root, "AII_OPERATOR_ADDRESS="+operator, "PATH="+failingTool(t, "awk", "tolower", 0)+string(os.PathListSeparator)+os.Getenv("PATH"))
		if err == nil || !strings.Contains(out, "but the comparison of their authors read none") {
			t.Fatalf("a comparison that compared nothing was read as a clean range: %v\n%s", err, out)
		}
	})
	// AN ADDRESS WITH A SPACE IN IT DOES NOT MOVE THE OTHER FIELD: git keeps an author address with a
	// space; the operator's address as that commit's committer is refused.
	t.Run("an address with a space does not hide the other field", func(t *testing.T) {
		root := fixture(t, "main")
		commitAs(t, root, "spaced", "x y@example.invalid", operator)
		if kept := strings.TrimSpace(git(t, root, "log", "-1", "--format=%ae")); kept != "x y@example.invalid" {
			t.Fatalf("rig: this git did not keep the space in the author's address (%q)", kept)
		}
		spaced := strings.TrimSpace(git(t, root, "rev-parse", "--short", "HEAD"))
		out, err := gate(t, root, "AII_OPERATOR_ADDRESS="+operator)
		if err == nil || !strings.Contains(out, spaced+" committer") {
			t.Fatalf("a space in the author's address hid the operator's address as committer: %v\n%s", err, out)
		}
	})
	t.Run("unset, it says it checked nothing", func(t *testing.T) {
		root := fixture(t, "main")
		commitAs(t, root, "authored", operator, operator)
		if out, err := gate(t, root); err != nil || !strings.Contains(out, "AII_OPERATOR_ADDRESS is unset, so nothing was checked") {
			t.Fatalf("unset: %v\n%s", err, out)
		}
	})
	t.Run("with no main, it says it checked nothing", func(t *testing.T) {
		root := fixture(t, "trunk")
		commitAs(t, root, "authored", operator, operator)
		if out, err := gate(t, root, "AII_OPERATOR_ADDRESS="+operator); err != nil || !strings.Contains(out, "has no main to measure from") {
			t.Fatalf("no main: %v\n%s", err, out)
		}
	})
}

// failingTool is a folder holding a stand-in for tool that exits code,
// printing nothing, when its arguments contain when (every call, when is
// empty), and runs the real tool otherwise: put first on a path, it fails
// that one use of the tool and no other.
func failingTool(t *testing.T, tool, when string, code int) string {
	t.Helper()
	real, err := exec.LookPath(tool)
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	write(t, shim, tool, fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *%s*) exit %d ;; esac\nexec '%s' \"$@\"\n", when, code, real))
	if err := os.Chmod(filepath.Join(shim, tool), 0o700); err != nil {
		t.Fatal(err)
	}
	return shim
}
