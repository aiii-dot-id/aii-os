//go:build darwin

package pluginhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTheLaunchdResidualFromInsideTheWall(t *testing.T) {
	if os.Getenv("AII_WALL_LAUNCHD") != "1" {
		t.Skip("registers a job with the account's launchd: set AII_WALL_LAUNCHD=1")
	}
	requireWall(t)
	dir := shortDir(t)
	const launchctl = "/bin/launchctl"
	marker := filepath.Join(dir, "started-by-launchd")
	label := "id.aiii.wallprobe.residual." + strconv.Itoa(os.Getpid())
	plist := filepath.Join(dir, label+".plist")
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>` + label + `</string><key>ProgramArguments</key><array><string>/usr/bin/touch</string><string>` + marker + `</string></array><key>RunAtLoad</key><false/></dict></plist>
`
	if err := os.WriteFile(plist, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	uid := strconv.Itoa(os.Getuid())
	var domain string
	for _, d := range []string{"gui/" + uid, "user/" + uid} {
		if out, err := exec.Command("/bin/launchctl", "bootstrap", d, plist).CombinedOutput(); err == nil {
			domain = d
			break
		} else {
			t.Logf("rig: bootstrap in %s: %v %s", d, err, strings.TrimSpace(string(out)))
		}
	}
	if domain == "" {
		t.Skip("rig: the account's launchd took the test's job in no domain, so the route cannot be tried")
	}
	target := domain + "/" + label
	t.Cleanup(func() {
		exec.Command("/bin/launchctl", "enable", target).Run()
		exec.Command("/bin/launchctl", "bootout", target).Run()
	})
	started := func() bool {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return false
	}
	disabled := func() bool {
		out, _ := exec.Command("/bin/launchctl", "print-disabled", domain).CombinedOutput()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, `"`+label+`"`) && (strings.Contains(line, "=> disabled") || strings.Contains(line, "=> true")) {
				return true
			}
		}
		return false
	}
	ask := func(contained bool, args ...string) string {
		t.Helper()
		argv := append([]string{launchctl}, args...)
		if contained {
			wrapped, _, err := containArgv(t.Context(), argv, nil, nativeFiles{own: standInTools})
			if err != nil {
				t.Fatalf("containment refused on a host that can build it: %v", err)
			}
			argv = wrapped
		}
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		return strings.TrimSpace(string(out)) + " (" + errText(err) + ")"
	}

	// The control: uncontained, the copy starts the job and disables it.
	os.Remove(marker)
	if out := ask(false, "kickstart", target); !started() {
		t.Skipf("rig: uncontained, launchd did not start the test's job (%s), so the route cannot be tried", out)
	}
	if out := ask(false, "disable", target); !disabled() {
		t.Skipf("rig: uncontained, launchd did not disable the test's job (%s)", out)
	}
	exec.Command("/bin/launchctl", "enable", target).Run()

	// Behind the wall.
	os.Remove(marker)
	kick := ask(true, "kickstart", target)
	kicked := started()
	off := ask(true, "disable", target)
	turnedOff := disabled()
	t.Logf("behind the wall: kickstart %s -> started=%v; disable %s -> disabled=%v", kick, kicked, off, turnedOff)
	if !kicked || !turnedOff {
		t.Errorf("THE LAUNCHD RESIDUAL CLOSED (started=%v, disabled=%v): change KNOWN_LIMITATIONS, which says it is open, and this test with them", kicked, turnedOff)
	}
}

func errText(err error) string {
	if err == nil {
		return "exit 0"
	}
	return err.Error()
}
