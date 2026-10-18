package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestSearchTextWithASlashIsSearched(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "vega")
	granted := filepath.Join(base, "grants", "proj")
	outside := filepath.Join(base, "outside")
	for p, body := range map[string]string{
		filepath.Join(home, "notes.md"):           "home-hit a/b\n",
		filepath.Join(granted, "src", "route.go"): "granted-hit a/b /etc/passwd\n",
		filepath.Join(outside, "secret.txt"):      "outside-hit a/b\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRegistry(home, firewall.DefaultPolicy(), Timeouts{})
	r.SetExtraRoots([]string{granted})
	call := func(name string, args map[string]any) Result {
		t.Helper()
		res, err := r.ExecuteModel(context.Background(), name, args)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Output, "outside-hit") {
			t.Fatalf("%s %v read outside the sandbox: %q", name, args, res.Output)
		}
		return res
	}
	searched := func(name string, args map[string]any, hit string) {
		t.Helper()
		if res := call(name, args); res.Error != "" || hit != "" && !strings.Contains(res.Output, hit) {
			t.Errorf("%s %v: search text was not searched: %+v", name, args, res)
		}
	}
	refused := func(name string, args map[string]any) {
		t.Helper()
		if res := call(name, args); !strings.HasPrefix(res.Error, "access denied") {
			t.Errorf("%s %v: a path outside the sandbox was not refused: %+v", name, args, res)
		}
	}

	searched("grep", map[string]any{"pattern": "a/b"}, "home-hit")
	searched("grep", map[string]any{"pattern": "a/b", "path": granted}, "granted-hit")
	searched("grep", map[string]any{"pattern": "/etc/passwd", "path": granted}, "granted-hit")
	refused("grep", map[string]any{"pattern": "a/b", "path": outside})

	if shellDialect != "bash" {
		return
	}
	q := func(p string) string { return "'" + p + "'" }
	shell := func(cmd string) map[string]any { return map[string]any{"command": cmd} }
	searched("shell", shell(`grep -rn 'a/b' .`), "home-hit")
	searched("shell", shell(`grep -rn "a/b" `+q(granted)), "granted-hit")
	searched("shell", shell(`cd `+q(granted)+` && grep -rn 'a/b' .`), "granted-hit")
	// Admission only: rg need not be installed where this runs.
	searched("shell", shell(`rg -n 'a/b' `+q(granted)), "")
	refused("shell", shell(`grep -rn 'a/b' `+q(outside)))
}

func TestASlashSearchRefusalNamesTheGrepTool(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(home, firewall.DefaultPolicy(), Timeouts{})
	if _, ok := r.Get("shell"); !ok {
		t.Skip("no shell on this host")
	}
	shell := func(cmd string) string {
		t.Helper()
		res, err := r.ExecuteModel(context.Background(), "shell", map[string]any{"command": cmd})
		if err != nil {
			t.Fatal(err)
		}
		return res.Error
	}
	const wall = " (best-effort check; run under a container for a hard boundary)"
	rows := map[string]string{
		`grep -rn '// TODO' .`:  "// TODO",
		`grep -rn '</div>' .`:   "/div",
		`rg -F '/etc/passwd' .`: "/etc/passwd",
	}

	if invocationExempt("/usr/bin/egrep") {
		rows[`/usr/bin/egrep -r '/api' .`] = "/api"
	}
	for cmd, refused := range rows {
		want := "access denied: " + refused + " is outside the sandbox " + home + wall +
			"\nif that is search text and not a path, the grep tool takes any pattern: grep pattern=\"" + refused + "\" path=\"…\""
		if got := shell(cmd); got != want {
			t.Errorf("%s:\n got %q\nwant %q", cmd, got, want)
		}
	}
	// The last: a search program outside the sandbox is refused as the
	// program it is, never offered back as search text.
	for _, cmd := range []string{`cat /etc/passwd`, `grep -rn needle ledger.jsonl`, `/opt/tools/rg -F '/api' .`} {
		if got := shell(cmd); !strings.HasPrefix(got, "access denied: ") || !strings.HasSuffix(got, wall) {
			t.Errorf("%s: no grep line belongs on this refusal: %q", cmd, got)
		}
	}
}
