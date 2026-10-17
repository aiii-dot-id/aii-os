//go:build windows

package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// The Run and StartupApproved keys here are the test binary's own subkeys
// (TestMain's guardRegistry), never the account's.

func setRunValue(t *testing.T, name, value string) {
	t.Helper()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if err := k.SetStringValue(name, value); err != nil {
		t.Fatal(err)
	}
}

// setApproved writes a StartupApproved record as Task Manager does when a
// person switches an entry off: its first byte odd.
func setApproved(t *testing.T, name string) {
	t.Helper()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, startupApprovedKey, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if err := k.SetBinaryValue(name, []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
}

func valueExists(t *testing.T, key, name string) bool {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	_, _, err = k.GetValue(name, nil)
	return err == nil
}

func TestTheStartupApprovedRecordGoesWithItsRunValue(t *testing.T) {
	ownHome(t)
	slotValue := Unit + "identity-77"
	setRunValue(t, slotValue, `"C:\x\AII OS.exe" --startup --dir "C:\y\identity-77"`)
	setApproved(t, slotValue)
	if err := removeAutostart("identity-77"); err != nil {
		t.Fatal(err)
	}
	if valueExists(t, runKey, slotValue) || valueExists(t, startupApprovedKey, slotValue) {
		t.Fatal("a slot's Run value or its StartupApproved record outlived its removal")
	}
	setRunValue(t, MachineValue, `"C:\x\AII OS.exe" --machine`)
	setApproved(t, MachineValue)
	if disabled, err := MachineRunDisabled(); err != nil || !disabled {
		t.Fatalf("rig: the switched-off record does not read: %v %v", disabled, err)
	}
	if err := removeRunValue(MachineValue); err != nil {
		t.Fatal(err)
	}
	if valueExists(t, runKey, MachineValue) || valueExists(t, startupApprovedKey, MachineValue) {
		t.Fatal("the machine service's Run value or its StartupApproved record outlived its removal")
	}
	if disabled, err := MachineRunDisabled(); err != nil || disabled {
		t.Fatalf("a reinstall would inherit the person's old switch: %v %v", disabled, err)
	}
}

// fakeSchtasks puts a schtasks first on PATH that holds a task while its
// marker exists: /Query answers 0 while it does; /Delete removes it unless
// outlive is set (a task the scheduler kept), and answers 0 either way.
func fakeSchtasks(t *testing.T, present, outlive bool) string {
	t.Helper()
	bin := t.TempDir()
	marker := filepath.Join(bin, "task")
	if present {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	del := `del "` + marker + `" 2>nul`
	if outlive {
		del = "rem the scheduler keeps it"
	}
	script := "@echo off\r\n" +
		"if /I \"%1\"==\"/Query\" (if exist \"" + marker + "\" (exit /b 0) else (echo ERROR: The system cannot find the file specified. 1>&2 & exit /b 1))\r\n" +
		"if /I \"%1\"==\"/Delete\" (" + del + " & exit /b 0)\r\n" +
		"exit /b 0\r\n"
	if err := os.WriteFile(filepath.Join(bin, "schtasks.cmd"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

func TestATaskThatOutlivesItsDeleteIsReported(t *testing.T) {
	fakeSchtasks(t, true, true)
	if err := deleteMachineTask(); err == nil || !strings.Contains(err.Error(), "still registered") {
		t.Fatalf("a task the scheduler kept: %v", err)
	}
	marker := fakeSchtasks(t, true, false)
	if err := deleteMachineTask(); err != nil {
		t.Fatalf("a task deleted: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("rig: the delete did not reach the fake")
	}
	fakeSchtasks(t, false, false)
	if err := deleteMachineTask(); err != nil {
		t.Fatalf("no task at all: %v", err)
	}
}

// UNREGISTERING IS VERIFIED AND IDEMPOTENT, as on the other rows: the Run
// value and its StartupApproved record gone, nothing listening on the stop
// channel, and a second unregister changes nothing.
func TestUnregisteringTwiceChangesNothingTheSecondTime(t *testing.T) {
	home := ownHome(t)
	fakeSchtasks(t, true, false)
	slot := SlotName(77)
	dir := filepath.Join(home, Dir, slot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	setRunValue(t, Unit+slot, `"C:\x\AII OS.exe" --startup --dir "`+dir+`"`)
	setApproved(t, Unit+slot)
	setRunValue(t, MachineValue, `"C:\x\AII OS.exe" --machine`)
	if SlotGone(slot) == nil || MachineGone() == nil {
		t.Fatal("rig: registered entries read as gone")
	}
	for round := 1; round <= 2; round++ {
		if err := Unregister(slot); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if err := UnregisterMachine(); err != nil {
			t.Fatalf("round %d: the machine service: %v", round, err)
		}
		if err := SlotGone(slot); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if err := MachineGone(); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("unregistering touched the slot: %v", err)
	}
}

func TestUnregisterRefusesInAForeignHome(t *testing.T) {
	home := ownHome(t)
	slot := SlotName(77)
	setRunValue(t, Unit+slot, `"C:\x\AII OS.exe" --startup --dir "C:\y"`)
	sandbox := filepath.Join(home, Dir, slot, "sandbox")
	if err := os.MkdirAll(filepath.Join(sandbox, Dir, slot), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", sandbox)
	var foreign *ForeignAccountError
	for name, act := range map[string]func() error{"unregister": func() error { return Unregister(slot) }, "unregister the machine": UnregisterMachine} {
		if err := act(); !errors.As(err, &foreign) {
			t.Errorf("%s under a foreign USERPROFILE: %v", name, err)
		}
	}
	if !valueExists(t, runKey, Unit+slot) {
		t.Fatal("a refused unregister removed the Run value")
	}
}
