//go:build darwin && !ios

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE ACCOUNT'S STARTUP REGISTRATIONS ARE ITS OWN LAUNCHAGENTS: each slot
// agent with the directory its -dir names, read from the plist the slot's
// registration wrote. The machine agent and another program's are not
// slots.
func TestTheAccountsStartupSlotsAreItsAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	agents := filepath.Join(home, agentDirRel)
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(label, body string) {
		if err := os.WriteFile(filepath.Join(agents, label+".plist"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	slotDir := filepath.Join(home, Dir, "identity-3")
	write(Unit+"identity-3", agentPlist(Unit+"identity-3", "/Applications/AII OS.app/Contents/MacOS/aii", slotDir))
	write(MachineLabel, machineAgentPlist(MachineLabel, "/Applications/AII OS.app/Contents/MacOS/aii", filepath.Join(home, Dir, MachineDirName, "link")))
	write("com.example.other", agentPlist("com.example.other", "/usr/bin/true", "/tmp"))

	slots, err := StartupSlots()
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 || slots[0].Slot != "identity-3" || slots[0].Dir != slotDir {
		t.Fatalf("startup slots = %+v, want identity-3 in %s", slots, slotDir)
	}
	program, registered, err := MachineRegistration()
	if err != nil || !registered || program != "/Applications/AII OS.app/Contents/MacOS/aii" {
		t.Fatalf("the machine registration reads back: %q, %v, %v", program, registered, err)
	}
}

// THE MACHINE AGENT IS KEPT ALIVE WHOLE, RUNS INTERACTIVE, AND HAS LAUNCHD
// HOLD THE LINK (the Sockets key, owner-only), which the service takes
// through launch_activate_socket.
func TestTheMachineAgentKeepsItsLink(t *testing.T) {
	p := machineAgentPlist(MachineLabel, "/Users/a/Applications/AII OS.app/Contents/MacOS/aii", "/Users/a/.aii/machine/link")
	for _, want := range []string{"<key>KeepAlive</key>\n\t<true/>", "<string>machine</string>", "<string>Interactive</string>",
		"<key>Sockets</key>", "<key>Link</key>", "<string>/Users/a/.aii/machine/link</string>", "<key>SockPathMode</key>\n\t\t\t<integer>384</integer>"} {
		if !strings.Contains(p, want) {
			t.Errorf("the machine agent lacks %q:\n%s", want, p)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.plist")
	if err := os.WriteFile(path, []byte(p), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := readPlist(path)
	if err != nil {
		t.Fatal(err)
	}
	if args, _ := v["ProgramArguments"].([]string); len(args) != 2 || args[1] != "machine" {
		t.Fatalf("the agent's program arguments read back as %v", v["ProgramArguments"])
	}
}

// AN INSTALLED APP'S VERSION IS THE ONE ITS RELEASE RECORDS in the bundle,
// and the installed copies are found in ~/Applications before
// /Applications.
func TestAnInstalledAppsVersionIsItsBundles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	app := filepath.Join(home, "Applications", appName)
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(app, "Contents", "MacOS", "aii")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>0.1.15</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := ReleaseVersion(exe); v != "0.1.15" {
		t.Fatalf("ReleaseVersion = %q", v)
	}
	if v := ReleaseVersion("/usr/local/bin/aii"); v != "" {
		t.Fatalf("a program outside a bundle records no version beside it: %q", v)
	}
	got := installedPrograms()
	if len(got) == 0 || got[0].Exe != exe || got[0].Version != "0.1.15" {
		t.Fatalf("installed programs = %+v; the account's own copy first", got)
	}
}
