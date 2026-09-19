package hostcap

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

var execSite = regexp.MustCompile(`exec\.Command|exec\.CommandContext|exec\.LookPath|syscall\.Exec\b|syscall\.ForkExec|os\.StartProcess`)

// .
var allowlist = map[string]string{
	"internal/tools/tool_shell.go":                   "registered only when hostcap.Shell is available (tools.go registration gate)",
	"internal/tools/shell_windows.go":                "windows build tag AND the same hostcap.Shell registration gate as tool_shell.go; the one exec-shaped call here is a LookPath choosing PowerShell 7 over the 5.1 that always exists — a PATH search, not a spawn",
	"internal/supervisor/supervisor.go":              "spawnAndAwaitReady refuses first when hostcap.NativeChild is unavailable",
	"internal/pluginhost/sandbox_linux.go":           "reached only inside the NativeChild spawn path (desktop linux tag)",
	"internal/pluginhost/contain_preflight_linux.go": "linux build tag AND called only from containArgv in sandbox_linux.go, so it is inside the same NativeChild spawn path; the one exec is the containment mechanism itself being asked whether it can build a namespace on this host, which is a question only a spawn can answer",
	"internal/pluginhost/sandbox_darwin.go":          "reached only inside the NativeChild spawn path (desktop darwin tag)",
	"internal/app/reexec_unix.go":                    "desktop-only build tag AND the startLive hostcap.SelfReplace gate",
	"internal/app/reexec_windows.go":                 "windows build tag AND the startLive hostcap.SelfReplace gate",
	"internal/install/service_linux.go":              "linux build tag AND every exec site behind the hostcap.Subprocess gate in run(); a host without subprocesses still gets its slot and the manual start command",
	"internal/install/service_darwin.go":             "darwin build tag AND every exec site behind the hostcap.Subprocess gate in run(); launchctl is only reached after that gate",
	"internal/oauth/note_darwin.go":                  "darwin build tag AND keychainLookup refuses first when hostcap.Subprocess is unavailable; security is the platform Keychain client",
	"internal/updates/bundle_darwin.go":              "darwin build tag AND the hostcap.Subprocess gate at the top of applyBundleUpdate; ditto, codesign and spctl are only reached after it. A macOS update replaces a SEALED .app, and the seal can only be checked by asking the platform's own tools — a host that cannot exec cannot verify a bundle and so must not install one",
	"cmd/aii-app/main.go":                            "darwin build tag AND the hostcap.Subprocess gate at the top of run(); the .app entry point starts the identity and opens a browser, both subprocesses",
	"internal/install/service_windows.go":            "windows build tag AND the hostcap.Subprocess gate at the top of Register; the autostart entry itself is a registry write, not an exec",
	"cmd/aii-app/main_windows.go":                    "windows build tag AND the hostcap.Subprocess gate at the top of run(); the launcher starts the identity and opens a browser, both subprocesses",
	"cmd/aii-setup/main_windows.go":                  "windows build tag AND the hostcap.Subprocess gate at the top of runInstall(); the installer stops a running copy, asks the shell for a shortcut, and starts the launcher",
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
				continue
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
