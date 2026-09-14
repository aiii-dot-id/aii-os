package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readPackaging(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{repoRoot(t), "packaging"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// .
// .
// .
// .
func TestReleasePrepareRefusesAnUnsafeOutputPath(t *testing.T) {
	root := repoRoot(t)
	for _, bad := range []string{"/", "/tmp/anything", "..", "../x", "a/../b", "a/.."} {
		cmd := exec.Command("sh", "packaging/release.sh", "prepare")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "OUT="+bad)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("OUT=%q was accepted: %s", bad, out)
		}
		if !strings.Contains(string(out), "must be a relative path under the repo") {
			t.Fatalf("OUT=%q refused for the wrong reason (the guard did not fire): %s", bad, out)
		}
	}
}

// .
// .
// .
func TestReleaseBindingIsNotPipedAwayAndCoversEveryExecutable(t *testing.T) {
	s := readPackaging(t, "release.sh")
	// .
	// .
	// .
	if regexp.MustCompile(`assert-source-bound\.sh[^\n]*[^|]\|[^|]`).MatchString(s) {
		t.Fatal("assert-source-bound.sh is piped again — a pipeline's status is sed's, and sed always succeeds")
	}
	if !strings.Contains(s, `2>&1) || die "$out"`) {
		t.Fatal("bind() no longer dies on the assertion's failure")
	}
	for _, site := range []string{
		`bind "$work/x/usr/bin/aii"`, `bind "$work/x/aii"`, `bind "$work/x/aii.exe"`, `bind "$exe"`,
		`bind "$work/x/AII OS.app/Contents/MacOS/aii"`, `bind "$work/x/AII OS.app/Contents/MacOS/AII OS"`,
	} {
		if !strings.Contains(s, site) {
			t.Fatalf("an executable escaped source binding: %s", site)
		}
	}
}

// .
// .
func TestReleaseInventoryRequiresTheInstallers(t *testing.T) {
	s := readPackaging(t, "release.sh")
	for _, want := range []string{`.deb"`, `.exe"`, `"AII-OS-${VERSION}.dmg"`, `MISSING installer`} {
		if !strings.Contains(s, want) {
			t.Fatalf("inventory does not require %s", want)
		}
	}
}

// .
// .
// .
// .
// .
func TestBuildAllForwardsIdentityAndFailsClosed(t *testing.T) {
	s := readPackaging(t, "build-all.sh")
	if strings.Contains(s, "SIGN_ID") {
		t.Fatal("SIGN_ID is back — a variable nothing reads")
	}
	if !strings.Contains(s, "IDENTITY=${IDENTITY:-} OUT=$rdir/out") {
		t.Fatal("IDENTITY is not forwarded to the remote build-dmg.sh")
	}
	if strings.Contains(s, "|| true") {
		t.Fatal("a failure is swallowed with || true")
	}
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("dpkg-deb not on this host; the live fail-closed probe needs the Debian packager")
	}
	out := t.TempDir()
	cmd := exec.Command("sh", "packaging/build-all.sh")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "BUILD_MACOS=1", "MAC_BUILDER=nobody@127.0.0.1", "OUT="+out, "GO="+goToolPath())
	got, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("BUILD_MACOS=1 with no reachable Mac exited 0 — a candidate with no macOS package would be announced complete:\n%s", got)
	}
	if !strings.Contains(string(got), "staged (not packaged)") {
		t.Fatalf("failed for the wrong reason:\n%s", got)
	}
}

// .
// .
func TestReleaseAttachCannotPipeAwayTheVerdict(t *testing.T) {
	s := readPackaging(t, "release.sh")
	if regexp.MustCompile(`"\$TOOL" verify[^\n]*[^|]\|[^|]`).MatchString(s) {
		t.Fatal("aii-release verify is piped again — its refusal would be indented and ignored")
	}
	if !strings.Contains(s, `|| die "verification REFUSED for $asset: $out"`) {
		t.Fatal("attach no longer dies when the verifier refuses")
	}
}

// .
// .
// .
func TestReleaseAssetCasesAllHaveADefaultArm(t *testing.T) {
	s := readPackaging(t, "release.sh")
	// .
	// .
	assigning := 0
	for _, m := range regexp.MustCompile(`(?s)case "\$asset" in(.*?)\n\s*esac`).FindAllStringSubmatch(s, -1) {
		if !regexp.MustCompile(`\b(p|a|goos|goarch)=`).MatchString(m[1]) {
			continue
		}
		assigning++
		if !strings.Contains(m[1], `*) die "no `) {
			t.Fatalf("an asset case that assigns platform/arch has no default arm — p/a would leak from the previous iteration:\n%s", m[1])
		}
	}
	if assigning < 2 {
		t.Fatalf("expected the build loop and the payload loop to assign from the asset name, found %d", assigning)
	}
	if !strings.Contains(s, `|| die "copy $(basename "$a") into the candidate"`) {
		t.Fatal("a failed installer copy no longer fails prepare")
	}
}

// .
// .
// .
// .
// .
func TestReleaseWorkflowCanActuallyRun(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "refs/tags/v") {
		t.Fatal("a tag gate is back — with workflow_dispatch and no tags, every run fails before building")
	}
	if strings.Contains(s, "files:") {
		t.Fatal("upload-artifact is given `files:` — it has no such input, and the missing required `path:` aborts the step")
	}
	uploads := strings.Count(s, "uses: actions/upload-artifact@v4")
	names := strings.Count(s, "name: aii-os-${{ matrix.")
	paths := strings.Count(s, "          path: |")
	if uploads == 0 || names != uploads || paths != uploads {
		t.Fatalf("%d upload steps, %d unique names, %d path inputs — a leg without its own name collides on the second run", uploads, names, paths)
	}
	if strings.Contains(s, "continue-on-error") {
		t.Fatal("a leg may pass while producing nothing again")
	}
	if strings.Contains(s, "platform: ios") {
		t.Fatal("the ios placeholder leg is back")
	}
}
