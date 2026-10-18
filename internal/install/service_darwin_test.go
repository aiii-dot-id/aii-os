//go:build darwin && !ios

package install

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeLaunchctl puts a launchctl first on PATH that prints say and exits
// with status, replaying what the real one answered on macOS 27.0.
func fakeLaunchctl(t *testing.T, say string, status int) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho '" + say + "'\nexit " + strconv.Itoa(status) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// scratchHome is ownHome: a home of the test's own, taken for the
// account's (OwnAccount).
func scratchHome(t *testing.T) string { t.Helper(); return ownHome(t) }

// A STOP WITH NO PROCESS IS THE STATE ASKED FOR: launchd holds the agent
// and nothing runs (kill exits 3), which aii stop reports as not running.
func TestStopSaysNotRunningWhenLaunchdHoldsNoProcess(t *testing.T) {
	scratchHome(t)
	fakeLaunchctl(t, "No process to signal.", 3)
	if err := Stop("identity-90"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("got %v, want ErrNotRunning", err)
	}
}

// A STOP OF A SLOT LAUNCHD DOES NOT HOLD IS A FAILURE, in launchctl's own
// words: the slot is not registered, so nothing launchd runs was stopped.
func TestStopOfASlotLaunchdDoesNotHoldIsAFailure(t *testing.T) {
	scratchHome(t)
	fakeLaunchctl(t, "Could not find service id.aiii.aii-os.identity-90 in domain for user gui: 501", 113)
	err := Stop("identity-90")
	if err == nil || errors.Is(err, ErrNotRunning) || !strings.Contains(err.Error(), "Could not find service") {
		t.Fatalf("got %v, want launchctl's refusal", err)
	}
}

// UNREGISTER OF AN AGENT LAUNCHD NO LONGER HOLDS STILL REMOVES ITS PLIST:
// bootout exits 3 when there is nothing to unload.
func TestUnregisterOfALabelLaunchdDoesNotHoldRemovesThePlist(t *testing.T) {
	home := scratchHome(t)
	plist := filepath.Join(home, agentDirRel, Unit+"identity-90.plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeLaunchctl(t, "Boot-out failed: 3: No such process", 3)
	if err := Unregister("identity-90"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Fatalf("the plist was kept: %v", err)
	}
}

// UNREGISTER REPORTS A BOOTOUT LAUNCHD REFUSED, and keeps the plist: the
// agent may still be loaded, and a removed plist would hide it.
func TestUnregisterReportsABootoutLaunchdRefused(t *testing.T) {
	home := scratchHome(t)
	plist := filepath.Join(home, agentDirRel, Unit+"identity-90.plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeLaunchctl(t, "Boot-out failed: 5: Input/output error", 5)
	if err := Unregister("identity-90"); err == nil || !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("got %v, want launchctl's refusal", err)
	}
	if _, err := os.Stat(plist); err != nil {
		t.Fatalf("the plist must stay while the agent may be loaded: %v", err)
	}
}

func TestTheLaunchAgentRunsTheIdentityAsInteractiveWork(t *testing.T) {
	plist := agentPlist("id.aiii.aii-os.identity-9", "/Applications/AII OS.app/Contents/MacOS/aii", "/Users/x/.aii/identity-9")
	want := "\t<key>ProcessType</key>\n\t<string>Interactive</string>\n"
	if strings.Count(plist, "<key>ProcessType</key>") != 1 || !strings.Contains(plist, want) {
		t.Fatalf("the launch agent must name ProcessType Interactive exactly once:\n%s", plist)
	}
	for _, slow := range []string{"Background", "Adaptive"} {
		if strings.Contains(plist, "<string>"+slow+"</string>") {
			t.Fatalf("the launch agent names %s, which launchd runs at priority 4", slow)
		}
	}
}
