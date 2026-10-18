package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTheLandingChainIsReleasedOnlyByItsHoldsAuthor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the landing kit is bash on the build host")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	script := filepath.Join(repoRoot(t), "test", "support", "landing", "release_test.sh")
	if _, err := os.Stat(script); os.IsNotExist(err) {
		t.Skip("this tree carries no landing kit")
	}
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "RELEASE_TEST_PASS") {
		t.Fatalf("release_test.sh: %v\n%s", err, out)
	}
}
