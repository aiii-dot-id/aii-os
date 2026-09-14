//go:build linux || darwin

package broker

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
// .
// .
// .
// .
func TestPublishFailedWriteKeepsThePriorFile(t *testing.T) {
	data := t.TempDir()
	h := newHost(t, newStore(t), Config{MaxFilesBytes: 1 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.private"})
	private := filepath.Join(data, "p")
	b.SetFiles(private, 0)
	prior := strings.Repeat("a", 32)
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "private", "enrollment.json", `{"data":"`+prior+`"}`)), statusSucceeded, "")

	var orig syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &orig); err != nil {
		t.Skip("no RLIMIT_FSIZE here")
	}
	limited := orig
	limited.Cur = 8
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
		t.Skipf("cannot lower RLIMIT_FSIZE: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &orig) })

	m := dispatch(t, b, fsParams("fs.publish", "private", "enrollment.json", `{"data":"`+strings.Repeat("b", 64)+`","expected_sha256":"`+hexSum([]byte(prior))+`"}`))
	_ = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &orig)
	wantResult(t, m, statusFailed, reasonFSIOFailed)
	got, err := os.ReadFile(filepath.Join(private, "enrollment.json"))
	if err != nil || string(got) != prior {
		t.Fatalf("a failed publication destroyed the prior file: %q %v", got, err)
	}
	entries, _ := os.ReadDir(private)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".publish-") {
			t.Fatalf("a temporary file survived the failure: %s", e.Name())
		}
	}
	// .
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "enrollment.json", `{"data":"`+strings.Repeat("c", 32)+`"}`)), statusSucceeded, "")
	if got, _ := os.ReadFile(filepath.Join(private, "enrollment.json")); string(got) != strings.Repeat("c", 32) {
		t.Fatalf("fs.write with room writes whole: %q", got)
	}
}
