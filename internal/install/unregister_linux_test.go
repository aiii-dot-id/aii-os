//go:build linux && !android

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE MACHINE UNITS GONE MEANS GONE (L4): unregistering the service
// succeeds when its unit files are already absent — systemd refuses to
// disable a unit whose file does not exist, which made a second unregister,
// or one after apt purge, fail — and it stops a unit still running whose
// file was removed under it. Gone is verified, not inferred from an exit
// status: no unit file, no enable link, nothing running.
func TestTheMachineUnitsGoneMeansGone(t *testing.T) {
	home := ownHome(t)
	m := newFakeManager(t, home)
	link := filepath.Join(home, "run", "aii-machine", "link")
	if err := RegisterMachine("/usr/bin/aii", link); err != nil {
		t.Fatal(err)
	}
	if !active(m.state, MachineUnit) || !active(m.state, MachineSocket) {
		t.Fatal("rig: the registered units do not run")
	}
	// Removed under the running units (an operator's rm, a purge).
	dir := filepath.Join(home, ".config", "systemd", "user")
	for _, n := range []string{MachineUnit, MachineSocket} {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := UnregisterMachine(); err != nil {
		t.Fatalf("unregistering units whose files are gone: %v", err)
	}
	if active(m.state, MachineUnit) || active(m.state, MachineSocket) {
		t.Fatal("a unit whose file was gone was left running")
	}
	if err := MachineGone(); err != nil {
		t.Fatalf("the machine service is not gone: %v", err)
	}
	if err := UnregisterMachine(); err != nil {
		t.Fatalf("unregistering again: %v", err)
	}
	// Gone is checked, never assumed: a unit running is not gone.
	if err := os.WriteFile(filepath.Join(m.state, MachineUnit), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MachineGone(); err == nil || !strings.Contains(err.Error(), MachineUnit) {
		t.Fatalf("a running unit read as gone: %v", err)
	}
}

// THE LINK'S SOCKET IS REMOVED ON STOP, and the link's own directory with
// it once empty: the socket unit kept its node in the runtime directory
// after the service was unregistered (RemoveOnStop defaults to off).
func TestTheLinksSocketIsRemovedOnStop(t *testing.T) {
	if s := machineSocketFile("/run/user/0/aii-machine/link"); !strings.Contains(s, "\nRemoveOnStop=yes\n") {
		t.Fatalf("the socket unit does not remove its node on stop:\n%s", s)
	}
	home := ownHome(t)
	newFakeManager(t, home)
	linkDir := filepath.Join(home, "run", "aii-machine")
	if err := RegisterMachine("/usr/bin/aii", filepath.Join(linkDir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := UnregisterMachine(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(linkDir); !os.IsNotExist(err) {
		t.Fatalf("the link's empty directory was left: %v", err)
	}
	// A directory that still holds something is not the service's to empty.
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linkDir, "other"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UnregisterMachine(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(linkDir, "other")); err != nil {
		t.Fatalf("unregistering removed what it did not make: %v", err)
	}
}
