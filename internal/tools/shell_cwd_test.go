//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cwdRig is a home with a tree in it, a directory outside it, and one
// link inside the home that leads out.
func cwdRig(t *testing.T) (r *Registry, home, outside string) {
	t.Helper()
	real := func(p string) string {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return resolved
		}
		return p
	}
	home, outside = real(t.TempDir()), real(t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(home, "notes.txt"), []byte("home note\n"), 0o644))
	must(os.MkdirAll(filepath.Join(home, "experiments", "run1"), 0o755))
	must(os.WriteFile(filepath.Join(home, "experiments", "exp.txt"), []byte("exp\n"), 0o644))
	must(os.MkdirAll(filepath.Join(home, "sub"), 0o755))
	// The same name under the home is an ordinary directory.
	must(os.MkdirAll(filepath.Join(home, "link"), 0o755))
	// A name the floor keeps.
	must(os.WriteFile(filepath.Join(home, "providers.json"), []byte("KEPT\n"), 0o600))
	must(os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("OUTSIDE\n"), 0o644))
	if err := os.Symlink(outside, filepath.Join(home, "sub", "link")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	return NewRegistry(home, nil, Timeouts{}), home, outside
}

func shellIn(t *testing.T, r *Registry, cwd any, command string) Result {
	t.Helper()
	args := map[string]any{"command": command}
	if cwd != nil {
		args["cwd"] = cwd
	}
	res, err := r.Execute(context.Background(), "shell", args)
	if err != nil {
		t.Fatalf("%q in %v: %v", command, cwd, err)
	}
	return res
}

func TestTheShellRunsInTheDirectoryTheCallNames(t *testing.T) {
	r, home, outside := cwdRig(t)
	for _, c := range []struct {
		name    string
		cwd     any
		command string
		want    string
	}{
		{"no cwd is the home", nil, "pwd", home + "\n"},
		{"a directory in the home", "experiments", "pwd", filepath.Join(home, "experiments") + "\n"},
		{"the same, written in full", filepath.Join(home, "experiments"), "pwd", filepath.Join(home, "experiments") + "\n"},
		{"a file beside it", "experiments", "cat exp.txt", "exp\n"},
		{"up into the home, which a cd had refused", "experiments", "cat ../notes.txt", "home note\n"},
		{"two up into the home", "experiments/run1", "cat ../../notes.txt", "home note\n"},
		{"an absolute path from there", "experiments", `cat "` + filepath.Join(home, "notes.txt") + `"`, "home note\n"},
		{"HOME is still the home", "experiments", "env | grep ^HOME=", "HOME=" + home + "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := shellIn(t, r, c.cwd, c.command)
			if res.Failed() || res.Refused || res.Output != c.want {
				t.Fatalf("got %+v, want output %q", res, c.want)
			}
		})
	}

	for _, c := range []struct {
		name    string
		cwd     any
		command string
		code    string
		says    string
	}{
		{"out of the home from the working directory", "experiments", "touch ran; cat ../../outside.txt", ReasonShellWall, "../../outside.txt is outside the sandbox"},
		{"through the link that leads out, which a cd had admitted", "sub", "touch ran; cat link/secret.txt", ReasonShellWall, "link/secret.txt is outside the sandbox"},
		{"a protected name is protected from there too", "experiments", "touch ran; cat providers.json", ReasonShellWall, "protected name"},
		{"and so is one a glob from there expands to", "experiments", "touch ran; cat ../provi*.json", ReasonShellWall, "protected name"},
		{"a working directory above the home", "..", "touch ran", ReasonPathRefused, "cwd"},
		{"a working directory outside", outside, "touch ran", ReasonPathRefused, "cwd"},
		{"a working directory through the link out", "sub/link", "touch ran", ReasonPathRefused, "cwd"},
		{"a file is no working directory", "notes.txt", "touch ran", ReasonPathRefused, "is not a directory"},
		{"nor is a name that is not there", "nowhere", "touch ran", ReasonPathRefused, "is not a directory"},
		{"nor a number", 42, "touch ran", ReasonArgumentsRequired, "cwd must be a string"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := shellIn(t, r, c.cwd, c.command)
			if !res.Refused || res.ReasonCode != c.code || !strings.Contains(res.Error, c.says) {
				t.Fatalf("got %+v, want a refusal %s saying %q", res, c.code, c.says)
			}
		})
	}
	// A refusal runs nothing, wherever it would have run.
	for _, dir := range []string{home, filepath.Join(home, "experiments"), filepath.Join(home, "sub"), outside} {
		if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
			t.Fatalf("a refused command ran in %s", dir)
		}
	}
}

// EQUIVALENT SPELLINGS AGREE FROM THE WORKING DIRECTORY. What the wall
// answers for a path written from the call's directory is what it
// answers for the same file written in full: admitted both ways, or
// refused both ways.
func TestBothSpellingsAgreeFromTheWorkingDirectory(t *testing.T) {
	r, home, outside := cwdRig(t)
	base := filepath.Join(home, "experiments")
	for _, c := range []struct{ relative, full string }{
		{"exp.txt", filepath.Join(home, "experiments", "exp.txt")},
		{"../notes.txt", filepath.Join(home, "notes.txt")},
		{"run1/../exp.txt", filepath.Join(home, "experiments", "exp.txt")},
		{"../sub/link/secret.txt", filepath.Join(outside, "secret.txt")},
		{"../../elsewhere.txt", filepath.Join(filepath.Dir(home), "elsewhere.txt")},
		{"../providers.json", filepath.Join(home, "providers.json")},
	} {
		rel, _ := r.shellWallFrom(r.policy, base, "cat "+c.relative)
		full, _ := r.shellWallFrom(r.policy, base, `cat "`+c.full+`"`) // quoted: a temporary directory's path may hold a space
		if (rel == "") != (full == "") {
			t.Errorf("%s from the working directory: %q; the same file in full: %q", c.relative, rel, full)
		}
	}
}

