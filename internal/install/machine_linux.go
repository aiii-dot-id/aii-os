//go:build linux && !android

package install

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

const (
	MachineUnit   = "aii-os-machine.service"
	MachineSocket = "aii-os-machine.socket"
)

const installedProgram = "/usr/bin/aii"

func installedPrograms() []InstalledProgram {
	if st, err := os.Stat(installedProgram); err == nil && st.Mode().IsRegular() {
		return []InstalledProgram{{Exe: installedProgram}}
	}
	return nil
}

func userUnitDir() (string, error) {
	if c := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(c) {
		return filepath.Join(c, "systemd", "user"), nil
	}
	home, err := operatorHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
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
	dir, err := userUnitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	changed := false
	for name, body := range map[string]string{MachineSocket: machineSocketFile(link), MachineUnit: machineUnitFile(program)} {
		path := filepath.Join(dir, name)
		want := []byte(body)
		if old, _ := os.ReadFile(path); bytes.Equal(old, want) {
			continue
		}
		if published, err := atomicfile.WriteReplace(path, want, 0o644); err != nil {
			if published {
				return fmt.Errorf("write %s: published but not durable: %w", path, err)
			}
			return fmt.Errorf("write %s: %w", path, err)
		}
		changed = true
	}
	if changed {
		if out, err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return fmt.Errorf("reload the user manager: %v: %s", err, out)
		}

		if out, err := run("systemctl", "--user", "try-restart", MachineUnit); err != nil {
			return fmt.Errorf("restart %s: %v: %s", MachineUnit, err, out)
		}
	}
	if out, err := run("systemctl", "--user", "enable", "--now", MachineSocket, MachineUnit); err != nil {
		return fmt.Errorf("enable %s: %v: %s", MachineUnit, err, out)
	}
	return nil
}

func UnregisterMachine() error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot unregister the machine service here: %s", cap.Reason)
	}
	if err := OwnAccount(); err != nil {
		return err
	}
	dir, err := userUnitDir()
	if err != nil {
		return err
	}
	var present, absent []string
	for _, name := range []string{MachineSocket, MachineUnit} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			present = append(present, name)
		} else {
			absent = append(absent, name)
		}
	}
	var failed error
	if len(present) > 0 {
		if out, err := run("systemctl", append([]string{"--user", "disable", "--now"}, present...)...); err != nil {
			failed = fmt.Errorf("disable %s: %v: %s", strings.Join(present, " "), err, out)
		}
	}
	for _, name := range absent {

		_, _ = run("systemctl", "--user", "stop", name)
		if link, err := wantsLink(name); err == nil {
			if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				_ = os.Remove(link)
			}
		}
	}
	for _, name := range []string{MachineUnit, MachineSocket} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}
	if out, err := run("systemctl", "--user", "daemon-reload"); err != nil {
		failed = errors.Join(failed, fmt.Errorf("reload the user manager: %v: %s", err, out))
	}
	removeEmptyLinkDir()
	if err := MachineGone(); err != nil {
		return errors.Join(failed, err)
	}
	return nil
}

func removeEmptyLinkDir() {
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(rt) {
		return
	}
	_ = os.Remove(filepath.Join(rt, machineLinkDirName))
}

const machineLinkDirName = "aii-machine"

func wantsLink(unit string) (string, error) {
	dir, err := userUnitDir()
	if err != nil {
		return "", err
	}
	target := "default.target.wants"
	if strings.HasSuffix(unit, ".socket") {
		target = "sockets.target.wants"
	}
	return filepath.Join(dir, target, unit), nil
}

func MachineRegistration() (program string, registered bool, err error) {
	dir, err := userUnitDir()
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, MachineUnit)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "ExecStart="); ok {
			if p, _, ok := strings.Cut(strings.TrimSpace(v), " machine"); ok {
				return strings.Trim(p, `"`), true, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return "", false, fmt.Errorf("%s names no program", path)
}

func MachineStartCommand() string {
	return "systemctl --user start " + MachineUnit
}

func StartupSlots() ([]StartupSlot, error) {
	dir, err := userUnitDir()
	if err != nil {
		return nil, err
	}
	root, err := Root()
	if err != nil {
		return nil, err
	}
	wants := filepath.Join(dir, "default.target.wants")
	entries, err := os.ReadDir(wants)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", wants, err)
	}
	var out []StartupSlot
	for _, e := range entries {
		slot, ok := strings.CutPrefix(e.Name(), Unit)
		if !ok {
			continue
		}
		slot, ok = strings.CutSuffix(slot, ".service")
		if !ok || !slotRE.MatchString(slot) {
			continue
		}
		out = append(out, StartupSlot{Slot: slot, Dir: filepath.Join(root, slot)})
	}
	return out, nil
}

func machineUnitFile(program string) string {
	return `[Unit]
Description=AII OS machine service
Documentation=https://aiii.id
Requires=` + MachineSocket + `
After=` + MachineSocket + `

[Service]
Type=simple
ExecStart="` + program + `" machine
Restart=always
RestartSec=1s
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=default.target
`
}

func ReleaseVersion(exe string) string { return "" }

func machineSocketFile(link string) string {
	return `[Unit]
Description=AII OS machine service link
Documentation=https://aiii.id

[Socket]
ListenStream=` + link + `
SocketMode=0600
DirectoryMode=0700
RemoveOnStop=yes

[Install]
WantedBy=sockets.target
`
}

func MachineRunDisabled() (bool, error) { return false, nil }

func userUnitDirs() ([]string, error) {
	dir, err := userUnitDir()
	if err != nil {
		return nil, err
	}
	home, err := operatorHome()
	if err != nil {
		return nil, err
	}
	data := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) {
		data = filepath.Join(home, ".local", "share")
	}
	dirs := []string{dir, dir + ".control", filepath.Join(data, "systemd", "user")}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(rt) {
		for _, name := range []string{"user", "user.control", "transient", "generator", "generator.early", "generator.late"} {
			dirs = append(dirs, filepath.Join(rt, "systemd", name))
		}
	}
	return dirs, nil
}

func StartFiles() (files, stems []string, err error) {
	dirs, err := userUnitDirs()
	if err != nil {
		return nil, nil, err
	}
	for _, dir := range dirs {
		for _, unit := range []string{MachineUnit, MachineSocket, Unit} {
			stems = append(stems, filepath.Join(dir, unit))
		}
	}
	for _, unit := range []string{MachineUnit, MachineSocket} {
		link, err := wantsLink(unit)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, link)
	}
	enabled, err := wantsLink(Unit)
	if err != nil {
		return nil, nil, err
	}
	return files, append(stems, enabled), nil
}
