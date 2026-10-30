//go:build windows

package updates

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func TestAnInstallReachedThroughAJunctionIsUpdatedWhereItLies(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	in := rig.install(t, archive)
	link := filepath.Join(t.TempDir(), "link")
	if out, err := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "mklink", "/J", link, in.dir).CombinedOutput(); err != nil {
		t.Fatalf("a junction %s -> %s: %v: %s", link, in.dir, err, out)
	}
	through := filepath.Join(link, filepath.Base(in.exe))
	if _, err := filepath.EvalSymlinks(through); err == nil {
		t.Log("EvalSymlinks follows this junction: the handle is not needed for it, and the update below holds all the same")
	}

	// One program, one target: the spelling through the junction and the
	// spelling where it lies take the same lock and read the same record.
	tgt, err := targetOf(through)
	if err != nil {
		t.Fatalf("the program reached through a junction was refused: %v", err)
	}
	direct, err := targetOf(in.exe)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(tgt.path, direct.path) || !strings.EqualFold(tgt.lock, direct.lock) {
		t.Errorf("one program, two targets: through the junction %q (lock %q), where it lies %q (lock %q)", tgt.path, tgt.lock, direct.path, direct.lock)
	}
	if a, b := statFor(t, tgt.path), statFor(t, in.exe); !os.SameFile(a, b) {
		t.Errorf("the target %s is not the program at %s", tgt.path, in.exe)
	}

	// And the update itself, run through the junction, replaces the program.
	if err := in.c.applyTo(context.Background(), through); err != nil {
		t.Fatalf("the update through a junction was refused: %v", err)
	}
	if got, err := os.ReadFile(in.exe); err != nil || string(got) == "old binary" {
		t.Errorf("the program behind the junction was not replaced: %q, %v", got, err)
	}
	if !exists(direct.record) || !exists(direct.backup) {
		t.Error("the update's record and backup are not beside the program where it lies")
	}
}

func statFor(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}
