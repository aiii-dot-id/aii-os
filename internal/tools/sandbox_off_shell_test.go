//go:build !windows

package tools

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// inQuotes quotes a path for a command: a temporary directory's path may
// hold a space.
func inQuotes(path string) string { return `"` + path + `"` }

// WITH THE SANDBOX OFF THE SHELL'S WALL ADMITS A PATH OUTSIDE EVERY ROOT,
// written in full, and the command runs: as an argument and as the
// directory the call runs in. Turned back on, it is outside the sandbox
// again.
func TestWithTheSandboxOffTheShellsWallAdmitsAPathOutsideEveryRoot(t *testing.T) {
	g := newOffRig(t)
	open := filepath.Join(g.elsewhere, "open.txt")

	if why := g.r.shellRefusal("cat " + inQuotes(open)); !strings.Contains(why, "is outside the sandbox") {
		t.Fatalf("with the sandbox on the wall refuses a path outside every root: %q", why)
	}
	if res := shellIn(t, g.r, g.elsewhere, "cat open.txt"); !res.Refused || !strings.Contains(res.Error, "outside sandbox") {
		t.Fatalf("with the sandbox on a working directory outside every root is refused: %+v", res)
	}

	g.r.SetSandboxOff(true)
	if why := g.r.shellRefusal("cat " + inQuotes(open)); why != "" {
		t.Fatalf("with the sandbox off the wall admits it: %s", why)
	}
	if res := shellIn(t, g.r, nil, "cat "+inQuotes(open)); res.Refused || res.Output != "open MARK\n" {
		t.Fatalf("the command runs: %+v", res)
	}
	if res := shellIn(t, g.r, g.elsewhere, "cat open.txt"); res.Refused || res.Output != "open MARK\n" {
		t.Fatalf("a call may run in a directory outside every root, and its relative paths are read from there: %+v", res)
	}
	// What the wall let through with the sandbox on it lets through still:
	// the home, the granted root, and a system directory, even one that
	// holds a place the host keeps.
	g.policy.SetPlaces([]*firewall.Rule{{ID: "sub.binary", Kind: firewall.KindSubstrate, Path: "/usr/lib/aii-rig/aii", Reason: "kept", Enforced: true}})
	for _, command := range []string{"cat notes.txt", "ls .", "cat " + inQuotes(filepath.Join(g.work, "proj", "main.go")), "ls /usr/lib", "cat /dev/null", "git diff HEAD~1"} {
		g.r.SetSandboxOff(false)
		on := g.r.shellRefusal(command)
		g.r.SetSandboxOff(true)
		if off := g.r.shellRefusal(command); on != "" || off != "" {
			t.Errorf("%q: sandbox on %q, off %q; turning the sandbox off refuses nothing it admitted", command, on, off)
		}
	}

	g.r.SetSandboxOff(false)
	if why := g.r.shellRefusal("cat " + inQuotes(open)); !strings.Contains(why, "is outside the sandbox") {
		t.Fatalf("turned back on, the wall holds at once: %q", why)
	}
}

