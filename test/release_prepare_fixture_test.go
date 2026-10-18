//go:build !windows

package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A RELEASE BUILDS EACH TARGET FOR THE GOOS AND ARCH ITS ROW NAMES, AND
// REFUSES A ROW IT CANNOT BUILD (T45). The rows are the release contract's
// (aii-release assets); no script parses a name for a target or holds the
// table again. The real scripts run from a copy of their tree in a temp
// directory with their one outside party stood in for: a stub `go` that
// builds the stub aii-release (`go build -o`), writes the licence file
// (`go run`), and otherwise records the GOOS/GOARCH it was asked for and
// writes a placeholder binary; and a stub aii-release that answers the
// questions the scripts ask.
//
// The archive loop was release.sh's own until the archives were given one
// builder (packaging/archives.sh, run by build-all.sh), and this test
// drove it there: two real rows built for their own platforms, then a
// refusal naming a third before any installer, binding or signing
// payload. Both halves are held where the loop now is.

type archiveFixture struct {
	root, side, goLog string
}

// newArchiveFixture copies the release scripts into a temp tree whose
// stub aii-release prints rows for `assets`.
func newArchiveFixture(t *testing.T, rows ...string) archiveFixture {
	t.Helper()
	// The scripts name their tree as the shell resolves it (pwd), and the
	// tests look for that name in what they print: on macOS a temp
	// directory sits behind a symlink, so the fixture resolves its own.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := archiveFixture{root: root, side: t.TempDir()}
	f.goLog = filepath.Join(f.side, "go.log")
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, script := range []string{"release.sh", "build-all.sh", "archives.sh", "third-party-licenses.sh"} {
		write(filepath.Join(f.root, "packaging", script), readPackaging(t, script), 0o755)
	}
	write(filepath.Join(f.root, "VERSION"), "9.9.9\n", 0o644)
	write(filepath.Join(f.root, "LICENSE"), "license\n", 0o644)
	write(filepath.Join(f.root, "go.mod"), "module example.invalid/fixture\n", 0o644)
	// What the Linux archive lays out beside its programs.
	write(filepath.Join(f.root, "packaging", "deb", "aii-os.desktop"), "[Desktop Entry]\n", 0o644)
	write(filepath.Join(f.root, "packaging", "deb", "aii.1"), ".TH AII 1\n", 0o644)
	write(filepath.Join(f.root, "packaging", "deb", "icons", "hicolor", "16x16", "apps", "aii-os.png"), "png", 0o644)
	quoted := make([]string, len(rows))
	for i, r := range rows {
		quoted[i] = `"` + r + `"`
	}
	write(filepath.Join(f.side, "aii-release"), `#!/bin/sh
case "$1" in
assets) printf '%s\n' `+strings.Join(quoted, " ")+` ;;
programs) printf '%s\t%s\n' ./cmd/aii aii ./cmd/aii-app aii-app ./cmd/aii-vulkan-probe aii-vulkan-probe ;;
archive-files)
  case "$3" in
  windows) printf '%s\n' aii.exe "AII OS.exe" LICENSE THIRD_PARTY_LICENSES ;;
  *) printf '%s\n' aii aii-app aii-vulkan-probe LICENSE THIRD_PARTY_LICENSES share/applications/aii-os.desktop share/man/man1/aii.1.gz share/icons/hicolor/16x16/apps/aii-os.png ;;
  esac ;;
complete) echo "complete: $*" ;;
*) exit 2 ;;
esac
`, 0o755)
	write(filepath.Join(f.side, "go"), `#!/bin/sh
if [ "$1" = run ]; then
  out=
  while [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done
  [ -n "$out" ] || exit 2
  echo "third-party licences" > "$out"; exit 0
fi
[ "$1" = build ] || exit 2
if [ "$2" = -o ]; then cp "$FAKE_TOOL" "$3" && chmod +x "$3"; exit 0; fi
out=
while [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done
[ -n "$out" ] || exit 2
echo placeholder > "$out"
echo "$GOOS/$GOARCH $(basename "$out")" >> "$GO_LOG"
`, 0o755)
	return f
}

func (f archiveFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", args...)
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), "GO="+filepath.Join(f.side, "go"), "AII_GO=", "VERSION=", "OUT=", "TOOL=", "UNSIGNED=",
		"SIGN_CMD=", "SIGNTOOL=", "BUILD_MACOS=", "FAKE_TOOL="+filepath.Join(f.side, "aii-release"), "GO_LOG="+f.goLog)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f archiveFixture) builds(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.goLog)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// Each Linux row is built for the GOOS and arch the row names, every
