//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// A HARDWARE QUERY NEVER PUTS A WINDOW ON THE DESKTOP. The identity runs
// detached, and a console program it starts without saying otherwise gets a
// visible console of its own — the Vulkan probe and nvidia-smi flashed one at
// every selection. The start's attributes, with no desktop involved.
func TestSelectionQueriesNeverShowAConsole(t *testing.T) {
	var out limitedSelectionOutput
	cmd := selectionCommand(t.Context(), &out, filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/c", "exit", "0")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("a hardware query would open a console window: %+v", cmd.SysProcAttr)
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("a hidden query must still run: %v", err)
	}
}
