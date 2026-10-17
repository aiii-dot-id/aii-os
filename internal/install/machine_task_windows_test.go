//go:build windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// THE MACHINE SERVICE IS STARTED BY THE ACCOUNT'S TASK, with the settings
// it was registered with, in the person's session. Opt-in
// (AII_WINDOWS_TASK_LIVE=1): it registers a task of its own name for a
// script that leaves a marker, runs it as an identity host would, reads
// the registered settings back, and deletes it. Run it in the desktop's
// session: from session 0 (ssh) the start is refused, which the second
// half shows instead.
func TestTheAccountsTaskStartsTheServiceWithItsSettings(t *testing.T) {
	if err := machineStartAllowed(); err != nil {
		t.Logf("this process may not start the service: %v", err)
		if !strings.Contains(err.Error(), "session 0") && !strings.Contains(err.Error(), "elevated") {
			t.Fatalf("the refusal must name the session or the token: %v", err)
		}
		return
	}
	if os.Getenv("AII_WINDOWS_TASK_LIVE") != "1" {
		t.Skip("set AII_WINDOWS_TASK_LIVE=1 to register and run a throwaway task")
	}
	// The one test that asks the real task scheduler, for a task of its
	// own name: past TestMain's refusing schtasks.
	t.Setenv("PATH", realPATH)
	dir := t.TempDir()
	marker := filepath.Join(dir, "started.txt")
	script := filepath.Join(dir, "start.cmd")
	if err := os.WriteFile(script, []byte("@echo started> \""+marker+"\"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	name := "AII OS machine test " + tu.User.Sid.String()
	xml := filepath.Join(dir, "task.xml")
	if err := os.WriteFile(xml, utf16LE(machineTaskXML(tu.User.Sid.String(), script, "", dir)), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := schtasks("/Create", "/F", "/TN", name, "/XML", xml); err != nil {
		t.Fatalf("register: %v: %s", err, out)
	}
	t.Cleanup(func() { schtasks("/Delete", "/F", "/TN", name) })
	if out, err := schtasks("/Run", "/TN", name); err != nil {
		t.Fatalf("run: %v: %s", err, out)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the task did not run its action")
		}
		time.Sleep(200 * time.Millisecond)
	}
	out, err := schtasks("/Query", "/XML", "/TN", name)
	if err != nil {
		t.Fatalf("query: %v: %s", err, out)
	}
	for _, want := range []string{"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>", "<Priority>5</Priority>", "<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>", "<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>", "<LogonType>InteractiveToken</LogonType>"} {
		if !strings.Contains(out, want) {
			t.Errorf("the registered task lacks %s:\n%s", want, out)
		}
	}

	if strings.Contains(out, "<RunLevel>HighestAvailable</RunLevel>") {
		t.Errorf("the registered task runs elevated:\n%s", out)
	}
}
