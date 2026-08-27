package hostcap

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// THE RUNTIME HALF'S ENFORCEMENT. The compile matrix proves the tree
// builds on five platforms; this test proves nobody ADDS a subprocess
// call without deciding where it can run. Every exec-shaped site must
// sit behind hostcap.Can() and be listed here WITH its justification
// — a new site fails this test until its author has made the topology
// decision consciously.
//
// This exists because exec.Command("bash", ...) compiled cleanly on
// all five platforms right up until the operator asked how a shell
// runs on iOS (2026-08-26).

var execSite = regexp.MustCompile(`exec\.Command|exec\.CommandContext|exec\.LookPath|syscall\.Exec\b|syscall\.ForkExec|os\.StartProcess`)

// allowlist: file (repo-relative) -> why its exec use is topology-safe.
var allowlist = map[string]string{
	"internal/tools/tool_shell.go":          "registered only when hostcap.Shell is available (tools.go registration gate)",
	"internal/supervisor/supervisor.go":     "spawnAndAwaitReady refuses first when hostcap.NativeChild is unavailable",
	"internal/pluginhost/sandbox_linux.go":  "reached only inside the NativeChild spawn path (desktop linux tag)",
	"internal/pluginhost/sandbox_darwin.go": "reached only inside the NativeChild spawn path (desktop darwin tag)",
	"internal/app/reexec_unix.go":           "desktop-only build tag AND the startLive hostcap.SelfReplace gate",
	"internal/app/reexec_windows.go":        "windows build tag AND the startLive hostcap.SelfReplace gate",
}

func TestEveryExecSiteIsTopologyGated(t *testing.T) {
	root := "../.."
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == "attic" || name == ".git" || name == "testdata" || name == "worktrees" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "internal/") && !strings.HasPrefix(rel, "cmd/") && !strings.HasPrefix(rel, "mobile/") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // prose about exec is not an exec site
			}
			if execSite.MatchString(line) {
				if _, ok := allowlist[rel]; !ok {
					violations = append(violations, rel+": "+trimmed)
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("exec-shaped call(s) outside the topology allowlist — subprocesses are a CAPABILITY "+
			"(iOS forbids exec; Android restricts it; Windows has no bash). Gate the site behind "+
			"hostcap.Can() and add it to the allowlist with its justification:\n  %s",
			strings.Join(violations, "\n  "))
	}
	for rel := range allowlist {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("allowlist names %s, which no longer exists — prune it", rel)
		}
	}
}
