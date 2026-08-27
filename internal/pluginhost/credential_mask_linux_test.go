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

// R75: the credential masks close the 2026-08-26 probes — /etc/shadow
// opened, /root/.ssh listed — while penalizing nothing a legitimate
// plugin touches. BEHAVIORAL: the wrapped command actually runs, and
// the oracle is GROUND TRUTH the test reads as itself (root on dev8) —
// the sandbox must not be able to reproduce the real secret bytes,
// whether the mask yields emptiness or a permission error.
func TestCredentialStoresAreMaskedInsideTheSandbox(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap absent; the fail-closed test covers refusal")
	}
	// Ground truth: what an UNCONTAINED reader sees. If the test itself
	// cannot read these (unprivileged CI), there is nothing to leak and
	// the probe is not meaningful — skip rather than assert a vacuous pass.
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
	if !strings.Contains(telemetry, "credential stores masked") {
		t.Fatalf("telemetry does not record the masking: %q", telemetry)
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxed probe did not run: %v\n%s", err, out)
	}
	s := string(out)

	// The secret itself must not survive into the sandbox.
	if got := between(t, s, "SHADOW"); strings.Contains(got, shadowFirst) {
		t.Fatalf("the real /etc/shadow content is readable inside the sandbox")
	}
	if got := strings.TrimSpace(between(t, s, "ROOTSSH")); got != "" {
		t.Fatalf("/root/.ssh is visible inside the sandbox: %q", got)
	}
	// And the legitimate world is untouched — no penalty (R75).
	if got := strings.TrimSpace(between(t, s, "OSREL")); got == "" {
		t.Fatal("ordinary /etc reads were penalized — /etc/os-release is empty")
	}
	if got := strings.TrimSpace(between(t, s, "LIBS")); got == "" {
		t.Fatal("the library world was penalized — /usr/lib lists empty")
	}
}
