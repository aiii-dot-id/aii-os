package test

import (
	"os/exec"
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
// .
// .
func TestTheShippedRuntimeCarriesNoTestScaffolding(t *testing.T) {
	goTool := goToolPath()
	root := repoRoot(t)

	deps := func(pkg string) []string {
		cmd := exec.Command(goTool, "list", "-deps", pkg)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		return strings.Split(strings.TrimSpace(string(out)), "\n")
	}

	// .
	for _, shipped := range []string{"./cmd/aii", "./cmd/aii-app", "./cmd/aii-setup"} {
		for _, dep := range deps(shipped) {
			if strings.HasSuffix(dep, "/packagefmt/packagetest") {
				t.Errorf("%s depends on %s — test scaffolding that mints throwaway trust roots must not ship to operators", shipped, dep)
			}
			if strings.HasSuffix(dep, "/genesis/genesistest") {
				t.Errorf("%s depends on %s — test scaffolding must not ship to operators", shipped, dep)
			}
		}
	}

	// .
	// .
	var found bool
	for _, dep := range deps("./cmd/aii-devsign") {
		if strings.HasSuffix(dep, "/packagefmt/packagetest") {
			found = true
		}
	}
	if !found {
		t.Error("cmd/aii-devsign no longer uses packagetest — the packager was supposed to MOVE, not disappear")
	}
}

// .
// .
func TestTheRuntimeOffersVerifyAndNotDevsign(t *testing.T) {
	goTool := goToolPath()
	root := repoRoot(t)
	bin := t.TempDir() + "/aii"
	build := exec.Command(goTool, "build", "-o", bin, "./cmd/aii")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build aii: %v: %s", err, out)
	}

	// .
	out, _ := exec.Command(bin, "verify").CombinedOutput()
	if !strings.Contains(string(out), "-ledger") {
		t.Errorf("`aii verify` must exist — the maintenance guide tells a source-less operator to run it, and restoring depends on it. Got: %s", out)
	}

	// .
	// .
	out, _ = exec.Command(bin, "escrow").CombinedOutput()
	if !strings.Contains(string(out), "aii escrow restore") || !strings.Contains(string(out), "age -d") {
		t.Errorf("`aii escrow` must exist and say how its file opens without it. Got: %s", out)
	}

	// .
	out, _ = exec.Command(bin, "plugin", "devsign").CombinedOutput()
	if strings.Contains(string(out), "-staging") {
		t.Errorf("`aii plugin devsign` still answers in the shipped runtime: %s", out)
	}
}
