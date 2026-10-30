//go:build linux

package pluginhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestAContainedEngineReadsWhatItIsHandedAndNothingElseOfTheAccounts(t *testing.T) {
	requireWall(t)
	l := newEngineLayout(t)
	asks := []string{"read=" + l.ownFile, "read=" + l.model, "read=" + l.state, "read=" + l.credential, "read=" + l.sketch}
	var control []string
	for _, p := range []string{l.ownFile, l.model, l.state, l.credential, l.sketch} {
		control = append(control, "read "+p+"=read")
	}
	out := l.probe(t, l.files(), asks, control)
	for _, handed := range []string{l.ownFile, l.model, l.state} {
		if !strings.Contains(out, "read "+handed+"=read") {
			t.Errorf("the engine could not read what it is handed (%s):\n%s", handed, out)
		}
	}
	// Not handed, so not there: the refusal is the file's absence, not a
	// mask the engine could see the shape of.
	for _, kept := range []string{l.credential, l.sketch} {
		if !strings.Contains(out, "read "+kept+"=refused open "+kept+": no such file or directory") {
			t.Errorf("the engine reached a file of the account's it was not handed (%s):\n%s", kept, out)
		}
	}
}

func TestAContainedEngineWritesNothingItIsHanded(t *testing.T) {
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
	if strings.Count(out, "write=refused") != len(made) || strings.Count(out, "read-only file system") != len(made) {
		t.Errorf("the engine wrote into what it is handed, or was refused otherwise than by the read-only file system:\n%s", out)
	}
	for _, m := range made {
		if _, err := os.Lstat(m); err == nil {
			t.Errorf("a file the contained engine made is on the host: %s", m)
		}
	}
}

// The engine starts the system's programs, in the view, and its own; a
// program in a directory it is not handed is not there to start.
func TestAContainedEngineStartsNoProgramItIsNotHanded(t *testing.T) {
	requireWall(t)
	l := newEngineLayout(t)
	stranger := filepath.Join(l.project, "tool")
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stranger, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(l.own, "helper")
	if err := os.WriteFile(own, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	asks := []string{"start=" + stranger + "|reach|null", "start=" + own + "|reach|null", "start=/usr/bin/true"}
	if out := runWallProbe(t, append([]string{l.child, "reach"}, asks...)); strings.Count(out, "start=ran\n") != 3 {
		t.Fatalf("control: uncontained, the engine must start all three, or the refusal behind the wall proves nothing:\n%s", out)
	}
	out := l.contained(t, l.files(), asks)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("the engine reported %d lines, want 3:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "start=refused") || !strings.Contains(lines[0], "no such file or directory") {
		t.Errorf("the engine started a program in a directory it is not handed:\n%s", out)
	}
	if lines[1] != "start=ran" || lines[2] != "start=ran" {
		t.Errorf("the engine could not start its own program or the system's:\n%s", out)
	}
}

// The view is the operator's to add to (plugins.native_view): a directory
// named there is shown, read-only, beside the default's, and one not named
// is not; naming it drops nothing of the default (/usr is still read). The
// same file, the same engine, one setting apart.
func TestAContainedEngineSeesWhatTheOperatorAddsToTheView(t *testing.T) {
	requireWall(t)
	l := newEngineLayout(t)
	const system = "/usr/bin/env" // in the default view on every Linux system
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
		t.Errorf("a directory the operator adds to the view is not shown:\n%s", out)
	}
	if !strings.Contains(out, "read "+system+"=read") {
		t.Errorf("A DIRECTORY THE OPERATOR ADDS REPLACED THE DEFAULT VIEW: the engine no longer reads the system's %s:\n%s", system, out)
	}
	if !strings.Contains(out, "write=refused") {
		t.Errorf("a directory the operator adds to the view is writable:\n%s", out)
	}
}

// An operator's link grants its target, including a target outside the
// default view. The grant is read-only and never removes a kept-place mask.
func TestAContainedEngineSeesALinkedViewWithoutUnmaskingItsTarget(t *testing.T) {
	requireWall(t)
	l := newEngineLayout(t)
	link := filepath.Join(t.TempDir(), "view")
	if err := os.Symlink(l.project, filepath.Join(filepath.Dir(link), "target")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", link); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(l.project, "kept", "secret")
	if err := os.MkdirAll(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := l.files()
	files.view = []string{link}
	files.places = []*firewall.Rule{{Kind: firewall.KindSubstrate, Path: filepath.Dir(secret), Enforced: true}}
	note := filepath.Join(link, "notes.txt")
	linkedSecret := filepath.Join(link, "kept", "secret")
	written := filepath.Join(link, "written")
	t.Cleanup(func() { os.Remove(written) })
	asks := []string{"read=" + note, "read=" + secret, "read=" + linkedSecret, "write=" + written}
	out := runWallProbe(t, append([]string{l.child, "reach"}, asks...))
	for _, want := range []string{"read " + note + "=read", "read " + secret + "=read", "read " + linkedSecret + "=read", "write=written"} {
		if !strings.Contains(out, want) {
			t.Fatalf("uncontained control lacks %q:\n%s", want, out)
		}
	}
	if err := os.Remove(written); err != nil {
		t.Fatal(err)
	}
	out = l.contained(t, files, asks)
	for _, want := range []string{"read " + note + "=read", "read " + secret + "=refused", "read " + linkedSecret + "=refused", "write=refused"} {
		if !strings.Contains(out, want) {
			t.Errorf("linked view lacks %q:\n%s", want, out)
		}
	}
}
