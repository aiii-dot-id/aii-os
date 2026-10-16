//go:build darwin && !ios

package install

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

const MachineLabel = Unit + "machine"

const appName = "AII OS.app"

func installedPrograms() []InstalledProgram {
	var out []InstalledProgram
	var dirs []string
	if home, err := operatorHome(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	dirs = append(dirs, "/Applications")
	for _, d := range dirs {
		app := filepath.Join(d, appName)
		exe := filepath.Join(app, "Contents", "MacOS", "aii")
		if st, err := os.Stat(exe); err != nil || !st.Mode().IsRegular() {
			continue
		}
		out = append(out, InstalledProgram{Exe: exe, Version: bundleVersion(app)})
	}
	return out
}

func bundleVersion(app string) string {
	v, err := readPlist(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	s, _ := v["CFBundleShortVersionString"].(string)
	return strings.TrimSpace(s)
}

func ReleaseVersion(exe string) string {
	macOS := filepath.Dir(exe)
	contents := filepath.Dir(macOS)
	app := filepath.Dir(contents)
	if filepath.Base(macOS) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(app, ".app") {
		return ""
	}
	return bundleVersion(app)
}

func machineAgentPath() (string, error) {
	home, err := operatorHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, agentDirRel, MachineLabel+".plist"), nil
}

func RegisterMachine(program, link string) error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot register the machine service here: %s", cap.Reason)
	}
	if err := OwnAccount(); err != nil {
		return err
	}
	path, err := machineAgentPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := ensureMachineDir(filepath.Dir(link)); err != nil {
		return err
	}
	want := []byte(machineAgentPlist(MachineLabel, program, link))
	old, _ := os.ReadFile(path)
	target := "gui/" + strconv.Itoa(os.Getuid())

	_, notLoaded := run("launchctl", "print", target+"/"+MachineLabel)
	loaded := notLoaded == nil
	if !bytes.Equal(old, want) {
		if published, err := atomicfile.WriteReplace(path, want, 0o644); err != nil {
			if published {
				return fmt.Errorf("write %s: published but not durable: %w", path, err)
			}
			return fmt.Errorf("write %s: %w", path, err)
		}

		if loaded {
			if out, err := run("launchctl", "bootout", target+"/"+MachineLabel); err != nil && exitStatus(err) != launchctlNothingThere {
				return fmt.Errorf("unload %s to replace it: %v: %s", MachineLabel, err, out)
			}
			loaded = false
		}
	}
	if !loaded {
		if out, err := run("launchctl", "bootstrap", target, path); err != nil {
			return fmt.Errorf("load %s: %v: %s", MachineLabel, err, out)
		}
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
	path, err := machineAgentPath()
	if err != nil {
		return err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if out, err := run("launchctl", "bootout", domain+"/"+MachineLabel); err != nil && exitStatus(err) != launchctlNothingThere {
		return fmt.Errorf("unload %s: %v: %s", MachineLabel, err, out)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func MachineRegistration() (program string, registered bool, err error) {
	path, err := machineAgentPath()
	if err != nil {
		return "", false, err
	}
	v, err := readPlist(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	args, _ := v["ProgramArguments"].([]string)
	if len(args) == 0 {
		return "", false, fmt.Errorf("%s names no program", path)
	}
	return args[0], true, nil
}

func MachineStartCommand() string {
	return "launchctl kickstart gui/" + strconv.Itoa(os.Getuid()) + "/" + MachineLabel
}

func StartupSlots() ([]StartupSlot, error) {
	home, err := operatorHome()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, agentDirRel)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []StartupSlot
	for _, e := range entries {
		slot, ok := strings.CutPrefix(e.Name(), Unit)
		if !ok {
			continue
		}
		slot, ok = strings.CutSuffix(slot, ".plist")
		if !ok || !slotRE.MatchString(slot) {
			continue
		}
		v, err := readPlist(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		args, _ := v["ProgramArguments"].([]string)
		if d := argAfter(args, "-dir"); d != "" {
			out = append(out, StartupSlot{Slot: slot, Dir: d})
		}
	}
	return out, nil
}

func argAfter(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func machineAgentPlist(label, program, link string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + xmlText(label) + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlText(program) + `</string>
		<string>machine</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>` + agentProcessType + `</string>
	<key>Sockets</key>
	<dict>
		<key>Link</key>
		<dict>
			<key>SockPathName</key>
			<string>` + xmlText(link) + `</string>
			<key>SockPathMode</key>
			<integer>384</integer>
		</dict>
	</dict>
</dict>
</plist>
`
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func readPlist(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(raw, []byte("bplist")) {
		return nil, fmt.Errorf("%s is a binary property list", path)
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	out := map[string]any{}
	depth, key := 0, ""
	var inArray bool
	var arr []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case depth == 3 && t.Name.Local == "key":
				var k string
				if err := dec.DecodeElement(&k, &t); err != nil {
					return nil, fmt.Errorf("parse %s: %w", path, err)
				}
				key = k
				depth--
			case depth == 3 && t.Name.Local == "string":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, fmt.Errorf("parse %s: %w", path, err)
				}
				out[key] = s
				depth--
			case depth == 3 && t.Name.Local == "array":
				inArray, arr = true, nil
			case depth == 4 && inArray && t.Name.Local == "string":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, fmt.Errorf("parse %s: %w", path, err)
				}
				arr = append(arr, s)
				depth--
			}
		case xml.EndElement:
			if depth == 3 && inArray && t.Name.Local == "array" {
				out[key] = arr
				inArray = false
			}
			depth--
		}
	}
}

func MachineRunDisabled() (bool, error) { return false, nil }

func StartFiles() (files, stems []string, err error) {
	home, err := operatorHome()
	if err != nil {
		return nil, nil, err
	}
	return nil, []string{filepath.Join(home, agentDirRel, Unit)}, nil
}