// WITH THE SANDBOX OFF THE SHELL'S WALL STILL KEEPS THE SUBSTRATE. Every
// kept file is refused by its full path, as before. And the three ways a
// command reached the substrate that only the boundary refused are
// refused still:
//
//   - a folder that holds the home or a kept place, as an argument, as a
//     glob's expansion or as the directory the call runs in: the wall
//     reads the command's text and cannot see what grep -r, find or tar
//     does inside a folder, so "grep -r key /" would have read the key;
//   - a directory changed into: "cd <the folder above the home> && cat
//     vega/data/credentials/k.key" names no kept place as the wall reads
//     it, from the home, and opens one in the shell;
//   - a relative path that walks up out of where the call runs: "cd sub
//     && cat ../data/credentials/k.key" reads, from the home, as a path
//     beside the home.
//
// The file tools take those same folders, because they ask the floor
// about every file they reach
// (TestWithTheSandboxOffTheSubstrateIsStillRefusedToEveryTool).
func TestWithTheSandboxOffTheShellsWallStillKeepsTheSubstrate(t *testing.T) {
	g := newOffRig(t)
	g.r.SetSandboxOff(true)

	for _, f := range g.kept {
		for _, command := range []string{"cat " + inQuotes(f), "cp " + inQuotes(f) + " " + inQuotes(filepath.Join(g.elsewhere, "taken")), "echo x > " + inQuotes(f)} {
			if why := g.r.shellRefusal(command); why == "" {
				t.Errorf("the wall must refuse %q with the sandbox off", command)
			}
		}
	}
	for _, dir := range []string{filepath.Join(g.home, "data"), filepath.Join(g.base, "machine")} {
		for _, command := range []string{"ls " + inQuotes(dir), "cd " + inQuotes(dir), "tar cf - " + inQuotes(dir)} {
			if why := g.r.shellRefusal(command); !strings.Contains(why, "which the host keeps") {
				t.Errorf("%q: a kept directory is refused as kept: %q", command, why)
			}
		}
	}

	// A folder that holds the home or a kept place.
	holders := []string{string(filepath.Separator), g.base, filepath.Dir(g.base)}
	for _, sub := range []string{"keys", "store", "etc", "bin", "creds"} {
		holders = append(holders, filepath.Join(g.base, sub))
	}
	for _, dir := range holders {
		for _, command := range []string{"grep -r KEPT " + inQuotes(dir), "cd " + inQuotes(dir) + " && ls", "find " + inQuotes(dir) + " -type f"} {
			why, named := g.r.shellWall(command)
			if !strings.Contains(why, holdsSubstrateWhy) || named != dir {
				t.Errorf("%q: a folder that holds the home or a kept place is not the shell's: %q (named %q)", command, why, named)
			}
		}
		if _, refused := g.r.shellBase(g.r.policy.Look(), map[string]any{"cwd": dir}); refused == nil || !refused.Refused || !strings.Contains(refused.Error, holdsSubstrateWhy) {
			t.Errorf("cwd %s: the shell does not start in a folder that holds the home or a kept place: %+v", dir, refused)
		}
	}
	if why := g.r.shellRefusal("ls -R " + inQuotes(filepath.Join(g.base, "ke*"))); !strings.Contains(why, holdsSubstrateWhy) {
		t.Errorf("a glob that expands to such a folder: %q", why)
	}
	// Refused by whichever check meets the expansion first: where the
	// path holds no space that is the scan of the command's names.
	if why := g.r.shellRefusal("cat " + inQuotes(filepath.Join(g.base, "mach*", "state.json"))); why == "" {
		t.Error("a glob that expands into a kept place must be refused")
	}
	// A folder that holds neither is the shell's, and so is a file in it.
	for _, command := range []string{"grep -r MARK " + inQuotes(g.elsewhere), "cd " + inQuotes(g.elsewhere) + " && ls", "ls " + inQuotes(filepath.Join(g.elsewhere, "open.txt"))} {
		if why := g.r.shellRefusal(command); why != "" {
			t.Errorf("%q names a folder that holds neither: %s", command, why)
		}
	}
	if dir, refused := g.r.shellBase(g.r.policy.Look(), map[string]any{"cwd": g.elsewhere}); refused != nil || dir != g.elsewhere {
		t.Errorf("cwd in a folder that holds neither: %q %+v", dir, refused)
	}

	// A relative path that walks up out of where the call runs.
	for _, command := range []string{"cd sub && cat ../data/credentials/k.key", "cat ../elsewhere/open.txt", "cat ../../x", "ls .."} {
		why := g.r.shellRefusal(command)
		if !strings.Contains(why, "is written in full") {
			t.Errorf("%q: a relative path out of the home is refused, with the way on: %q", command, why)
		}
	}
	// From a working directory outside every root, the same: its own files
	// by their names, a walk up refused.
	if why, _ := g.r.shellWallFrom(g.r.policy.Look(), g.elsewhere, "cat open.txt made/x.txt"); why != "" {
		t.Errorf("relative paths below the working directory: %s", why)
	}
	if why, _ := g.r.shellWallFrom(g.r.policy.Look(), g.elsewhere, "cat ../keys/identity.sec"); why == "" {
		t.Error("a kept file reached by a walk up from the working directory")
	}
	if why, _ := g.r.shellWallFrom(g.r.policy.Look(), g.elsewhere, "cat ../store/x"); !strings.Contains(why, "is written in full") {
		t.Errorf("a walk up out of the working directory: %q", why)
	}
	// The whole call, not only the wall's reading of it.
	if res := shellIn(t, g.r, nil, "cd "+inQuotes(g.base)+" && cat vega/data/credentials/k.key"); !res.Refused || res.ReasonCode != ReasonShellWall || strings.Contains(res.Output, "KEPT") {
		t.Fatalf("a cd above the home and a relative path into the data directory: %+v", res)
	}
	if res := shellIn(t, g.r, g.base, "cat vega/data/credentials/k.key"); !res.Refused || res.ReasonCode != ReasonPathRefused || strings.Contains(res.Output, "KEPT") {
		t.Fatalf("a working directory above the home: %+v", res)
	}
	// $HOME and ~ stay unresolvable to the check, so they stay refused.
	for _, command := range []string{"cat ~/data/credentials/k.key", "cat $HOME/data/credentials/k.key"} {
		if why := g.r.shellRefusal(command); why == "" {
			t.Errorf("%q must stay refused", command)
		}
	}
}
