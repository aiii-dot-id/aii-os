//go:build !windows

package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReferenceAllowsPermissionProbesWithoutExposingItsLog(t *testing.T) {
	base, err := os.MkdirTemp("", "aii-reference-permissions-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	if err := os.Chmod(base, 0o711); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	driver := filepath.Join(state, "go")
	const script = `#!/bin/bash
if [ "$*" = version ]; then echo 'go version permission-fixture'; exit 0; fi
[ "$*" = 'test -v -race -count=1 -timeout 45m ./...' ] || exit 81
set -eu
printf '%s' "$TMPDIR" > "$FIXTURE_STATE/tmpdir"
touch "$TMPDIR/probe"
echo 'private test output'
touch "$FIXTURE_STATE/ready"
for ((i=0; i<1000; i++)); do
  test ! -f "$FIXTURE_STATE/done" || exit 0
  sleep 0.01
done
exit 82
`
	if err := os.WriteFile(driver, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "run_race_scope.sh", "reference")
	cmd.Env = append(os.Environ(), "AII_GO="+driver, "TMPDIR="+base, "FIXTURE_STATE="+state)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := filepath.Join(state, "done")
	waited := false
	t.Cleanup(func() {
		os.WriteFile(done, nil, 0o600)
		if !waited {
			_ = cmd.Wait()
		}
	})
	for {
		if _, err := os.Stat(filepath.Join(state, "ready")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("reference driver did not become ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	raw, err := os.ReadFile(filepath.Join(state, "tmpdir"))
	if err != nil {
		t.Fatal(err)
	}
	tmp := strings.TrimSpace(string(raw))
	run := filepath.Dir(tmp)
	for _, path := range []string{run, tmp} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o711 {
			t.Errorf("permission-probe ancestor %s is not traversal-only: %v %v", path, info, err)
		}
	}
	log := filepath.Join(run, "reference.log")
	info, err := os.Stat(log)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("reference output is not private: %v %v", info, err)
	}
	if os.Geteuid() == 0 {
		probe := exec.Command("/bin/sh", "-c", `test -e "$1/probe" && test ! -r "$2" && test ! -r "$1"`, "probe", tmp, log)
		probe.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
		if out, err := probe.CombinedOutput(); err != nil {
			t.Errorf("unprivileged probe could not traverse its path, or could read private output: %v %s", err, out)
		}
	}
	if err := os.WriteFile(done, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(run); !os.IsNotExist(err) {
		t.Errorf("reference runner did not remove its owned directory: %v", err)
	}
}
