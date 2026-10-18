//go:build windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// filepath.IsAbs answers "does this name a volume", which is not the
// question containment asks. These are the spellings it gets wrong.
func TestIsRootedCoversDriveRootedPaths(t *testing.T) {
	for _, c := range []struct {
		path string
		want bool
	}{
		{`\aii-escape-probe\secret.txt`, true}, // IsAbs says false
		{"/aii-escape-probe/secret.txt", true}, // IsAbs says false
		{`C:\Windows`, true},

		{`C:foo`, true},
		{`C:foo\bar.txt`, true},
		{`d:x`, true},
		{`\\server\share\x`, true},
		{"relative\\path", false},
		{"..\\escape", false},
		{"", false},
	} {
		if got := isRooted(c.path); got != c.want {
			t.Errorf("isRooted(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// The measured escape, kept as a test. Before the seam, the shell tool
// returned this file's contents from outside the sandbox because the
// containment check had joined a drive-rooted path onto the sandbox
// while PowerShell read it from the drive root.
func TestDriveRootedPathCannotEscapeTheSandbox(t *testing.T) {
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		t.Skip("no SystemDrive")
	}
	outside := filepath.Join(drive+string(filepath.Separator), "aii-escape-probe")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Skip("cannot create probe dir:", err)
	}
	defer os.RemoveAll(outside)
	const marker = "ESCAPED-SANDBOX-MARKER"
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte(marker), 0o644); err != nil {
		t.Skip("cannot write probe file:", err)
	}

	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	for _, c := range []struct{ tool, key, val string }{
		{"read", "file_path", `\aii-escape-probe\secret.txt`},
		{"read", "file_path", "/aii-escape-probe/secret.txt"},
		{"shell", "command", `type \aii-escape-probe\secret.txt`},
		{"shell", "command", "type /aii-escape-probe/secret.txt"},
	} {
		res, _ := r.Execute(context.Background(), c.tool, map[string]any{c.key: c.val})
		if strings.Contains(res.Output, marker) {
			t.Errorf("%s escaped the sandbox via %q: out=%q", c.tool, c.val, res.Output)
		}
	}
}

// The Windows form of "system-binary invocation is still allowed".
// PowerShell's own commands carry no path, so the containment scan never
// sees one; the unix form of this law names /bin and /usr, which on
// Windows are drive-rooted paths to nothing.
func TestWindowsSystemCommandsNeedNoPath(t *testing.T) {
	r, _ := relRegistry(t)
	for _, cmd := range []string{
		"Get-ChildItem",
		"echo hello",
		"Get-Location",
		"Write-Output test",
		"$PSVersionTable.PSVersion",
	} {
		if why := r.shellRefusal(cmd); why != "" {
			t.Errorf("a PowerShell command was refused: %q -> %s", cmd, why)
		}
	}
}