func TestARefusalAfterACdNamesTheWorkingDirectory(t *testing.T) {
	r, home, _ := cwdRig(t)
	res := shellIn(t, r, nil, "cd experiments && cat ../notes.txt")
	if !res.Refused || res.ReasonCode != ReasonShellWall {
		t.Fatalf("a cd is not followed: %+v", res)
	}
	for _, want := range []string{"../notes.txt is outside the sandbox", "relative paths are read from " + home, "cwd="} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("the refusal must say %q:\n%s", want, res.Error)
		}
	}
	// What it says to do, done.
	if res := shellIn(t, r, "experiments", "cat ../notes.txt"); res.Refused || res.Output != "home note\n" {
		t.Fatalf("the way the refusal names does not work: %+v", res)
	}
	// No cd, no such line; and an absolute path is not a matter of where
	// it is read from.
	for _, command := range []string{"cat ../../etc/x", "cd experiments && cat /etc/hosts"} {
		if res := shellIn(t, r, nil, command); !res.Refused || strings.Contains(res.Error, "relative paths are read from") {
			t.Errorf("%q: %+v", command, res)
		}
	}
}

func TestARefusedSlashThatIsNoPathNamesTheWayOn(t *testing.T) {
	r, _, _ := cwdRig(t)
	const line = "if that is text and not a path"
	for _, c := range []struct {
		command string
		hinted  bool
	}{
		{`python3 -c "print(10 // 3)"`, true},
		{`python3 -c "print(3 / 4)"`, true},
		{`echo $((10 / 2))`, true},
		{`echo '{"path": "/v1/models"}'`, true},
		{`python3 -c "print('/etc/hosts')"`, true},
		// A word of the command itself reads as the path it is.
		{`cat /etc/hosts`, false},
		{`ls /`, true}, // nothing but a slash: the line is offered, and the refusal stands
		// A search has its own line, and only that one: for a pattern that
		// is one word, and for one that stands inside a longer pattern.
		{`grep -n "/etc/hosts" notes.txt`, false},
		{`grep -rn "see /etc/hosts for" notes.txt`, false},
	} {
		res := shellIn(t, r, nil, c.command)
		if !res.Refused || res.ReasonCode != ReasonShellWall || !strings.Contains(res.Error, "is outside the sandbox") {
			t.Errorf("%q must still be refused by the wall: %+v", c.command, res)
			continue
		}
		if got := strings.Contains(res.Error, line); got != c.hinted {
			t.Errorf("%q: the line is offered=%v, want %v:\n%s", c.command, got, c.hinted, res.Error)
		}
	}
	for _, search := range []string{`grep -n "/etc/hosts" notes.txt`, `grep -rn "see /etc/hosts for" notes.txt`} {
		if res := shellIn(t, r, nil, search); !strings.Contains(res.Error, "the grep tool takes any pattern") {
			t.Errorf("%q: the search's own line is gone: %s", search, res.Error)
		}
	}
	// And the way it names is one: the same arithmetic, put in a file with
	// the write tool and named in the command, runs.
	if res, err := r.Execute(context.Background(), "write", map[string]any{"file_path": "calc.sh", "content": "echo $((10 / 2))\n"}); err != nil || res.Failed() {
		t.Fatalf("writing the program: %+v %v", res, err)
	}
	if res := shellIn(t, r, nil, "sh calc.sh"); res.Refused || res.Output != "5\n" {
		t.Fatalf("the program in a file: %+v", res)
	}
}

// A WORKING DIRECTORY IN A GRANTED ROOT reads its names as the full path
// does. A project the operator granted may hold a config.json of its own:
// written in full it is the project's file, and the home's name rule does
// not reach it. Read from a working directory inside the grant, the bare
// name is the same file and gets the same answer; read from the home, it
// is the home's kept name, as before.
func TestAWorkingDirectoryInAGrantedRootReadsNamesAsTheFullPathDoes(t *testing.T) {
	r, _, _ := cwdRig(t)
	project := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(project); err == nil {
		project = resolved
	}
	if err := os.WriteFile(filepath.Join(project, "config.json"), []byte("{\"project\": true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.SetExtraRoots([]string{project})

	full := shellIn(t, r, nil, `cat "`+filepath.Join(project, "config.json")+`"`)
	from := shellIn(t, r, project, "cat config.json")
	if full.Refused || from.Refused || full.Output != from.Output || from.Output != "{\"project\": true}\n" {
		t.Fatalf("in full: %+v\nfrom the working directory: %+v", full, from)
	}
	if home := shellIn(t, r, nil, "cat config.json"); !home.Refused || !strings.Contains(home.Error, "protected name") {
		t.Fatalf("the bare name read from the home is the home's kept name: %+v", home)
	}
}
