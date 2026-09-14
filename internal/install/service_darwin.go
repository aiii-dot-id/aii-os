//go:build darwin && !ios

// .
// .
// .

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

// .
// .
const Unit = "id.aiii.aii-os."

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
var agentDirRel = filepath.Join("Library", "LaunchAgents")

// .
// .
func Register(slot string) (started bool, notes []string, err error) {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return false, []string{"cannot register a service here: " + cap.Reason}, nil
	}

	root, err := Root()
	if err != nil {
		return false, nil, err
	}
	home, err := operatorHome()
	if err != nil {
		return false, nil, err
	}
	dir := filepath.Join(root, slot)
	label := Unit + slot
	agentDir := filepath.Join(home, agentDirRel)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return false, nil, fmt.Errorf("create %s: %w", agentDir, err)
	}
	path := filepath.Join(agentDir, label+".plist")

	exe, err := os.Executable()
	if err != nil {
		return false, nil, fmt.Errorf("locate the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	if err := os.WriteFile(path, []byte(agentPlist(label, exe, dir)), 0o644); err != nil {
		return false, nil, fmt.Errorf("write %s: %w", path, err)
	}

	// .
	// .
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_, _ = run("launchctl", "bootout", domain+"/"+label)
	if out, err := run("launchctl", "bootstrap", domain, path); err != nil {
		if !strings.Contains(out, "already") {
			return false, notes, fmt.Errorf("load %s: %v: %s", label, err, out)
		}
	}
	notes = append(notes, "it will start again at login; macOS lists it under Settings → General → Login Items")
	return true, notes, nil
}

// .
// .
// .
func Unregister(slot string) error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot unregister a service here: %s", cap.Reason)
	}
	home, err := operatorHome()
	if err != nil {
		return err
	}
	label := Unit + slot
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if out, err := run("launchctl", "bootout", domain+"/"+label); err != nil {
		if !strings.Contains(out, "not find") && !strings.Contains(out, "No such") {
			return fmt.Errorf("unload %s: %v: %s", label, err, out)
		}
	}
	if err := os.Remove(filepath.Join(home, agentDirRel, label+".plist")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

// .
// .
func StartCommand(slot, dir string) string {
	return "aii register " + slot
}

func agentPlist(label, exe, dir string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + exe + `</string>
		<string>-dir</string>
		<string>` + dir + `</string>
	</array>
	<key>WorkingDirectory</key>
	<string>` + dir + `</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`
}

func run(name string, args ...string) (string, error) {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return "", fmt.Errorf("subprocesses unavailable: %s", cap.Reason)
	}
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// .
// .
// .
// .
// .
func Stop(slot string) error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot stop a service here: %s", cap.Reason)
	}
	label := Unit + slot
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if out, err := run("launchctl", "kill", "SIGTERM", domain+"/"+label); err != nil {
		if strings.Contains(out, "not find") || strings.Contains(out, "No such") {
			return nil
		}
		return fmt.Errorf("stop %s: %v: %s", label, err, out)
	}
	return nil
}
