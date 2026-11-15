package app

import (
	"slices"
	"testing"
	"time"
)

// A TOOL SWITCH DOES NOT WAIT ON THE LOCK IT HOLDS. The shell's description
// reads the configuration (boot.go wires the registry's ShellWindowsUTF8 to
// configSnapshot), and both places that change a tool's switch run under
// cfgMu: the dashboard's switch (setToolEnabled) and a reload whose
// tools.disabled changed (applyDisabledTools). When either listed the tools
// with their descriptions, it took cfgMu again on its own goroutine and
// never returned: the switch did nothing and the host could not shut down.
// It was found by A10's journey on a running host (window r8, 2026-11-11);
// no test before it pressed a switch on a host wired as boot wires one.
func TestAToolSwitchDoesNotWaitOnTheConfigLockItHolds(t *testing.T) {
	a, _ := newSandboxApp(t)
	a.toolReg.SetShellWindowsUTF8(func() bool { return a.configSnapshot().Tools.shellWindowsUTF8() }) // as boot.go wires it
	if !slices.Contains(a.toolReg.Names(), "shell") {
		t.Skip("this host registers no shell tool, so no description here reads the configuration")
	}
	within := func(what string, f func() error) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- f() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not return within 10 s: it waits on the configuration lock it holds", what)
		}
	}

	within("the dashboard's switch", func() error { return a.setToolEnabled("read", false) })
	if a.toolReg.ToolEnabled("read") {
		t.Fatal("the switch returned and the read tool is still on")
	}
	if !slices.Contains(a.configSnapshot().Tools.Disabled, "read") {
		t.Fatalf("the saved configuration does not hold the read tool off: %v", a.configSnapshot().Tools.Disabled)
	}

	// A reload applies a changed tools.disabled under cfgMu, as reload does.
	within("a reload's switches", func() error {
		a.cfgMu.Lock()
		defer a.cfgMu.Unlock()
		return applyDisabledTools(a.toolReg, nil)
	})
	if !a.toolReg.ToolEnabled("read") {
		t.Fatal("a reload with no tool switched off left the read tool off")
	}
}
