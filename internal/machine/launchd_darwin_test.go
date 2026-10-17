//go:build darwin && !ios

package machine

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// UNDER LAUNCHD THE SERVICE TAKES THE LINK ITS AGENT'S SOCKETS KEY HOLDS,
// and a client that connects while the job is between runs waits for the
// next one. Opt-in (AII_LAUNCHD_LIVE=1): it bootstraps a throwaway agent
// in this account's gui domain, from a plist in a temporary directory,
// and boots it out at the end. The job is this test binary in a child
// mode that takes the link through Listen, answers one client, and exits;
// KeepAlive starts it again.
func TestUnderLaunchdTheServiceTakesTheAgentsLink(t *testing.T) {
	if os.Getenv("AII_MACHINE_LAUNCHD_CHILD") != "" {
		launchdChild()
		return
	}
	if os.Getenv("AII_LAUNCHD_LIVE") != "1" {
		t.Skip("set AII_LAUNCHD_LIVE=1 to bootstrap a throwaway LaunchAgent")
	}
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	path, _ := LinkPath(dir)
	bin := filepath.Join(root, "svc")
	copyExecutable(t, bin)
	label := "id.aiii.aii-os.test-link-" + strconv.Itoa(os.Getpid())
	plist := filepath.Join(root, "agent.plist")
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + label + `</string>
<key>ProgramArguments</key><array><string>` + bin + `</string><string>-test.run=^TestUnderLaunchdTheServiceTakesTheAgentsLink$</string></array>
<key>EnvironmentVariables</key><dict><key>AII_MACHINE_LAUNCHD_CHILD</key><string>` + dir + `</string></dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>StandardErrorPath</key><string>` + filepath.Join(root, "err.log") + `</string>
<key>Sockets</key><dict><key>` + launchdSocketName + `</key><dict><key>SockPathName</key><string>` + path + `</string><key>SockPathMode</key><integer>384</integer></dict></dict>
</dict></plist>
`
	if err := os.WriteFile(plist, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if out, err := exec.Command("launchctl", "bootstrap", domain, plist).CombinedOutput(); err != nil {
		t.Fatalf("bootstrap: %v: %s", err, out)
	}
	t.Cleanup(func() { exec.Command("launchctl", "bootout", domain+"/"+label).Run() })
	// launchd makes the socket once the agent is loaded, not within the
	// bootstrap call.
	waitFor(t, "launchd makes the agent's socket", func() bool { _, err := os.Stat(path); return err == nil })
	for i := 0; i < 2; i++ {
		start := time.Now()
		c, err := net.DialTimeout("unix", path, 2*time.Second)
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
		line, err := bufio.NewReader(c).ReadString('\n')
		c.Close()
		if err != nil || !strings.HasPrefix(line, "managed=true") {
			errLog, _ := os.ReadFile(filepath.Join(root, "err.log"))
			t.Fatalf("client %d: %q, %v\n%s", i, line, err, errLog)
		}
		t.Logf("client %d answered after %s: %s", i, time.Since(start).Round(time.Millisecond), strings.TrimSpace(line))
	}
}

func launchdChild() {
	h, err := Listen(os.Getenv("AII_MACHINE_LAUNCHD_CHILD"))
	if err != nil {
		os.Stderr.WriteString("listen: " + err.Error() + "\n")
		time.Sleep(time.Second)
		return
	}
	c, err := h.Listener().Accept()
	if err == nil {
		c.Write([]byte("managed=" + strconv.FormatBool(h.managed) + " pid=" + strconv.Itoa(os.Getpid()) + "\n"))
		c.Close()
	}
	h.Close()
}
