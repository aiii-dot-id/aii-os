package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTheShippedRuntimeCarriesNoTestScaffolding(t *testing.T) {
	goTool := goToolPath()
	root := repoRoot(t)
	const module = "github.com/aiii-dot-id/aii-os/"

	deps := func(row platformRow, pkgs ...string) []string {
		cmd := exec.Command(goTool, append([]string{"list", "-e", "-deps"}, pkgs...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOOS="+row.goos, "GOARCH="+row.goarch)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: go list -deps %v: %v", row, pkgs, err)
		}
		return strings.Split(strings.TrimSpace(string(out)), "\n")
	}

	checked := 0
	for _, row := range platformRows {
		// Every executable a package installs on a user's machine, and on
		// the phones the one library the shells bind.
		shipped := []string{"./cmd/aii", "./cmd/aii-app", "./cmd/aii-setup", "./cmd/aii-vulkan-probe"}
		if row.goos == "android" || row.goos == "ios" {
			shipped = []string{"./mobile"}
		}
		for _, dep := range deps(row, shipped...) {
			checked++
			if !strings.HasPrefix(dep, module) {
				continue
			}
			if name := dep[strings.LastIndex(dep, "/")+1:]; strings.HasSuffix(name, "test") {
				t.Errorf("%s: %v depend on %s — test scaffolding must not ship to operators", row, shipped, dep)
			}
		}
	}
	if checked < 100 {
		t.Fatalf("read %d dependencies across the rows — the graph is not being asked", checked)
	}

	// And the developer tool still HAS it, or the move deleted the
	// capability instead of relocating it.
	var found bool
	for _, dep := range deps(platformRows[0], "./cmd/aii-devsign") {
		if strings.HasSuffix(dep, "/packagefmt/packagetest") {
			found = true
		}
	}
	if !found {
		t.Error("cmd/aii-devsign no longer uses packagetest — the packager was supposed to MOVE, not disappear")
	}
}

// The runtime must not offer a verb it no longer implements, and must
// offer the one the maintenance documentation promises.
func TestTheRuntimeOffersVerifyAndNotDevsign(t *testing.T) {
	goTool := goToolPath()
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "aii")
	if runtime.GOOS == "windows" {
		// Windows executes nothing without a recognised extension: every
		// run below would return nothing, as if no verb existed.
		bin += ".exe"
	}
	build := exec.Command(goTool, "build", "-o", bin, "./cmd/aii")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build aii: %v: %s", err, out)
	}

	// verify must exist and explain itself without a config or identity.
	out, _ := exec.Command(bin, "verify").CombinedOutput()
	if !strings.Contains(string(out), "-ledger") {
		t.Errorf("`aii verify` must explain ledger verification without source files or an identity. Got: %s", out)
	}

	// escrow must explain recovery without requiring a running identity.
	out, _ = exec.Command(bin, "escrow").CombinedOutput()
	if !strings.Contains(string(out), "aii escrow restore") || !strings.Contains(string(out), "age -d") {
		t.Errorf("`aii escrow` must exist and say how its file opens without it. Got: %s", out)
	}

	// devsign must not.
	out, _ = exec.Command(bin, "plugin", "devsign").CombinedOutput()
	if strings.Contains(string(out), "-staging") {
		t.Errorf("`aii plugin devsign` still answers in the shipped runtime: %s", out)
	}
}
