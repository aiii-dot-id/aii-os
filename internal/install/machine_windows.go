//go:build windows

package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

const MachineValue = Unit + "machine"

const installedDir = `Programs\AII OS`

func installedPrograms() []InstalledProgram {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return nil
	}
	exe := filepath.Join(local, installedDir, "aii.exe")
	if st, err := os.Stat(exe); err == nil && st.Mode().IsRegular() {
		return []InstalledProgram{{Exe: exe}}
	}
	return nil
}

func RegisterMachine(program, link string) error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot register the machine service here: %s", cap.Reason)
	}
	if err := OwnAccount(); err != nil {
		return err
	}
	mdir, err := MachineDir()
	if err != nil {
		return err
	}
	if err := ensureMachineDir(mdir); err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open HKCU\\%s: %w", runKey, err)
	}
	defer k.Close()
	if err := k.SetStringValue(MachineValue, machineCommandLine(program)); err != nil {
		return fmt.Errorf("write the machine service's autostart entry: %w", err)
	}
	if terr := registerMachineTask(program); terr != nil {
		if serr := startMachine(program); serr != nil {
			return errors.Join(terr, serr)
		}
		return fmt.Errorf("the machine service is registered for sign-in and started, but its task is not, so an identity cannot start it again: %w", terr)
	}
	return runMachineTask()
}

func machineCommandLine(program string) string {
	l, args := machineLauncher(program)
	return `"` + l + `" ` + args
}

func machineLauncher(program string) (exe, args string) {
	dir := filepath.Dir(program)
	for _, name := range []string{"AII OS.exe", "aii-app.exe"} {
		if l := filepath.Join(dir, name); fileExists(l) {
			return l, "--machine"
		}
	}
	return program, "machine"
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func StartMachine(program string) error { return startMachine(program) }

func startMachine(program string) error {
	p, err := StartDetached(func() *exec.Cmd {
		c := exec.Command(program, "machine")
		c.Dir = filepath.Dir(program)
		return c
	})
	if err != nil {
		return fmt.Errorf("start the machine service: %w", err)
	}
	return p.Release()
}

func StartRegisteredMachine() error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot start the machine service here: %s", cap.Reason)
	}
	if err := machineStartAllowed(); err != nil {
		return err
	}
	return runMachineTask()
}

var ErrWrongSession = errors.New("the machine service is not started from here")

func machineStartAllowed() error {
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		return fmt.Errorf("%w: this process's session does not read: %v", ErrWrongSession, err)
	}
	if session == 0 {
		return fmt.Errorf("%w: this process runs in session 0, where ssh and WinRM run and no person's desktop is; the service starts at the next sign-in, or run aii machine in the desktop's own session", ErrWrongSession)
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("%w: this process is elevated, and the service runs with the person's ordinary token; start it from an ordinary process", ErrWrongSession)
	}
	return nil
}

func machineTaskName() (string, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("read this account: %w", err)
	}
	return "AII OS machine " + tu.User.Sid.String(), nil
}

func registerMachineTask(program string) error {
	name, err := machineTaskName()
	if err != nil {
		return err
	}
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	exe, args := machineLauncher(program)
	xml := machineTaskXML(tu.User.Sid.String(), exe, args, filepath.Dir(program))
	f, err := os.CreateTemp("", "aii-machine-task-*.xml")
	if err != nil {
		return fmt.Errorf("write the task definition: %w", err)
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(utf16LE(xml))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write the task definition: %w", werr)
	}
	if out, err := schtasks("/Create", "/F", "/TN", name, "/XML", f.Name()); err != nil {
		return fmt.Errorf("register the machine service's task: %v: %s", err, out)
	}
	return nil
}

func runMachineTask() error {
	name, err := machineTaskName()
	if err != nil {
		return err
	}
	if out, err := schtasks("/Run", "/TN", name); err != nil {
		return fmt.Errorf("start the machine service's task (%s): %v: %s — register it from the installed program: aii machine register", name, err, out)
	}
	return nil
}

func deleteMachineTask() error {
	name, err := machineTaskName()
	if err != nil {
		return err
	}
	out, derr := schtasks("/Delete", "/F", "/TN", name)
	if _, qerr := schtasks("/Query", "/TN", name); qerr == nil {
		if derr != nil {
			return fmt.Errorf("the machine service's task %s is still registered: its delete failed: %v: %s", name, derr, out)
		}
		return fmt.Errorf("the machine service's task %s is still registered after its delete", name)
	}
	return nil
}

func schtasks(args ...string) (string, error) {
	c := exec.Command("schtasks", args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func utf16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 2+2*len(u))
	out[0], out[1] = 0xff, 0xfe
	for i, r := range u {
		out[2+2*i], out[3+2*i] = byte(r), byte(r>>8)
	}
	return out
}

func UnregisterMachine() error {
	if err := OwnAccount(); err != nil {
		return err
	}
	dir, err := MachineDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := stopDirectory(ctx, dir); err != nil && !errors.Is(err, ErrNotRunning) {
		return fmt.Errorf("stop the machine service: %w", err)
	}
	if err := deleteMachineTask(); err != nil {
		return err
	}
	return removeRunValue(MachineValue)
}

var startupApprovedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`

func MachineRunDisabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	b, _, err := k.GetBinaryValue(MachineValue)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return startupApprovedDisabled(b), nil
}

func MachineRegistration() (program string, registered bool, err error) {
	v, ok, err := runValue(MachineValue)
	if err != nil || !ok {
		return "", false, err
	}
	exe := firstQuoted(v)
	if exe == "" {
		return "", false, fmt.Errorf("the machine service's autostart entry names no program: %q", v)
	}
	if strings.EqualFold(filepath.Base(exe), "aii.exe") {
		return exe, true, nil
	}
	return filepath.Join(filepath.Dir(exe), "aii.exe"), true, nil
}

func MachineStartCommand() string {
	return "aii machine"
}

func StartupSlots() ([]StartupSlot, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open HKCU\\%s: %w", runKey, err)
	}
	defer k.Close()
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return nil, fmt.Errorf("read HKCU\\%s: %w", runKey, err)
	}
	var out []StartupSlot
	for _, name := range names {
		slot, ok := strings.CutPrefix(name, Unit)
		if !ok || !slotRE.MatchString(slot) {
			continue
		}
		v, _, err := k.GetStringValue(name)
		if err != nil {
			continue
		}
		if d := quotedAfter(v, "--dir"); d != "" {
			out = append(out, StartupSlot{Slot: slot, Dir: d})
		}
	}
	return out, nil
}

func runValue(name string) (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("open HKCU\\%s: %w", runKey, err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func firstQuoted(cmdline string) string {
	rest, ok := strings.CutPrefix(strings.TrimSpace(cmdline), `"`)
	if !ok {
		return ""
	}
	p, _, _ := strings.Cut(rest, `"`)
	return p
}

func quotedAfter(cmdline, flag string) string {
	_, rest, ok := strings.Cut(cmdline, flag+` "`)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, `"`)
	return v
}

func ReleaseVersion(exe string) string { return "" }

func StartFiles() (files, stems []string, err error) { return nil, nil, nil }
