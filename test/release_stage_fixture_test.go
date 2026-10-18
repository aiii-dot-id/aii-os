//go:build !windows

package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const fakeAiiRelease = `#!/bin/sh
cmd=$1; shift
case "$cmd" in
assets) printf '%s\n' "aii-os_9.9.9_linux_amd64.tar.gz linux amd64 linux" "aii-os_9.9.9_windows_amd64.zip windows amd64 windows" "aii-os_9.9.9_amd64.deb linux amd64 linux" ;;
verify)
  kind=release; art=; rev=
  while [ $# -gt 0 ]; do
    case "$1" in -kind) kind=$2; shift ;; -artifact) art=$2; shift ;; -source-rev) rev=$2; shift ;; esac
    shift
  done
  if [ "$kind" = evidence ] && [ "$(cat "$art")" != "evidence for $rev" ]; then
    echo "NOT VERIFIED: the bundle binds another source" >&2; exit 1
  fi
  if [ "$kind" = release ] && [ -n "$FAKE_REFUSE" ] && [ "$(basename "$art")" = "$FAKE_REFUSE" ]; then
    echo "NOT VERIFIED: the envelope is signed by another root" >&2; exit 1
  fi
  echo "VERIFIED $kind" ;;
*) exit 2 ;;
esac
`

const fakeGo = `#!/bin/sh
[ "$1" = build ] && [ "$2" = -o ] || exit 2
cp "$FAKE_TOOL" "$3" && chmod +x "$3"
`

const recordingGh = `#!/bin/sh
echo "$*" >> "$GH_LOG"
if [ "$1 $2" = "release view" ]; then
  case "$*" in
    *"--json isDraft"*) echo true; exit 0 ;;
    *"--json assets"*) for a in $FAKE_REMOTE; do echo "$a"; done; exit 0 ;;
  esac
  [ -n "$FAKE_DRAFT" ] && exit 0
  exit 1
fi
exit 0
`

const recordingPublicDerive = `#!/bin/sh
echo "$*" >> "$GUARD_LOG"
[ "$1" = guard-release ] || exit 2
shift
while [ $# -gt 0 ]; do
  case "$1" in
    -repo) repo=$2 ;;
    -remote-url) remote=$2 ;;
    -tag) tag=$2 ;;
    -source-rev) rev=$2 ;;
    -target) target=$2 ;;
    *) exit 2 ;;
  esac
  shift 2
done
[ "$repo" = "$EXPECTED_PUBLIC_ROOT" ] || exit 1
[ "$remote" = https://github.com/example/aii-os.git ] || exit 1
[ "$tag" = v9.9.9 ] && [ "$rev" = abc123 ] && [ "$target" = abc123 ] || exit 1
[ -z "$FAKE_GUARD_REFUSE" ] || exit 1
echo "${FAKE_GUARD_TARGET:-abc123}"
`

var publicSet = []string{
	"AII-OS-9.9.9.dmg", "aii-os_9.9.9_amd64.deb", "aii-os_9.9.9_amd64.deb.platform.sig",
	"aii-os_9.9.9_linux_amd64.tar.gz", "aii-os_9.9.9_linux_amd64.tar.gz.platform.sig",
	"aii-os_9.9.9_windows_amd64.zip", "aii-os_9.9.9_windows_amd64.zip.platform.sig",
	"aii-setup-9.9.9-amd64.exe",
}

type releaseFixture struct {
	root, cand, evidence, ghLog, guardLog string
	env                                   []string
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	f := &releaseFixture{root: t.TempDir()}
	bin, side := t.TempDir(), t.TempDir()
	f.cand = filepath.Join(f.root, "dist", "candidate")
	f.evidence = filepath.Join(side, "evidence-9.9.9-abc123.tar.gz")
	f.ghLog = filepath.Join(side, "gh.log")
	f.guardLog = filepath.Join(side, "guard.log")
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(f.root, "packaging", "release.sh"), string(script), 0o755)
	write(filepath.Join(f.root, "VERSION"), "9.9.9\n", 0o644)
	write(filepath.Join(side, "aii-release"), fakeAiiRelease, 0o755)
	write(filepath.Join(bin, "go"), fakeGo, 0o755)
	write(filepath.Join(bin, "gh"), recordingGh, 0o755)
	write(filepath.Join(bin, "public-derive"), recordingPublicDerive, 0o755)
	for _, name := range publicSet {
		write(filepath.Join(f.cand, name), "bytes of "+name, 0o644)
	}
	for _, a := range []string{"aii-os_9.9.9_linux_amd64.tar.gz", "aii-os_9.9.9_windows_amd64.zip", "aii-os_9.9.9_amd64.deb"} {
		write(filepath.Join(f.cand, a+".payload.json"), "{}", 0o644)
	}
	write(filepath.Join(f.cand, "CANDIDATE.txt"), "source_rev abc123\nversion 9.9.9\n"+
		"aii-os_9.9.9_linux_amd64.tar.gz linux amd64\naii-os_9.9.9_windows_amd64.zip windows amd64\naii-os_9.9.9_amd64.deb linux amd64\n", 0o644)
	write(f.evidence, "evidence for abc123", 0o644)
	write(f.evidence+".platform.sig", "signature", 0o644)
	f.env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GO="+filepath.Join(bin, "go"), "FAKE_TOOL="+filepath.Join(side, "aii-release"),
		"GH_LOG="+f.ghLog, "GUARD_LOG="+f.guardLog, "EXPECTED_PUBLIC_ROOT="+f.root,
		"PUBLIC_DERIVE="+filepath.Join(bin, "public-derive"),
		"RELEASE_REPO=example/aii-os", "EVIDENCE="+f.evidence)
	return f
}

