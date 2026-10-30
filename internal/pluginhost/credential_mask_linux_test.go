//go:build linux

package pluginhost

import (
	"context"
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
		`printf "OSREL<%s>\n" "$(head -c 40 /usr/lib/os-release 2>/dev/null)"; ` +
		`printf "ETCREL<%s>\n" "$(head -c 40 /etc/os-release 2>/dev/null)"; ` +
		`printf "LIBS<%s>\n" "$(ls /usr/lib 2>/dev/null | head -1)"`
	for _, tc := range []struct {
		name    string
		view    []string
		widened bool
	}{
		{"the default view", nil, false},
		{"a view widened to /etc and /root", []string{"/etc", "/root"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv, telemetry, err := containArgv(context.Background(), []string{"/bin/sh", "-c", script}, nil, nativeFiles{view: tc.view})
			if err != nil {
				t.Fatalf("containment refused: %v", err)
			}
			// THE TELEMETRY MUST NOT OVERSTATE THE MASK. It names what is
			// masked, and it names what the child is shown, so an operator
			// who widens the view reads what the child then sees.
			if !strings.Contains(telemetry.Description, "ssh and shadow files masked") {
				t.Fatalf("telemetry does not record what is actually masked: %q", telemetry)
			}
			if !strings.Contains(telemetry.Description, "showing only /usr") || tc.widened && !strings.Contains(telemetry.Description, "/etc, /root") {
				t.Fatalf("telemetry does not name what the child is shown: %q", telemetry)
			}
			out, err := walled(t, exec.Command(argv[0], argv[1:]...), telemetry).CombinedOutput()
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
			if got := strings.TrimSpace(between(t, s, "OSREL")); got == "" {
				t.Fatal("the system's view was penalized — /usr/lib/os-release is empty")
			}
			if got := strings.TrimSpace(between(t, s, "LIBS")); got == "" {
				t.Fatal("the library world was penalized — /usr/lib lists empty")
			}
			// A widened view shows the rest of /etc: the masks hide the
			// stores in it, and nothing else of it.
			if got := strings.TrimSpace(between(t, s, "ETCREL")); tc.widened && got == "" {
				t.Fatal("a view widened to /etc does not show /etc/os-release, so the masks above were not tested against a shown /etc")
			}
		})
	}
}
