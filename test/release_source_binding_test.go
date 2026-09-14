package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestReleaseRefusesABinaryThatIsNotSourceBound(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "packaging", "assert-source-bound.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the release binding check is missing: %v", err)
	}
	goTool := goToolPath()

	// .
	bin := filepath.Join(t.TempDir(), "probe")
	build := exec.Command(goTool, "build", "-o", bin, "./cmd/aii")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v: %s", err, out)
	}

	rev := gitRev(t, root)
	dirty := gitDirty(t, root)

	run := func(args ...string) (string, error) {
		cmd := exec.Command("sh", append([]string{script}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GO="+goTool)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// .
	// .
	// .
	if !dirty {
		out, err := run(bin, rev)
		if err != nil {
			t.Fatalf("a binary built from HEAD of a clean tree must be accepted, got: %s", out)
		}
		if !strings.Contains(out, "source-bound") {
			t.Fatalf("the accept path must say so: %s", out)
		}
	} else {
		out, err := run(bin, rev)
		if err == nil {
			t.Fatalf("a binary built from a MODIFIED tree must be refused, got: %s", out)
		}
		if !strings.Contains(out, "MODIFIED") {
			t.Fatalf("the refusal must name the modified tree: %s", out)
		}
	}

	// .
	out, err := run(bin, strings.Repeat("0", 40))
	if err == nil {
		t.Fatalf("a binary built from a different commit must be refused, got: %s", out)
	}
	if !strings.Contains(out, "different commit") {
		t.Fatalf("the refusal must name what is wrong: %s", out)
	}
	if !strings.Contains(out, rev) {
		t.Fatalf("the refusal must report the revision actually found, so the operator can see the drift: %s", out)
	}

	notGo := filepath.Join(t.TempDir(), "notgo")
	if err := os.WriteFile(notGo, []byte("this is not an executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err = run(notGo, rev)
	if err == nil {
		t.Fatalf("a file with no build identity must be refused, got: %s", out)
	}
	if !strings.Contains(out, "no Go build information") {
		t.Fatalf("the refusal must name the missing build identity: %s", out)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func gitRev(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("cannot read HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func gitDirty(t *testing.T, dir string) bool {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}

func goToolPath() string {
	if v := os.Getenv("AII_GO"); v != "" {
		return v
	}
	if v := os.Getenv("GO"); v != "" {
		return v
	}
	return "go"
}
