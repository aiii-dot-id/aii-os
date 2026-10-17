//go:build !windows

package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A PACKAGE IS NAMED FOR THE VERSION ITS BINARIES REPORT (T45). The
// binaries carry the VERSION file compiled in; no build stamps one. So a
// VERSION in the environment that disagrees with the file would name a
// candidate, a .deb, a .dmg or a setup .exe for one version while every
// binary inside says another — and the updater would offer that release
// again on every check. Each script refuses it before it builds or
// writes anything, and accepts an agreeing value or none.
//
// The scripts run from a copy of their tree in a temp directory whose
// VERSION says 9.9.9. GO is a stub that announces itself and fails, so
// reaching it is the proof the version check let the script through.
func TestThePackagersRefuseAVersionTheFileDoesNotSay(t *testing.T) {
	const reached = "stub go reached"
	scripts := []struct {
		path, shell string
		args        []string
		passed      string // what the script says once past the check
	}{
		{"release.sh", "sh", []string{"attach"}, "no candidate at"},
		{"build-all.sh", "sh", nil, reached},
		{"archives.sh", "sh", []string{"linux"}, reached},
		{"deb/build-deb.sh", "sh", nil, reached},
		{"windows/build-setup.sh", "sh", nil, reached},
		{"macos/build-dmg.sh", "bash", nil, reached},
	}
	tree := func(t *testing.T) (root, goStub string) {
		t.Helper()
		root = t.TempDir()
		write := func(path, body string, mode os.FileMode) {
			t.Helper()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), mode); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range scripts {
			write(filepath.Join(root, "packaging", s.path), readPackaging(t, strings.Split(s.path, "/")...), 0o755)
		}
		// The step every installer script sources for the third-party
		// licences: beside them here as it is in the tree.
		write(filepath.Join(root, "packaging", "third-party-licenses.sh"), readPackaging(t, "third-party-licenses.sh"), 0o644)
		write(filepath.Join(root, "VERSION"), "9.9.9\n", 0o644)
		write(filepath.Join(root, "LICENSE"), "license\n", 0o644)
		goStub = filepath.Join(t.TempDir(), "go")
		write(goStub, "#!/bin/sh\necho '"+reached+"'\nexit 3\n", 0o755)
		return root, goStub
	}
	run := func(t *testing.T, root, goStub string, s struct {
		path, shell string
		args        []string
		passed      string
	}, env ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(s.shell, append([]string{filepath.Join("packaging", s.path)}, s.args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), append([]string{"GO=" + goStub, "AII_GO=", "VERSION=", "OUT=dist", "IDENTITY=qualification-no-signing-identity", "BIN_DIR="}, env...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, s := range scripts {
		t.Run(s.path, func(t *testing.T) {
			root, goStub := tree(t)
			out, err := run(t, root, goStub, s, "VERSION=9.9.8")
			if err == nil {
				t.Fatalf("VERSION=9.9.8 against a 9.9.9 file was accepted:\n%s", out)
			}
			if !strings.Contains(out, "VERSION=9.9.8 is set, but the VERSION file says 9.9.9") {
				t.Fatalf("refused for another reason, or without naming both values:\n%s", out)
			}
			if strings.Contains(out, s.passed) {
				t.Fatalf("the script went on past the version check:\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(root, "dist")); !os.IsNotExist(err) {
				t.Fatalf("the refusal came after the script wrote its output directory (%v)", err)
			}
			for _, agree := range []string{"VERSION=9.9.9", "VERSION="} {
				out, _ := run(t, root, goStub, s, agree)
				if !strings.Contains(out, s.passed) || strings.Contains(out, "the VERSION file says") {
					t.Fatalf("%s was refused, or stopped before the build:\n%s", agree, out)
				}
			}
		})
	}
}
