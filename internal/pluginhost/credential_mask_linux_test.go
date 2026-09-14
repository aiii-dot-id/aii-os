//go:build linux

package pluginhost

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func between(t *testing.T, s, name string) string {
	t.Helper()
	i := strings.Index(s, name+"<")
	if i < 0 {
		t.Fatalf("marker %q missing in output:\n%s", name, s)
	}
	rest := s[i+len(name)+1:]
	j := strings.Index(rest, ">")
	if j < 0 {
		t.Fatalf("marker %q unterminated:\n%s", name, s)
	}
	return rest[:j]
}

// .
// .
// .
// .
// .
// .
func TestCredentialStoresAreMaskedInsideTheSandbox(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap absent; the fail-closed test covers refusal")
	}
	// .
	// .
	// .
	realShadow, err := os.ReadFile("/etc/shadow")
	if err != nil || len(strings.TrimSpace(string(realShadow))) == 0 {
		t.Skip("/etc/shadow not readable by the test itself; nothing to prove masked")
	}
	shadowFirst := strings.SplitN(strings.TrimSpace(string(realShadow)), "\n", 2)[0]

	script := `printf "SHADOW<%s>\n" "$(cat /etc/shadow 2>/dev/null)"; ` +
		`printf "ROOTSSH<%s>\n" "$(ls /root/.ssh 2>/dev/null)"; ` +
		`printf "OSREL<%s>\n" "$(head -c 40 /etc/os-release 2>/dev/null)"; ` +
		`printf "LIBS<%s>\n" "$(ls /usr/lib 2>/dev/null | head -1)"`
	argv, telemetry, err := containArgv([]string{"/bin/sh", "-c", script})
	if err != nil {
		t.Fatalf("containment refused: %v", err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if !strings.Contains(telemetry, "ssh and shadow files masked") {
		t.Fatalf("telemetry does not record what is actually masked: %q", telemetry)
	}
	if !strings.Contains(telemetry, "other user-readable credentials are NOT") {
		t.Fatalf("telemetry claims a mask without naming its limit — the sentence an operator plans around: %q", telemetry)
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxed probe did not run: %v\n%s", err, out)
	}
	s := string(out)

	// .
	if got := between(t, s, "SHADOW"); strings.Contains(got, shadowFirst) {
		t.Fatalf("the real /etc/shadow content is readable inside the sandbox")
	}
	if got := strings.TrimSpace(between(t, s, "ROOTSSH")); got != "" {
		t.Fatalf("/root/.ssh is visible inside the sandbox: %q", got)
	}
	// .
	if got := strings.TrimSpace(between(t, s, "OSREL")); got == "" {
		t.Fatal("ordinary /etc reads were penalized — /etc/os-release is empty")
	}
	if got := strings.TrimSpace(between(t, s, "LIBS")); got == "" {
		t.Fatal("the library world was penalized — /usr/lib lists empty")
	}
}
