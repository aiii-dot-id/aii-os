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
	root, run := builtFromTheTreesCopy(t, "github.com/wlynxg/anet", "anet", "the Android program does not link with the published module")
	if out := run(filepath.Join(root, "third_party", "anet"), "test", "-count=1", "./..."); !strings.Contains(out, "ok") && !strings.Contains(out, "no test files") {
		t.Fatalf("the copy's own tests said neither ok nor that this platform has none:\n%s", out)
	}
}

func TestTheICEModuleIsTheTreesOwnCopy(t *testing.T) {
	const module, copied, added = "github.com/pion/ice/v4", "v4.4.7", "TestControlledSelector_FollowsNominationOffQuietSelectedPair"
	root, run := builtFromTheTreesCopy(t, module, "pion-ice", "a controlled channel keeps sending on a path that has gone quiet")
	if required := strings.TrimSpace(run(root, "list", "-m", "-f", "{{.Version}}", module)); required != copied {
		t.Fatalf("go.mod requires %s %s and third_party/pion-ice is a copy of %s: carry its change onto the new version, or drop the copy if that release has it", module, required, copied)
	}
	if out := run(root, "test", "-count=1", "-v", "-run", "^"+added+"$", module); !strings.Contains(out, "--- PASS: "+added+" ") {
		t.Fatalf("the copy's own test did not pass:\n%s", out)
	}
}

// builtFromTheTreesCopy fails unless the go tool builds module from
// third_party/<dir>, saying why the copy is there, and returns the tree's root and a way to run the go
// tool. A directory with a go.mod of its own is no part of ./..., so no
// other run reads it: this asks the go tool which directory is built for
// the module's path.
func builtFromTheTreesCopy(t *testing.T, module, dir, why string) (string, func(dir string, args ...string) string) {
	t.Helper()
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
	own := filepath.Join(root, "third_party", dir)
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
		t.Fatalf("%s is built from %s, not from the tree's own copy %s: %s", module, built, own, why)
	}
	return root, run
}
