//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func TestTheLauncherAsksTheServiceManagerAndNeverStartsTheIdentity(t *testing.T) {
	const n = 90
	if serving(install.Port(n)) {
		t.Skipf("port %d is in use on this host", install.Port(n))
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	slot := install.SlotName(n)
	if err := os.MkdirAll(filepath.Join(home, install.Dir, slot), 0o700); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(home, "aii-calls")
	aii := filepath.Join(home, "aii")
	script := "#!/bin/sh\necho \"$@\" >> '" + calls + "'\n" +
		"if [ \"$1\" = register ]; then echo 'register: the service manager refused' >&2; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(aii, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := launch(aii)
	if err == nil || !strings.Contains(err.Error(), "the service manager refused") {
		t.Fatalf("the manager's refusal is the answer, got: %v", err)
	}
	raw, _ := os.ReadFile(calls)
	if got := strings.TrimSpace(string(raw)); got != "register "+slot {
		t.Fatalf("the launcher asked aii for %q; want only %q", got, "register "+slot)
	}
}
