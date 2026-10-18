//go:build linux && !android

package install

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// WHAT STARTS THE PROGRAM IS NAMED IN EVERY DIRECTORY OF THE ACCOUNT'S
// THAT ITS SERVICE MANAGER READS USER UNITS FROM, AND WITH ITS DROP-INS.
// A unit of the host's name written to the control, data or runtime
// directory is read as the host's unit, and a drop-in beside a unit is
// read into it, so naming the two machine units as files in the one
// directory the installer writes to left both open. The names are stems,
// which hold <unit>.d with the unit. Where the manager's own tool is
// installed it is asked for its search path under a person of this
// test's making, and every directory of that person's it prints must be
// one this names: the list is the manager's, not ours.
func TestStartFilesNameEveryUserUnitDirectoryAndEachUnitsDropIns(t *testing.T) {
	person := t.TempDir()
	t.Setenv("HOME", person)
	t.Setenv("SUDO_USER", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(person, "run"))
	files, stems, err := StartFiles()
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := userUnitDirs()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(person, ".config", "systemd", "user"), filepath.Join(person, ".config", "systemd", "user.control"),
		filepath.Join(person, ".local", "share", "systemd", "user"),
		filepath.Join(person, "run", "systemd", "user"), filepath.Join(person, "run", "systemd", "transient"),
	} {
		if !slices.Contains(dirs, dir) {
			t.Fatalf("the account's unit directory %s is not named: %v", dir, dirs)
		}
	}
	for _, dir := range dirs {
		for _, unit := range []string{MachineUnit, MachineSocket, Unit} {
			if !slices.Contains(stems, filepath.Join(dir, unit)) {
				t.Errorf("%s is not held in %s: a unit there, and its drop-in directory, would be read as the host's", unit, dir)
			}
		}
	}
	// A stem holds what begins with it: the unit, its drop-in directory,
	// and an identity's unit of any slot.
	begins := func(path string) bool {
		return slices.ContainsFunc(stems, func(stem string) bool { return strings.HasPrefix(path, stem) })
	}
	unitDir, _ := userUnitDir()
	for _, path := range []string{
		filepath.Join(unitDir, MachineUnit), filepath.Join(unitDir, MachineUnit+".d", "override.conf"),
		filepath.Join(unitDir, MachineSocket+".d", "override.conf"), filepath.Join(unitDir, Unit+"identity-1.service"),
		filepath.Join(unitDir, Unit+".service.d", "override.conf"), filepath.Join(unitDir+".control", MachineUnit),
		filepath.Join(person, "run", "systemd", "transient", Unit+"identity-1.service"),
	} {
		if !begins(path) {
			t.Errorf("%s starts or changes what starts the program and no stem holds it", path)
		}
	}
	for _, unit := range []string{MachineUnit, MachineSocket} {
		link, _ := wantsLink(unit)
		if !slices.Contains(files, link) {
			t.Errorf("the enable link of %s is not named", unit)
		}
	}

	out, err := exec.Command("systemd-analyze", "--user", "unit-paths").Output()
	if err != nil {
		t.Logf("the service manager's own tool did not answer here (%v): its search path was not compared", err)
		return
	}
	asked := 0
	for _, line := range strings.Split(string(out), "\n") {
		dir := filepath.Clean(strings.TrimSpace(line))
		if !strings.HasPrefix(dir, person+string(filepath.Separator)) {
			continue
		}
		asked++
		if !slices.Contains(dirs, dir) {
			t.Errorf("the service manager reads user units from %s, of this account, and it is not named", dir)
		}
	}
	if asked == 0 {
		t.Logf("the service manager's tool printed no directory of this test's person: its search path was not compared:\n%s", out)
	} else {
		t.Logf("the service manager's search path was compared: %d directories of the account, all named", asked)
	}
}
