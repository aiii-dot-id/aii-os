package test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheNetworkModuleIsTheTreesOwnCopy(t *testing.T) {
	root := repoRoot(t)
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(goToolPath(), args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("go %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out.String())
		}
		return out.String()
	}
	const module = "github.com/wlynxg/anet"
	own := filepath.Join(root, "third_party", "anet")
	built := strings.TrimSpace(run(root, "list", "-m", "-f", "{{.Dir}}", module))
	if built == "" {
		t.Fatalf("the go tool names no directory for %s: the walk is not reading the module graph", module)
	}
	same := func(a, b string) bool {
		ai, aerr := os.Stat(a)
		bi, berr := os.Stat(b)
		return aerr == nil && berr == nil && os.SameFile(ai, bi)
	}
	if !same(built, own) {
		t.Fatalf("%s is built from %s, not from the tree's own copy %s: the Android program does not link with the published module", module, built, own)
	}
	if out := run(own, "test", "-count=1", "./..."); !strings.Contains(out, "ok") && !strings.Contains(out, "no test files") {
		t.Fatalf("the copy's own tests said neither ok nor that this platform has none:\n%s", out)
	}
}
