//go:build darwin

package pluginhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAContainedEngineOnMacOSReadsWhatItIsHandedAndNothingElseOfTheAccounts(t *testing.T) {
	requireWall(t)
	l := newEngineLayout(t)
	asks := []string{"read=" + l.ownFile, "read=" + l.model, "read=" + l.state, "read=" + l.credential, "read=" + l.sketch}
	var control []string
	for _, p := range []string{l.ownFile, l.model, l.state, l.credential, l.sketch} {
		control = append(control, "read "+p+"=read")
	}
	// The account's own home, by a file every account has and that holds
	// nothing secret (CoreFoundation's text encoding), so the test makes
	// nothing there.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if mine := filepath.Join(home, ".CFUserTextEncoding"); fileExists(mine) {
		for _, p := range []string{mine, filepath.Join("/System/Volumes", "Data") + mine} {
			asks = append(asks, "read="+p)
			control = append(control, "read "+p+"=read")
		}
	}
	if local := firstReadableFile("/usr/local"); local != "" {
		asks = append(asks, "read="+local)
		control = append(control, "read "+local+"=read")
	} else {
		t.Log("no readable file under /usr/local on this host: that part of the view is not tried")
	}
	out := l.probe(t, l.files(), asks, control)
	for _, handed := range []string{l.ownFile, l.model, l.state} {
		if !strings.Contains(out, "read "+handed+"=read") {
			t.Errorf("the engine could not read what it is handed (%s):\n%s", handed, out)
		}
	}
	for _, ask := range asks[3:] {
		kept := strings.TrimPrefix(ask, "read=")
		if !strings.Contains(out, "read "+kept+"=refused open "+kept+": operation not permitted") {
			t.Errorf("the engine reached a file of the account's it was not handed (%s):\n%s", kept, out)
		}
	}
	// A directory it is not handed is not listed either.
	if out := l.contained(t, l.files(), []string{"list=" + l.project, "list=" + home}); strings.Count(out, "list=refused") != 2 {
		t.Errorf("the engine listed a directory it is not handed:\n%s", out)
	}
}

