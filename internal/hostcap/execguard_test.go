package hostcap

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var execSite = regexp.MustCompile(`exec\.Command|exec\.CommandContext|exec\.LookPath|syscall\.Exec\b|syscall\.ForkExec|os\.StartProcess`)

// allowlist: file (repo-relative) -> why its exec use is topology-safe.
var allowlist = map[string]string{
	"internal/app/selection_capacity.go":             "selectionQuery checks hostcap.Subprocess before a bounded shell-free read from fixed OS/driver paths or the host's own isolated Vulkan probe; only opted-in resource-aware package selection calls it, never mobile subprocesses",
	"internal/browsertest/browsertest.go":            "test scaffolding: tests alone import it (TestTheShippedRuntimeCarriesNoTestScaffolding); its one exec-shaped call is a LookPath for a browser's executable on PATH — a search, not a spawn",
	"internal/tools/tool_shell.go":                   "registered only when hostcap.Shell is available (tools.go registration gate)",
	"internal/tools/shell_windows.go":                "windows build tag AND the same hostcap.Shell registration gate as tool_shell.go; the one exec-shaped call here is a LookPath choosing PowerShell 7 over the 5.1 that always exists — a PATH search, not a spawn",
	"internal/supervisor/supervisor.go":              "spawnAndAwaitReady refuses first when hostcap.NativeChild is unavailable",
	"internal/pluginhost/sandbox_linux.go":           "reached only inside the NativeChild spawn path (desktop linux tag)",
	"internal/pluginhost/contain_preflight_linux.go": "linux build tag AND called only from containArgv in sandbox_linux.go, so it is inside the same NativeChild spawn path; the one exec is the containment mechanism itself being asked whether it can build a namespace on this host, which is a question only a spawn can answer",
	"internal/pluginhost/sandbox_darwin.go":          "reached only inside the NativeChild spawn path (desktop darwin tag)",
	"internal/app/reexec_unix.go":                    "desktop-only build tag AND the startLive hostcap.SelfReplace gate",
	"internal/app/reexec_windows.go":                 "windows build tag AND the startLive hostcap.SelfReplace gate",
	"internal/install/run.go":                        "desktop build tag ((darwin && !ios) || (linux && !android)) AND every exec site behind the hostcap.Subprocess gate in run(); systemctl and launchctl are only reached after that gate, and a host without subprocesses still gets its slot and the manual start command",
	"internal/oauth/note_darwin.go":                  "darwin build tag AND keychainLookup refuses first when hostcap.Subprocess is unavailable; security is the platform Keychain client",
	"internal/updates/bundle_darwin.go":              "darwin build tag AND the hostcap.Subprocess gate at the top of applyBundleUpdate; ditto, codesign and spctl are only reached after it. A macOS update replaces a SEALED .app, and the seal can only be checked by asking the platform's own tools — a host that cannot exec cannot verify a bundle and so must not install one",
	"cmd/aii-release/licenses.go":                    "a release tool run where a release is built: its one exec site, goTool, refuses first when hostcap.Subprocess is unavailable; it asks the go tool for the module graph of the shipped commands and for GOROOT, to write the third-party licences",
	"cmd/aii-release/packages.go":                    "a release tool run where a release is built: its one exec site, packageContents, refuses first when hostcap.Subprocess is unavailable; it asks dpkg-deb, the tool that built a system package, for the package's architecture and file tree, to check that it installs every program the release ships",
	"cmd/aii-app/main.go":                            "desktop build tag (darwin && !ios, linux && !android) AND the hostcap.Subprocess gate at the top of run(); the .app entry point and the Linux menu entry ask aii register to have the service manager start the identity, and open a browser, and on a port another program holds run aii dashboard-port (the config's one writer, contract) — all subprocesses",
	"cmd/aii-app/main_darwin.go":                     "darwin && !ios build tag AND reached from run() after its hostcap.Subprocess gate, or from alert() reporting run()'s failure on a desktop Mac, where subprocesses always exist; open shows the dashboard, osascript the failure and the port question; with --uninstall (L1c) it runs the aii beside it (uninstall -plan, then -yes), asks and tells with osascript, and open -R shows the bundle",
	"cmd/aii-app/main_linux.go":                      "linux && !android build tag AND reached from run() after its hostcap.Subprocess gate, or from alert() reporting run()'s failure on a Linux desktop; xdg-open shows the dashboard, notify-send the failure, zenity or kdialog (found by LookPath) the port question",
	"internal/install/service_windows.go":            "windows build tag AND the hostcap.Subprocess gate at the top of Register; the autostart entry itself is a registry write, not an exec",
	"internal/install/machine_windows.go":            "windows build tag AND the hostcap.Subprocess gate at the top of RegisterMachine and StartRegisteredMachine (StartMachine is the launcher's, behind its own gate); schtasks registers, runs and removes the machine service's on-demand task, and a direct start is the registration's fallback when the task cannot be registered",
	"cmd/aii-app/token_words.go":                     "desktop and windows build tags AND reached only from the launcher's run() after its hostcap.Subprocess gate, when it opens the dashboard; it runs aii dashboard-link beside it, which prints the address to open, signed in once where the dashboard asks for its access token",
	"cmd/aii-app/main_windows.go":                    "windows build tag AND the hostcap.Subprocess gate at the top of run(); the launcher starts the identity and opens a browser, both subprocesses",
	"cmd/aii-setup/main_windows.go":                  "windows build tag AND the hostcap.Subprocess gate at the top of runInstall(); the installer stops a running copy, asks the shell for a shortcut, and starts the launcher",
	"internal/app/uninstall_offer_darwin.go":         "darwin && !ios build tag AND openUninstall refuses first when hostcap.Subprocess is unavailable; /usr/bin/open asks LaunchServices to open the app in its uninstall mode, outside this identity's launchd job, so the app outlives the identity it stops (L1c, C6)",
	"internal/uninstall/platform_windows.go":         "windows build tag AND shortcutTarget refuses first when hostcap.Subprocess is unavailable; a .lnk is a COM format and this program pure Go, so the shell that wrote the installer's shortcut (WScript.Shell, through PowerShell) reads its target, and uninstall removes it only when it points into the install (L1c, C8)",
	"internal/uninstall/platform_darwin.go":          "darwin && !ios build tag AND processes() refuses first when hostcap.Subprocess is unavailable; /bin/ps, setuid on macOS, is the one way an account sees that another account runs a bundle they share (kern.procargs2 refuses another account's process), so uninstall keeps that bundle's updater files",
}

// guard:tree
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