// program the contract names, and archived under the row's own name. The
// second row's target is one no table here holds: only the row says it.
func TestTheArchivesAreBuiltForTheTargetTheirRowNames(t *testing.T) {
	f := newArchiveFixture(t,
		"aii-os_9.9.9_linux_amd64.tar.gz linux amd64 linux",
		"aii-os_9.9.9_linux_riscv64.tar.gz linux riscv64 freebsd",
		"aii-os_9.9.9_macos_arm64.tar.gz macos arm64 darwin",
		"aii-os-app_9.9.9_macos_arm64.zip macos arm64 darwin",
		"aii-os_9.9.9_amd64.deb linux amd64 linux")
	out, err := f.run(t, "packaging/archives.sh", "linux")
	if err != nil {
		t.Fatalf("archives.sh linux: %v\n%s", err, out)
	}
	want := strings.Join([]string{
		"linux/amd64 aii", "linux/amd64 aii-app", "linux/amd64 aii-vulkan-probe",
		"freebsd/riscv64 aii", "freebsd/riscv64 aii-app", "freebsd/riscv64 aii-vulkan-probe",
	}, "\n")
	if got := f.builds(t); got != want {
		t.Fatalf("builds:\n%s\nwant, each row for its own target and no other row built:\n%s", got, want)
	}
	for _, built := range []string{"aii-os_9.9.9_linux_amd64.tar.gz", "aii-os_9.9.9_linux_riscv64.tar.gz"} {
		listing, err := exec.Command("tar", "-tzf", filepath.Join(f.root, "dist", built)).Output()
		if err != nil {
			t.Fatalf("%s was not written: %v", built, err)
		}
		if got, want := strings.TrimSpace(string(listing)), "aii\naii-app\naii-vulkan-probe\nLICENSE\nTHIRD_PARTY_LICENSES\nshare/applications/aii-os.desktop\nshare/man/man1/aii.1.gz\nshare/icons/hicolor/16x16/apps/aii-os.png"; got != want {
			t.Fatalf("%s holds:\n%s\nwant what archive-files answered, in its order:\n%s", built, got, want)
		}
	}
	for _, other := range []string{"aii-os_9.9.9_macos_arm64.tar.gz", "aii-os-app_9.9.9_macos_arm64.zip", "aii-os_9.9.9_amd64.deb"} {
		if _, err := os.Stat(filepath.Join(f.root, "dist", other)); !os.IsNotExist(err) {
			t.Errorf("archives.sh linux wrote %s (%v)", other, err)
		}
	}
	// Every archive written is read back before the script ends.
	if !strings.Contains(out, "complete: complete -dir "+filepath.Join(f.root, "dist")+" -version 9.9.9 -platform linux") {
		t.Fatalf("the archives were not read back (aii-release complete):\n%s", out)
	}
}