func TestAContainedEngineOnMacOSLearnsNoNameItIsNotHanded(t *testing.T) {
	requireWall(t)
	l := newEngineLayout(t)
	probe := buildNameProbe(t, l)
	name := "aiiw-name-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	secret := filepath.Join(filepath.Dir(l.credential), name)
	if err := os.WriteFile(secret, []byte("a credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(filepath.Dir(l.credential), "nothing-here")
	say := func(contained bool, args ...string) string {
		t.Helper()
		argv := append([]string{probe}, args...)
		if contained {
			wrapped, _, err := containArgv(t.Context(), argv, nil, l.files())
			if err != nil {
				t.Fatalf("containment refused on a host that can build it: %v", err)
			}
			argv = wrapped
		}
		out, _ := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	ids := say(false, "ids", secret)
	volume := filepath.Join("/System/Volumes", "Data")
	// The control: uncontained, each of the three tells.
	if got := say(false, "stat", secret); got != "stat=there" {
		t.Fatalf("control: %s", got)
	}
	if got := say(false, "stat", missing); !strings.Contains(got, "No such file") {
		t.Fatalf("control: %s", got)
	}
	if got := say(false, "fsgetpath", ids); got != "fsgetpath="+secret {
		t.Fatalf("control: fsgetpath must name the file uncontained, or its refusal below proves nothing: %s", got)
	}
	searchable := say(false, "searchfs", volume, name) == "searchfs=1"
	if !searchable {
		t.Logf("rig: uncontained, searchfs did not find the file on %s: its half is not tried", volume)
	}
	// Behind the wall: the file and a missing one look alike, and neither
	// name is given.
	there, absent := say(true, "stat", secret), say(true, "stat", missing)
	if there != absent || !strings.Contains(there, "Operation not permitted") {
		t.Errorf("behind the wall a file outside what it sees and a missing one must look alike: %q against %q", there, absent)
	}
	if got := say(true, "fsgetpath", ids); !strings.HasPrefix(got, "fsgetpath=refused") {
		t.Errorf("behind the wall fsgetpath named a file the engine is not handed: %s", got)
	}
	if searchable {
		if got := say(true, "searchfs", volume, name); !strings.HasPrefix(got, "searchfs=refused") {
			t.Errorf("behind the wall searchfs found a file the engine is not handed: %s", got)
		}
	}
}

// buildNameProbe builds testdata/nameprobe into the engine's own directory,
// so the wall lets it start, or skips where the system's compiler is absent.
func buildNameProbe(t *testing.T, l engineLayout) string {
	t.Helper()
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("no xcrun: the C probe cannot be built here")
	}
	src, err := filepath.Abs("testdata/nameprobe/probe.c")
	if err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(l.own, "nameprobe")
	if out, err := exec.Command("xcrun", "clang", "-O1", "-o", probe, src).CombinedOutput(); err != nil {
		t.Skipf("the C probe does not build here (%v): %s", err, bytes.TrimSpace(out))
	}
	return probe
}

// NOR, IN A VIEW THE OPERATOR WIDENED, WHETHER A REFUSED FILE IS THERE. The
// system's ssh directory and the account's stay refused wherever the view
// shows (seatbeltRefused). Refused the read alone, inside a view widened to
// /private/etc or /Users, whose allow shows existence, a file there said
// "operation not permitted" and a missing one "no such file". The control:
// uncontained, each says which it is; behind the wall a missing name the
// widened view does show still says "no such file", so the view is widened.
func TestAContainedEngineOnMacOSCannotTellARefusedFileInAWidenedViewFromAMissingOne(t *testing.T) {
	requireWall(t)
	l := newEngineLayout(t)
	probe := buildNameProbe(t, l)
	say := func(files *nativeFiles, path string) string {
		t.Helper()
		argv := []string{probe, "stat", path}
		if files != nil {
			wrapped, _, err := containArgv(t.Context(), argv, nil, *files)
			if err != nil {
				t.Fatalf("containment refused on a host that can build it: %v", err)
			}
			argv = wrapped
		}
		out, _ := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	missing := "aiiw-missing-" + strconv.Itoa(os.Getpid())
	type pair struct{ there, absent string }
	var pairs []pair
	for _, p := range []string{"/private/etc/ssh/sshd_config", "/private/etc/ssh/ssh_config"} {
		if fileExists(p) {
			pairs = append(pairs, pair{p, filepath.Join("/private/etc/ssh", missing)})
			break
		}
	}
	files := l.files()
	files.view = []string{"/private/etc"}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if mine := filepath.Join(home, ".ssh"); filepath.Dir(home) == "/Users" && fileExists(mine) {
		pairs = append(pairs, pair{mine, filepath.Join(mine, missing)})
		files.view = append(files.view, "/Users")
	} else {
		t.Logf("no ~/.ssh under /Users on this host: the account's half is not tried")
	}
	if len(pairs) == 0 {
		t.Skip("no ssh configuration under /private/etc/ssh and no ~/.ssh: nothing refused to tell apart")
	}
	if got := say(&files, filepath.Join("/private/etc", missing)); !strings.Contains(got, "No such file") {
		t.Fatalf("control: behind the wall a missing name the widened view shows must say so, or the view is not widened: %s", got)
	}
	for _, p := range pairs {
		if got := say(nil, p.there); got != "stat=there" {
			t.Fatalf("control: %s: %s", p.there, got)
		}
		if got := say(nil, p.absent); !strings.Contains(got, "No such file") {
			t.Fatalf("control: %s: %s", p.absent, got)
		}
		there, absent := say(&files, p.there), say(&files, p.absent)
		if there != absent || !strings.Contains(there, "Operation not permitted") {
			t.Errorf("in a view widened to %q, a refused file and a missing one beside it must look alike: %s %q against %s %q", files.view, p.there, there, p.absent, absent)
		}
	}
}

// What the engine is handed it reads and does not write.
func TestAContainedEngineOnMacOSWritesNothingItIsHanded(t *testing.T) {
	requireWall(t)
	l := newEngineLayout(t)
	var asks, made []string
	for _, dir := range []string{l.own, l.models, l.private} {
		made = append(made, filepath.Join(dir, "made-here"))
		asks = append(asks, "write="+made[len(made)-1])
	}
	t.Cleanup(func() {
		for _, m := range made {
			os.Remove(m)
		}
	})
	out := runWallProbe(t, append([]string{l.child, "reach"}, asks...))
	if strings.Count(out, "write=written") != len(made) {
		t.Fatalf("control: uncontained, the engine must write each directory it is handed, or the refusal behind the wall proves nothing:\n%s", out)
	}
	for _, m := range made {
		if err := os.Remove(m); err != nil {
			t.Fatal(err)
		}
	}
	out = l.contained(t, l.files(), asks)
	if strings.Count(out, "write=refused") != len(made) || strings.Count(out, "operation not permitted") != len(made) {
		t.Errorf("the engine wrote into what it is handed, or was refused otherwise than by the wall:\n%s", out)
	}
	for _, m := range made {
		if _, err := os.Lstat(m); err == nil {
			t.Errorf("a file the contained engine made is on the host: %s", m)
		}
	}
}

// The view is the operator's to add to (plugins.native_view): a directory
// named there is read, beside the default's, and one not named is not;
// naming it drops nothing of the default; the credential stores stay
// refused inside what it adds.
func TestAContainedEngineOnMacOSSeesWhatTheOperatorAddsToTheView(t *testing.T) {
	requireWall(t)
	l := newEngineLayout(t)
	const system = "/usr/bin/env" // in the default view on every Mac
	asks := []string{"read=" + l.sketch, "write=" + filepath.Join(l.project, "made-here"), "read=" + system}
	t.Cleanup(func() { os.Remove(filepath.Join(l.project, "made-here")) })
	out := l.probe(t, l.files(), asks, []string{"read " + l.sketch + "=read", "write=written", "read " + system + "=read"})
	if err := os.Remove(filepath.Join(l.project, "made-here")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "read "+l.sketch+"=refused") {
		t.Fatalf("with the default view the engine read a project it is not handed:\n%s", out)
	}
	if !strings.Contains(out, "read "+system+"=read") {
		t.Fatalf("with the default view the engine could not read the system's %s:\n%s", system, out)
	}
	files := l.files()
	files.view = []string{l.project}
	out = l.contained(t, files, asks)
	if !strings.Contains(out, "read "+l.sketch+"=read") {
		t.Errorf("a directory the operator adds to the view is not read:\n%s", out)
	}
	if !strings.Contains(out, "read "+system+"=read") {
		t.Errorf("A DIRECTORY THE OPERATOR ADDS REPLACED THE DEFAULT VIEW: the engine no longer reads the system's %s:\n%s", system, out)
	}
	if !strings.Contains(out, "write=refused") {
		t.Errorf("a directory the operator adds to the view is writable:\n%s", out)
	}
	// The system's own key directory stays refused wherever the view shows
	// it: a view widened to /private/etc reads its hosts file and not ssh's.
	files.view = []string{l.project, "/private/etc"}
	if out := l.contained(t, files, []string{"read=/private/etc/ssh/sshd_config", "read=/private/etc/hosts"}); !strings.Contains(out, "read /private/etc/ssh/sshd_config=refused") || !strings.Contains(out, "read /private/etc/hosts=read") {
		if fileExists("/private/etc/ssh/sshd_config") {
			t.Errorf("a view widened to /private/etc brought the system's ssh directory in, or did not show /private/etc:\n%s", out)
		}
	}
}

// firstReadableFile is a regular file under dir the account can read, or "".
func firstReadableFile(dir string) string {
	var found string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			if f, err := os.Open(p); err == nil {
				f.Close()
				found = p
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