func (f *releaseFixture) stage(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "packaging/release.sh", "stage")
	cmd.Dir = f.root
	cmd.Env = append(append([]string{}, f.env...), extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *releaseFixture) attach(t *testing.T, extra ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command("sh", "packaging/release.sh", "attach")
	cmd.Dir = f.root
	cmd.Env = append(append([]string{}, f.env...), extra...)
	var o, e strings.Builder
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

func TestAttachSealsOnlyWhatTheVerifierAdmits(t *testing.T) {
	f := newReleaseFixture(t)
	out, errOut, err := f.attach(t)
	if err != nil || !strings.Contains(out, "candidate SEALED: 3 asset(s) verified") {
		t.Fatalf("a candidate every envelope of which verifies was not sealed (%v):\n%s%s", err, out, errOut)
	}

	g := newReleaseFixture(t)
	out, errOut, err = g.attach(t, "FAKE_REFUSE=aii-os_9.9.9_windows_amd64.zip")
	if err == nil {
		t.Fatalf("attach exited 0 over a refused envelope:\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, "verification REFUSED for aii-os_9.9.9_windows_amd64.zip: NOT VERIFIED: the envelope is signed by another root") {
		t.Fatalf("the verifier's refusal did not reach stderr with the asset named:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if strings.Contains(out+errOut, "SEALED") {
		t.Fatalf("announced a seal while refusing:\n%s%s", out, errOut)
	}

	h := newReleaseFixture(t)
	if err := os.Remove(filepath.Join(h.cand, "aii-os_9.9.9_linux_amd64.tar.gz.platform.sig")); err != nil {
		t.Fatal(err)
	}
	out, errOut, err = h.attach(t)
	if err == nil || strings.Contains(out+errOut, "SEALED") {
		t.Fatalf("attach sealed a candidate missing a signature (%v):\n%s%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "aii-os_9.9.9_linux_amd64.tar.gz has no signature") {
		t.Fatalf("the missing signature was not named on stderr:\n%s", errOut)
	}
}

func (f *releaseFixture) ghCalls(t *testing.T) []string {
	b, err := os.ReadFile(f.ghLog)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestStagingUploadsExactlyTheCheckedPublicSet(t *testing.T) {
	f := newReleaseFixture(t)
	out, err := f.stage(t)
	if err != nil || !strings.Contains(out, "READY TO PUBLISH") {
		t.Fatalf("a complete candidate did not stage (%v):\n%s", err, out)
	}
	var uploaded []string
	created := false
	for _, c := range f.ghCalls(t) {
		if strings.HasPrefix(c, "release create ") {
			created = true
			if !strings.Contains(c, "--verify-tag") || strings.Contains(c, "--target") {
				t.Fatalf("release creation could create an unchecked tag: %s", c)
			}
		}
		if strings.HasPrefix(c, "release upload ") {
			uploaded = append(uploaded, filepath.Base(strings.Fields(c)[3]))
		}
	}
	if !created {
		t.Fatal("the successful control did not create a draft")
	}
	guard, err := os.ReadFile(f.guardLog)
	if err != nil || !strings.Contains(string(guard), "guard-release -repo "+f.root+" -remote-url https://github.com/example/aii-os.git -tag v9.9.9 -source-rev abc123 -target abc123") {
		t.Fatalf("the public source was not verified with its exact binding: %s (%v)", guard, err)
	}
	sort.Strings(uploaded)
	want := append(append([]string{}, publicSet...), "SHA256SUMS")
	sort.Strings(want)
	if strings.Join(uploaded, " ") != strings.Join(want, " ") {
		t.Fatalf("uploaded %v, want exactly %v", uploaded, want)
	}
	sums, err := os.ReadFile(filepath.Join(f.cand, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	var summed []string
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		summed = append(summed, strings.TrimPrefix(strings.Fields(line)[1], "*"))
	}
	sort.Strings(summed)
	if strings.Join(summed, " ") != strings.Join(publicSet, " ") {
		t.Fatalf("SHA256SUMS covers %v, want exactly %v", summed, publicSet)
	}

	g := newReleaseFixture(t)
	if out, err := g.stage(t, "FAKE_DRAFT=1", "FAKE_REMOTE=aii-os_9.9.9_linux_amd64.tar.gz SHA256SUMS"); err != nil {
		t.Fatalf("a draft holding only this candidate's assets was refused (%v):\n%s", err, out)
	}
}

func TestStagingRequiresVerifiedPublicSourceBeforeAnyGitHubCall(t *testing.T) {
	for _, c := range []struct {
		name string
		env  []string
	}{
		{"arbitrary target", []string{"RELEASE_TARGET=other"}},
		{"private or unregistered source", []string{"FAKE_GUARD_REFUSE=1"}},
		{"guard returned another source", []string{"FAKE_GUARD_TARGET=other"}},
		{"wrong destination", []string{"RELEASE_REPO=other/repo"}},
		{"missing verifier", []string{"PUBLIC_DERIVE=/no-such-public-derive"}},
		{"existing draft still requires provenance", []string{"FAKE_DRAFT=1", "FAKE_GUARD_REFUSE=1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newReleaseFixture(t)
			out, err := f.stage(t, c.env...)
			if err == nil || strings.Contains(out, "READY TO PUBLISH") {
				t.Fatalf("unverified source was staged (%v): %s", err, out)
			}
			if calls := f.ghCalls(t); len(calls) != 0 {
				t.Fatalf("called GitHub before source admission: %v", calls)
			}
		})
	}
}

func TestStagingRefusesAnIncompleteOrStrayCandidateBeforeWriting(t *testing.T) {
	remove := func(f *releaseFixture, t *testing.T, names ...string) {
		for _, n := range names {
			if err := os.Remove(filepath.Join(f.cand, n)); err != nil {
				t.Fatal(err)
			}
		}
	}
	rows := func(f *releaseFixture, t *testing.T, body string) {
		if err := os.WriteFile(filepath.Join(f.cand, "CANDIDATE.txt"), []byte("source_rev abc123\nversion 9.9.9\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, says string
		env        []string
		mutate     func(*releaseFixture, *testing.T)
	}{
		{"a target is missing", "is missing from the candidate", nil, func(f *releaseFixture, t *testing.T) {
			remove(f, t, "aii-os_9.9.9_windows_amd64.zip")
		}},
		{"a target and its row are gone", "does not list exactly", nil, func(f *releaseFixture, t *testing.T) {
			remove(f, t, "aii-os_9.9.9_windows_amd64.zip", "aii-os_9.9.9_windows_amd64.zip.platform.sig", "aii-os_9.9.9_windows_amd64.zip.payload.json")
			rows(f, t, "aii-os_9.9.9_linux_amd64.tar.gz linux amd64\n")
		}},
		{"an installer is gone", "AII-OS-9.9.9.dmg is missing", nil, func(f *releaseFixture, t *testing.T) {
			remove(f, t, "AII-OS-9.9.9.dmg")
		}},
		{"a target is listed twice", "does not list exactly", nil, func(f *releaseFixture, t *testing.T) {
			rows(f, t, "aii-os_9.9.9_linux_amd64.tar.gz linux amd64\naii-os_9.9.9_linux_amd64.tar.gz linux amd64\naii-os_9.9.9_windows_amd64.zip windows amd64\n")
		}},
		{"a target is listed under another platform", "does not list exactly", nil, func(f *releaseFixture, t *testing.T) {
			rows(f, t, "aii-os_9.9.9_linux_amd64.tar.gz linux arm64\naii-os_9.9.9_windows_amd64.zip windows amd64\n")
		}},
		{"the evidence binds another source", "the evidence REFUSED", nil, func(f *releaseFixture, t *testing.T) {
			if err := os.WriteFile(f.evidence, []byte("evidence for 0ther"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"there is no evidence", "set EVIDENCE=", []string{"EVIDENCE="}, nil},
		{"a stray file is in the candidate", "not part of it", nil, func(f *releaseFixture, t *testing.T) {
			if err := os.WriteFile(filepath.Join(f.cand, "build.log"), []byte("a log"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"the draft holds an asset the candidate does not", "already holds assets", []string{"FAKE_DRAFT=1", "FAKE_REMOTE=aii-os_9.9.9_linux_amd64.tar.gz old-debug.log"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newReleaseFixture(t)
			if c.mutate != nil {
				c.mutate(f, t)
			}
			out, err := f.stage(t, c.env...)
			if err == nil {
				t.Fatalf("staged anyway:\n%s", out)
			}
			if !strings.Contains(out, c.says) {
				t.Fatalf("refused for another reason (want %q):\n%s", c.says, out)
			}

			sealed := strings.Contains(strings.Join(c.env, " "), "FAKE_DRAFT=1")
			if strings.Contains(out, "READY TO PUBLISH") || (!sealed && strings.Contains(out, "SEALED")) {
				t.Fatalf("claimed readiness while refusing:\n%s", out)
			}
			for _, call := range f.ghCalls(t) {
				if !strings.HasPrefix(call, "release view ") {
					t.Fatalf("wrote to the release before refusing: gh %s", call)
				}
			}
		})
	}
}
