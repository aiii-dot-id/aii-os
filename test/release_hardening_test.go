package test

import (
	"fmt"
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

// THE VERSION IS COMPILED IN, AND NOTHING STAMPS ONE (T45). internal/version
// embeds the VERSION file, and the binary reports that and nothing else.
// The build sites used to inject the same value again through
// -ldflags -X: a second path to one fact, and once the variable it named
// is gone, a stamp the linker drops without a word. So no build site in
// packaging/ or the workflows stamps a version. (The mobile bind's
// BuildCommit stamp is a commit, not a version, and is not this.)
func TestNoBuildSiteStampsAVersion(t *testing.T) {
	root := repoRoot(t)
	stamp := regexp.MustCompile(`-X\s+\S*\.Version=`)
	var sites []string
	for _, dir := range []string{"packaging", filepath.Join(".github", "workflows")} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			switch filepath.Ext(path) {
			case ".sh", ".yml", ".md", ".ps1":
			default:
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if stamp.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					sites = append(sites, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), i+1))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(sites) > 0 {
		t.Fatalf("a build site stamps a version with -ldflags -X; the version is compiled in (internal/version), and a stamp is a second path to it: %v", sites)
	}
}

// prepare removes OUT and CAND wholesale. An override that escapes the
// repo must be refused BEFORE anything runs — OUT=$HOME used to remove
// $HOME. Every value here is refused by the guard, so nothing after it
// executes; the test never lets an unguarded script see these paths.
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

// The binding assertion's exit status is the verdict; a `| sed` after it
// discarded that status, so the two checks that ran could not fail a
// release. And it must reach every executable the candidate ships.
func TestReleaseBindingIsNotPipedAwayAndCoversEveryExecutable(t *testing.T) {
	s := readPackaging(t, "release.sh")
	// A lone pipe after the assertion. bind()'s own line ends in
	// `|| die`, which is two pipes and the opposite of the defect; the
	// first version of this regex flagged it and failed the clean tree.
	if regexp.MustCompile(`assert-source-bound\.sh[^\n]*[^|]\|[^|]`).MatchString(s) {
		t.Fatal("assert-source-bound.sh is piped again — a pipeline's status is sed's, and sed always succeeds")
	}
	if !strings.Contains(s, `2>&1) || die "$out"`) {
		t.Fatal("bind() no longer dies on the assertion's failure")
	}
	for _, site := range []string{
		`bind "$work/x/usr/bin/aii"`, `bind "$work/x/aii"`, `bind "$work/x/aii.exe"`, `bind "$exe"`,
		`bind "$work/x/usr/bin/aii-vulkan-probe"`, `bind "$work/x/aii-vulkan-probe"`,
		`bind "$work/x/AII OS.app/Contents/MacOS/aii"`, `bind "$work/x/AII OS.app/Contents/MacOS/AII OS"`,
		// The launcher, wherever a release puts one: it was built and
		// shipped unbound in the package and the installer, and not
		// shipped at all in the archives (0.1.16).
		`bind "$work/x/usr/bin/aii-app"`, `bind "$work/x/aii-app"`, `bind "$work/x/AII OS.exe"`,
	} {
		if !strings.Contains(s, site) {
			t.Fatalf("an executable escaped source binding: %s", site)
		}
	}
}

func TestReleaseInventoryRequiresTheInstallers(t *testing.T) {
	s := readPackaging(t, "release.sh")
	for _, want := range []string{`"$OUT"/*.deb`, `MISSING $asset`, `.exe"`, `"AII-OS-${VERSION}.dmg"`, `MISSING installer`} {
		if !strings.Contains(s, want) {
			t.Fatalf("inventory does not require %s", want)
		}
	}
	if strings.Contains(s, `installers() { printf '%s\n' "aii-os_`) {
		t.Fatal("the Ubuntu package is listed as an installer outside the signed set again")
	}
}

// build-all.sh: an operator's IDENTITY reaches the remote build (it was
// read into a dead variable); scp failures are not swallowed; and when
// BUILD_MACOS=1 asked for a package, "staged" is a failure. The last is
// proven live: an unreachable MAC_BUILDER must make the script exit
// non-zero after saying so.
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
	// Exercise the off-Mac handoff on every host. Running this fixture on
	// a Mac must not sign/notarize a package and mistake that success for
	// a failed remote handoff. Only the host and connection seams are fake;
	// the real packagers and the script's exit status remain the probe.
	bin := t.TempDir()
	for name, script := range map[string]string{
		"uname": "#!/bin/sh\nprintf 'Linux\\n'\n",
		"ssh":   "#!/bin/sh\nexit 255\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	tree := t.TempDir()
	if cp, err := exec.Command("sh", "-c", `tar -C "$1" --exclude=./.git -cf - . | tar -C "$2" -xf -`, "sh", repoRoot(t), tree).CombinedOutput(); err != nil {
		t.Fatalf("the tree could not be copied: %v\n%s", err, cp)
	}
	cmd := exec.Command("sh", "packaging/build-all.sh")
	cmd.Dir = tree
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "BUILD_MACOS=1", "MAC_BUILDER=nobody@127.0.0.1", "OUT="+out, "GO="+goToolPath())
	got, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("BUILD_MACOS=1 with no reachable Mac exited 0 — a candidate with no macOS package would be announced complete:\n%s", got)
	}
	if !strings.Contains(string(got), "staged (not packaged)") {
		t.Fatalf("failed for the wrong reason:\n%s", got)
	}
}

// attach's admission decision is the verifier's exit status. Piping the
// verifier through sed announced "SEALED" over a refused signature. The
// lint stays; the behaviour is run by TestAttachSealsOnlyWhatTheVerifierAdmits.
func TestReleaseAttachCannotPipeAwayTheVerdict(t *testing.T) {
	s := readPackaging(t, "release.sh")
	if regexp.MustCompile(`"\$TOOL" verify[^\n]*[^|]\|[^|]`).MatchString(s) {
		t.Fatal("aii-release verify is piped again — its refusal would be indented and ignored")
	}
}

// A failed installer copy fails prepare.
func TestReleasePrepareDiesOnAFailedInstallerCopy(t *testing.T) {
	s := readPackaging(t, "release.sh")
	if !strings.Contains(s, `|| die "copy $(basename "$a") into the candidate"`) {
		t.Fatal("a failed installer copy no longer fails prepare")
	}
}

// release.yml is workflow_dispatch-only and publishes nothing, but it
// must be able to RUN: every manual invocation used to die at a tag gate
// left over from the tag-triggered design, its uploads passed `files:`
// to an action that only knows `path:`, three legs shared the default
// artifact name, and an iOS leg was green while producing nothing.
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
