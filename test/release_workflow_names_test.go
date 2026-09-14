package test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// .
// .
// .
// .
// .
// .
// .
func TestReleaseWorkflowNamesArchivesAsTheUpdaterExpects(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wf := string(b)

	// .
	// .
	// .
	for i, line := range strings.Split(wf, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "x86_64") {
			t.Fatalf("release.yml line %d still emits x86_64; the updater's vocabulary is GOARCH (amd64): %s", i+1, strings.TrimSpace(line))
		}
	}
	// .
	// .
	for _, ext := range []string{"tar.gz", "zip"} {
		want := "aii-os_${{ steps.version.outputs.version }}_${{ steps.platform.outputs.name }}_${{ matrix.goarch }}." + ext
		if !strings.Contains(wf, `archive="`+want+`"`) {
			t.Errorf("the %s archive step must name its file %q (from matrix.goarch), workflow says otherwise", ext, want)
		}
	}

	// .
	// .
	rows := regexp.MustCompile(`\{ goos: (\w+),\s+goarch: (\w+), archive: ([\w.]+) \}`).FindAllStringSubmatch(wf, -1)
	if len(rows) != 3 {
		t.Fatalf("expected the three desktop matrix rows, found %d", len(rows))
	}
	platform := map[string]string{"linux": "linux", "windows": "windows", "darwin": "macos"}
	supported := map[string]bool{}
	for _, tg := range updates.SupportedTargets() {
		supported[tg.Platform+"/"+tg.Arch] = true
	}
	wantNames := map[string]bool{
		"aii-os_1.2.3_linux_amd64.tar.gz": false,
		"aii-os_1.2.3_linux_arm64.tar.gz": false,
		"aii-os_1.2.3_windows_amd64.zip":  false,
	}
	for _, r := range rows {
		goos, goarch, archive := r[1], r[2], r[3]
		name := "aii-os_1.2.3_" + platform[goos] + "_" + goarch + "." + archive
		if got := updates.AssetName("1.2.3", platform[goos], goarch); got != name {
			t.Errorf("row %s/%s: the workflow would produce %q, the updater looks for %q", goos, goarch, name, got)
		}
		if !supported[platform[goos]+"/"+goarch] {
			t.Errorf("row %s/%s is not an updater-supported target", goos, goarch)
		}
		if _, ok := wantNames[name]; !ok {
			t.Errorf("unexpected archive name %q", name)
		}
		wantNames[name] = true
	}
	for n, seen := range wantNames {
		if !seen {
			t.Errorf("no matrix row produces %q", n)
		}
	}
}
