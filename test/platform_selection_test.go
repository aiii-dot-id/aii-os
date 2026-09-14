package test

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
// .
// .
// .
// .
// .
// .
// .

import (
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
const repoFromTest = ".."

// .
var buildRows = []struct{ goos, goarch string }{
	{"linux", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
	{"android", "arm64"},
	{"ios", "arm64"},
}

// .
// .
// .
func selectedFiles(t *testing.T, dir, goos, goarch string) []string {
	t.Helper()
	c := build.Default
	c.GOOS, c.GOARCH, c.CgoEnabled = goos, goarch, false
	p, err := c.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("%s on %s/%s: %v", dir, goos, goarch, err)
	}
	return p.GoFiles
}

func withPrefix(files []string, prefix string) []string {
	var out []string
	for _, f := range files {
		if strings.HasPrefix(f, prefix) {
			out = append(out, f)
		}
	}
	return out
}

// .
// .
// .
// .
// .
// .
// .
func TestServiceInstallerSelectedPerPlatform(t *testing.T) {
	want := map[string]string{
		"linux":   "service_linux.go",
		"darwin":  "service_darwin.go",
		"windows": "service_windows.go",
		"android": "service_other.go",
		"ios":     "service_other.go",
	}
	dir := filepath.Join(repoFromTest, "internal", "install")
	for _, r := range buildRows {
		got := withPrefix(selectedFiles(t, dir, r.goos, r.goarch), "service_")
		if len(got) != 1 {
			t.Errorf("%s: %d service implementations selected (%v) — exactly one must define Register/Unregister/StartCommand",
				r.goos, len(got), got)
			continue
		}
		if got[0] != want[r.goos] {
			t.Errorf("%s selects %s, want %s — %s cannot run what %s does",
				r.goos, got[0], want[r.goos], r.goos, got[0])
		}
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestFilenameSuffixNeverLeaksAcrossTheAliasRule(t *testing.T) {
	alsoSelectedOn := map[string]string{"linux": "android", "darwin": "ios"}

	err := filepath.WalkDir(repoFromTest, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n != "." && n != ".." && (strings.HasPrefix(n, ".") || n == "vendor" || n == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		for host, mobile := range alsoSelectedOn {
			if !strings.HasSuffix(name, "_"+host+".go") {
				continue
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if hasBuildConstraint(src) {
				continue
			}
			t.Errorf("%s is selected on GOOS=%s as well as %s — its name is its only constraint, "+
				"and %s satisfies the %s tag. Add an explicit //go:build line (`%s && !%s`) "+
				"so the row it lands on is a decision rather than an alias.",
				filepath.Clean(path), mobile, host, mobile, host, host, mobile)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// .
// .
// .
func hasBuildConstraint(src []byte) bool {
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if strings.HasPrefix(line, "//go:build ") {
			return true
		}
	}
	return false
}
