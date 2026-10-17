//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// AN INSPECTION THAT FAILS IS NOT "STOPPED". The launcher says it cannot
// tell, and neither starts the slot nor opens a port nobody identified.
func TestAnInspectionThatFailsIsNotReadAsStopped(t *testing.T) {
	saved := slotRunning
	slotRunning = func(string) (bool, error) {
		return false, errors.New("read the process table: the process table is not readable here")
	}
	t.Cleanup(func() { slotRunning = saved })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	slot := install.SlotName(90)
	if err := os.MkdirAll(filepath.Join(home, install.Dir, slot), 0o700); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(home, "aii-calls")
	aii := filepath.Join(home, "aii")
	if err := os.WriteFile(aii, []byte("#!/bin/sh\necho \"$@\" >> '"+calls+"'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := launch(aii)
	if err == nil || !strings.Contains(err.Error(), "cannot determine whether this identity is running") {
		t.Fatalf("an unreadable process table: %v", err)
	}
	if raw, _ := os.ReadFile(calls); len(raw) != 0 {
		t.Fatalf("the launcher acted on a guess: aii was asked %q", raw)
	}
}