// THE WINDOWS ARCHIVE IS WHAT ITS INSTALLER'S BUILD STAGED, AND NOTHING IS
// BUILT FOR IT. Its programs were built a second time for the archive,
// without the installer's resources, and signed a second time; the
// launcher was not in it at all (0.1.16). archives.sh windows runs no
// build: it zips the four staged files under the names the contract
// gives, byte for byte, uses the stage once, and without a stage says
// which script makes one.
func TestTheWindowsArchiveIsWhatItsInstallerStaged(t *testing.T) {
	const row = "aii-os_9.9.9_windows_amd64.zip windows amd64 windows"
	f := newArchiveFixture(t, row)
	if out, err := f.run(t, "packaging/archives.sh", "windows"); err == nil || !strings.Contains(out, "packaging/windows/build-setup.sh stages what its installer writes") {
		t.Fatalf("archives.sh windows without a stage (%v):\n%s", err, out)
	}
	staged := map[string]string{"aii.exe": "MZ the installer's aii.exe", "AII OS.exe": "MZ the installer's launcher", "LICENSE": "the license", "THIRD_PARTY_LICENSES": "the licences"}
	stage := filepath.Join(f.root, "dist", "windows-amd64")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range staged {
		if err := os.WriteFile(filepath.Join(stage, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := f.run(t, "packaging/archives.sh", "windows")
	if err != nil {
		t.Fatalf("archives.sh windows: %v\n%s", err, out)
	}
	if got := f.builds(t); got != "" {
		t.Fatalf("a program was built for the Windows archive; it carries the installer's own:\n%s", got)
	}
	archive := filepath.Join(f.root, "dist", "aii-os_9.9.9_windows_amd64.zip")
	listing, err := exec.Command("unzip", "-Z1", archive).Output()
	if err != nil {
		t.Fatalf("the archive was not written: %v", err)
	}
	if got, want := strings.TrimSpace(string(listing)), "aii.exe\nAII OS.exe\nLICENSE\nTHIRD_PARTY_LICENSES"; got != want {
		t.Fatalf("the archive holds:\n%s\nwant:\n%s", got, want)
	}
	for name, body := range staged {
		got, err := exec.Command("unzip", "-p", archive, name).Output()
		if err != nil || string(got) != body {
			t.Errorf("%s in the archive is %q (%v), want the staged file %q", name, got, err, body)
		}
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Errorf("the stage was left behind for a later run to archive again (%v)", err)
	}
	if !strings.Contains(out, "-platform windows") {
		t.Fatalf("the archive was not read back:\n%s", out)
	}
}

// A row the release has no build for stops it by name before anything is
// built: through archives.sh for a row of its platform that names no
// target, through build-all.sh for a platform it has no packager for, and
// through release.sh prepare, which runs both, before any installer,
// binding or signing payload.
func TestPrepareRefusesATargetItHasNoBuildFor(t *testing.T) {
	for _, c := range []struct{ name, row string }{
		{"a platform with no packager", "aii-os_9.9.9_plan9_amd64.tar.gz plan9 amd64 "},
		{"a row with no arch", "aii-os_9.9.9_linux_arm64.tar.gz linux"},
	} {
		asset := strings.Fields(c.row)[0]
		rows := []string{"aii-os_9.9.9_linux_amd64.tar.gz linux amd64 linux", "aii-os_9.9.9_macos_arm64.tar.gz macos arm64 darwin", c.row}
		t.Run(c.name+"/build-all", func(t *testing.T) {
			f := newArchiveFixture(t, rows...)
			out, err := f.run(t, "packaging/build-all.sh")
			if err == nil || !strings.Contains(out, "no build rule for asset "+asset) {
				t.Fatalf("build-all.sh did not refuse the row by name (%v):\n%s", err, out)
			}
			if got := f.builds(t); got != "" {
				t.Fatalf("something was built before the refusal:\n%s", got)
			}
			for _, later := range []string{"=== ubuntu", "=== linux", "=== windows", "=== macos"} {
				if strings.Contains(out, later) {
					t.Fatalf("build-all.sh went on to %q after the refusal:\n%s", later, out)
				}
			}
		})
		t.Run(c.name+"/prepare", func(t *testing.T) {
			f := newArchiveFixture(t, rows...)
			for _, args := range [][]string{
				{"init", "-q"}, {"add", "-A"},
				{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "fixture"},
			} {
				if out, err := exec.Command("git", append([]string{"-C", f.root}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			out, err := f.run(t, "packaging/release.sh", "prepare")
			if err == nil {
				t.Fatalf("prepare accepted a target it has no build for:\n%s", out)
			}
			if !strings.Contains(out, "no build rule for asset "+asset) {
				t.Fatalf("prepare failed for another reason, or without naming the asset:\n%s", out)
			}
			for _, later := range []string{"=== ubuntu", "inventory: every supported target", "bind every shipped executable", "emit signing payloads", "candidate prepared"} {
				if strings.Contains(out, later) {
					t.Fatalf("prepare went on to %q after the refusal:\n%s", later, out)
				}
			}
			cand := filepath.Join(f.root, "dist", "candidate")
			if _, err := os.Stat(filepath.Join(cand, "CANDIDATE.txt")); !os.IsNotExist(err) {
				t.Fatalf("a CANDIDATE.txt was written (%v)", err)
			}
			if payloads, _ := filepath.Glob(filepath.Join(cand, "*.payload.json")); len(payloads) > 0 {
				t.Fatalf("signing payloads were written: %v", payloads)
			}
			if got := f.builds(t); got != "" {
				t.Fatalf("something was built before the refusal:\n%s", got)
			}
		})
	}
	// And archives.sh itself, asked for the platform of a row that names
	// no target, refuses that row by name; the row before it was built.
	f := newArchiveFixture(t, "aii-os_9.9.9_linux_amd64.tar.gz linux amd64 linux", "aii-os_9.9.9_linux_arm64.tar.gz linux")
	out, err := f.run(t, "packaging/archives.sh", "linux")
	if err == nil || !strings.Contains(out, "no build rule for asset aii-os_9.9.9_linux_arm64.tar.gz") {
		t.Fatalf("archives.sh did not refuse the row by name (%v):\n%s", err, out)
	}
	if strings.Contains(out, "complete: ") {
		t.Fatalf("archives.sh went on to its check after the refusal:\n%s", out)
	}
}
