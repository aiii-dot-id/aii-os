//go:build linux && !android

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE ACCOUNT'S STARTUP REGISTRATIONS ARE ITS ENABLED SLOT UNITS: each
// aii-os@<slot> instance its user manager wants, run in ~/.aii/<slot> as
// the template says. The machine units are not slots.
func TestTheAccountsStartupSlotsAreItsEnabledUnits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	wants := filepath.Join(home, ".config", "systemd", "user", "default.target.wants")
	if err := os.MkdirAll(wants, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"aii-os@identity-2.service", MachineUnit, "other.service"} {
		if err := os.Symlink("/usr/lib/systemd/user/x", filepath.Join(wants, name)); err != nil {
			t.Fatal(err)
		}
	}
	slots, err := StartupSlots()
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 || slots[0].Slot != "identity-2" || slots[0].Dir != filepath.Join(home, Dir, "identity-2") {
		t.Fatalf("startup slots = %+v", slots)
	}
}

// THE MACHINE SERVICE RESTARTS ALWAYS AND TAKES ITS LINK FROM ITS SOCKET
// UNIT, which holds it owner-only across a restart.
func TestTheMachineUnitsKeepTheirLink(t *testing.T) {
	u := machineUnitFile("/usr/bin/aii")
	for _, want := range []string{`ExecStart="/usr/bin/aii" machine`, "Restart=always", "Requires=" + MachineSocket, "After=" + MachineSocket, "WantedBy=default.target"} {
		if !strings.Contains(u, want) {
			t.Errorf("the service unit lacks %q:\n%s", want, u)
		}
	}
	s := machineSocketFile("/run/user/0/aii-machine/link")
	for _, want := range []string{"ListenStream=/run/user/0/aii-machine/link", "SocketMode=0600", "DirectoryMode=0700", "WantedBy=sockets.target"} {
		if !strings.Contains(s, want) {
			t.Errorf("the socket unit lacks %q:\n%s", want, s)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, MachineUnit), []byte(u), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, ok, err := MachineRegistration(); err != nil || !ok || p != "/usr/bin/aii" {
		t.Fatalf("the machine registration reads back: %q, %v, %v", p, ok, err)
	}
}
