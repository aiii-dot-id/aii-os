//go:build darwin

package pluginhost

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAContainedNativeChildCannotHaveLaunchServicesStartAProgram(t *testing.T) {
	requireWall(t)
	dir := shortDir(t)
	marker := filepath.Join(dir, "started-outside")
	app := filepath.Join(dir, "Marker.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "Marker"), []byte("#!/bin/sh\n/usr/bin/touch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleExecutable</key><string>Marker</string><key>CFBundleIdentifier</key><string>id.example.wallprobe.marker</string><key>CFBundleName</key><string>Marker</string><key>CFBundlePackageType</key><string>APPL</string><key>LSUIElement</key><true/></dict></plist>
`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	started := func(wait time.Duration) bool {
		deadline := time.Now().Add(wait)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return false
	}
	for _, how := range []struct{ name, target string }{
		{"by its path", app},
		{"by an address", "file://" + app},
	} {
		ask := "start=/usr/bin/open|-g|" + how.target
		os.Remove(marker)
		out := runWallProbe(t, []string{fakechildBin, "reach", ask})
		if !strings.Contains(out, "start=ran") || !started(15*time.Second) {
			t.Skipf("rig (%s): uncontained, LaunchServices started nothing in this session, so its refusal cannot be shown:\n%s", how.name, out)
		}
		os.Remove(marker)
		argv, wall, err := containArgv(context.Background(), []string{fakechildBin, "reach", ask}, nil, nativeFiles{own: standInTools})
		if err != nil {
			t.Fatalf("containment refused on a host that can build it: %v", err)
		}
		out = runContained(t, argv, wall)
		if !strings.Contains(out, "start=ran exit=") {
			t.Errorf("%s: `open` must run and be refused by LaunchServices; the start rule is tried elsewhere:\n%s", how.name, out)
		}
		if started(5 * time.Second) {
			t.Errorf("%s: a program was started outside the wall at the contained child's request", how.name)
		}
		t.Logf("%s, inside the wall: %s", how.name, strings.TrimSpace(out))
	}
}

// A CONTAINED CHILD STARTS ONLY ITS OWN PROGRAMS: its image, and what is
// under the directories the host verified for its package. A system tool
// started from inside the wall is inside it too, so this is the layer
// under the rules that refuse the services; it keeps a child from
// borrowing a tool's standing with them.
func TestAContainedNativeChildStartsOnlyItsOwnPrograms(t *testing.T) {
	requireWall(t)
	own := t.TempDir()
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(own, "helper")
	if err := os.WriteFile(helper, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	asks := []string{"start=/usr/bin/true", "start=/bin/sh|-c|exit 0", "start=" + helper + "|reach"}
	out := runWallProbe(t, append([]string{fakechildBin, "reach"}, asks...))
	if strings.Count(out, "start=ran") != 3 {
		t.Fatalf("rig: uncontained, each program must start, or its refusal below proves nothing:\n%s", out)
	}
	argv, wall, err := containArgv(context.Background(), append([]string{fakechildBin, "reach"}, asks...), nil, nativeFiles{own: []string{own}})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(runContained(t, argv, wall)), "\n")
	if len(lines) != 3 {
		t.Fatalf("the probe did not report each attempt: %q", lines)
	}
	for i, system := range []bool{true, true, false} {
		switch refused := strings.HasPrefix(lines[i], "start=refused"); {
		case system && !refused:
			t.Errorf("the contained child started a program that is not its own (%s): %s", asks[i], lines[i])
		case !system && lines[i] != "start=ran":
			t.Errorf("the contained child could not start its own program (%s): %s", asks[i], lines[i])
		}
	}
}

// A CONTAINED CHILD READS NO OTHER TERMINAL. Measured under this wall's
// first rules: a contained process opened another terminal of the same
// user by its device path and read what was typed into it. A terminal is
// a file its owner may open, and the wall refused only writes. The
// terminal here is one the system's `script` makes for a shell that
// prints its name and waits; nothing is typed into it.
func TestAContainedNativeChildReadsNoOtherTerminal(t *testing.T) {
	requireWall(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	holder := exec.CommandContext(ctx, "/usr/bin/script", "-q", "/dev/null", "/bin/sh", "-c", "tty; sleep 60")
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Skipf("rig: this host cannot make a terminal with script: %v", err)
	}
	t.Cleanup(func() { cancel(); holder.Wait() })
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	terminal := regexp.MustCompile(`/dev/ttys[0-9]+`).FindString(line)
	if terminal == "" {
		t.Skipf("rig: script named no terminal: %q", line)
	}
	out := runWallProbe(t, []string{fakechildBin, "reach", "open=" + terminal})
	if !strings.Contains(out, "open=opened") {
		t.Fatalf("rig: uncontained, the probe must open the terminal, or its refusal below proves nothing:\n%s", out)
	}
	argv, wall, err := containArgv(context.Background(), []string{fakechildBin, "reach", "open=" + terminal, "open=/dev/tty"}, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	if out = runContained(t, argv, wall); strings.Count(out, "open=refused EPERM") != 2 {
		t.Errorf("inside the wall another terminal of the user, and the controlling terminal, must not open:\n%s", out)
	}
}

// buildReachProbe compiles the C probe for the two attempts Go cannot make
// without cgo. Where the system has no compiler the routes cannot be
// tried and the test says so.
func buildReachProbe(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("no xcrun: the C probe cannot be built here")
	}
	src, err := filepath.Abs("testdata/reachprobe/probe.c")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "reachprobe")
	if out, err := exec.Command("xcrun", "clang", "-O1", "-o", bin, src, "-framework", "IOKit", "-framework", "CoreFoundation").CombinedOutput(); err != nil {
		t.Skipf("the C probe does not build here (%v): %s", err, bytes.TrimSpace(out))
	}
	return bin
}

// A CONTAINED CHILD CHANGES NO SHARED MEMORY OF ANOTHER PROGRAM'S AND OPENS
// NO DEVICE DRIVER. Both were open under this wall's first rules, and both
// were found by the builder of the plugin the wall is closed for, reading
// it as its critic. Named shared memory is kept by a name, not a path, so
// "no write" did not see it: an object made outside was changed from
// inside. A connection to a device driver is how a program asks the
// machine itself for something; the power domain's is how one asks it to
// sleep. The object here is the test's own; the connection is opened and
// closed and nothing is asked of it.
func TestAContainedNativeChildChangesNoSharedMemoryAndOpensNoDriver(t *testing.T) {
	requireWall(t)
	probe := buildReachProbe(t)
	say := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(probe, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("the probe did not run (%v): %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	inside := func(args ...string) string {
		t.Helper()
		argv, wall, err := containArgv(context.Background(), append([]string{probe}, args...), nil, nativeFiles{})
		if err != nil {
			t.Fatalf("containment refused on a host that can build it: %v", err)
		}
		return strings.TrimSpace(runContained(t, argv, wall))
	}

	name := "/aiiw." + strconv.Itoa(os.Getpid())
	if out := say("make", name); out != "shm=wrote" {
		t.Skipf("rig: this host makes no named shared memory: %s", out)
	}
	t.Cleanup(func() { exec.Command(probe, "remove", name).Run() })
	if say("change", name) != "shm=wrote" || say("read", name) != "shm=INSIDE!" {
		t.Fatal("rig: uncontained, the probe must change the object, or its refusal below proves nothing")
	}
	if say("make", name) != "shm=wrote" || say("read", name) != "shm=OUTSIDE" {
		t.Fatal("rig: the object could not be set back")
	}
	if out := inside("change", name); !strings.HasPrefix(out, "shm=refused") {
		t.Errorf("the contained child opened shared memory another program made: %s", out)
	}
	if out := say("read", name); out != "shm=OUTSIDE" {
		t.Errorf("shared memory made outside the wall was changed from inside: %s", out)
	}

	if out := say("iokit"); out != "iokit=opened" {
		t.Skipf("rig: uncontained, the power domain did not open here (%s), so its refusal cannot be shown", out)
	}
	if out := inside("iokit"); !strings.HasPrefix(out, "iokit=refused") {
		t.Errorf("the contained child opened a connection to a device driver: %s", out)
	}
}
