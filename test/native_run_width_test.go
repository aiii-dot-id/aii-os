package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// THE WINDOWS SUITE'S WIDTH REACHES THE GUEST AND ITS LOG (2026-11-15). native-run.sh writes the script the guest
// runs. With AII_WINDOWS_NATIVE_JOBS and AII_WINDOWS_NATIVE_P named, that script exports the queue's width and the go
// command's -p, and says in the log what took effect. With neither named, it is the script it always was. Read here
// from --scripts-only, which writes the scripts into the evidence directory, reaches nothing and exits 3, a status no
// caller takes for a run: a copy of the runner in a scratch repository, with ssh, scp and sshpass on PATH as stand-ins
// that only say they were reached. And on return, the runner counts a run at a named width only when its log says
// that width took effect, and no run that says it passed with a fail event in its log (test/native-judge.sh).

func nativeRunDry(t *testing.T, host string, env ...string) (remote, out string, code int) {
	remote, _, out, code = nativeRunDryBoth(t, host, env...)
	return remote, out, code
}

func nativeRunDryBoth(t *testing.T, host string, env ...string) (remote, launcher, out string, code int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the runner runs on the Linux and Mac hosts")
	}
	for _, tool := range []string{"sh", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	src, err := os.ReadFile("native-run.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `if [ "${1:-}" = --scripts-only ]; then scripts_only=1; shift; fi`) {
		t.Fatal("native-run.sh has no --scripts-only, so it is not run here: a run would reach a host")
	}
	tc, err := os.ReadFile("toolchain.sh")
	if err != nil {
		t.Fatal(err)
	}
	judge, err := os.ReadFile("native-judge.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo, ev, bin := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "test"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"native-run.sh": src, "toolchain.sh": tc, "native-judge.sh": judge} {
		if err := os.WriteFile(filepath.Join(repo, "test", name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reached := filepath.Join(ev, "reached")
	for _, tool := range []string{"ssh", "scp", "sshpass"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\necho \"$0 $*\" >> '"+reached+"'\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var base []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "AII_WINDOWS_NATIVE_") && !strings.HasPrefix(kv, "AII_NATIVE_") && !strings.HasPrefix(kv, "PATH=") {
			base = append(base, kv)
		}
	}
	base = append(base, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid", "GIT_CEILING_DIRECTORIES="+filepath.Dir(repo))
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "the runner"}} {
		c := exec.Command("git", args...)
		c.Dir, c.Env = repo, base
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	c := exec.Command("git", "rev-parse", "--short", "HEAD")
	c.Dir, c.Env = repo, base
	rev, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "test/native-run.sh", "--scripts-only", host, ev)
	cmd.Dir, cmd.Env = repo, append(base, env...)
	b, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if r, err := os.ReadFile(reached); err == nil {
		t.Fatalf("the dry run reached a host:\n%s", r)
	}
	raw, _ := os.ReadFile(filepath.Join(ev, "aii-native-"+strings.TrimSpace(string(rev))+".sh"))
	ps, _ := os.ReadFile(filepath.Join(ev, "aii-native-"+strings.TrimSpace(string(rev))+".ps1"))
	return string(raw), string(ps), string(b), code
}

func TestTheWindowsSuitesWidthReachesTheGuestAndItsLog(t *testing.T) {
	const plain = "export GOTOOLCHAIN=local GOFLAGS=-buildvcs=false\n"
	const says = `echo "native width jobs=${AII_NATIVE_JOBS:-default} goflags=$(go env GOFLAGS)"` + "\n"
	t.Run("unnamed, the guest's script is as it was", func(t *testing.T) {
		remote, out, code := nativeRunDry(t, "windows")
		if code != 3 || !strings.HasPrefix(remote, plain) || strings.Contains(remote, "native width") {
			t.Errorf("exit %d (%s); the script:\n%s", code, out, remote)
		}
	})
	t.Run("named, the guest runs serially and its log says so", func(t *testing.T) {
		remote, out, code := nativeRunDry(t, "windows", "AII_WINDOWS_NATIVE_JOBS=1", "AII_WINDOWS_NATIVE_P=1")
		head := "export GOTOOLCHAIN=local GOFLAGS='-buildvcs=false -p=1' AII_NATIVE_JOBS=1\n" + says
		if code != 3 || !strings.HasPrefix(remote, head) {
			t.Fatalf("exit %d (%s); the script does not begin\n%s\nbut:\n%s", code, out, head, remote)
		}
		// What the guest's shell makes of those two lines, its go stand-in answering as go env does.
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\n[ \"$1 $2\" = 'env GOFLAGS' ] && echo \"$GOFLAGS\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		sh := exec.Command("sh", "-c", head)
		sh.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin"}
		got, err := sh.CombinedOutput()
		if err != nil || string(got) != "native width jobs=1 goflags=-buildvcs=false -p=1\n" {
			t.Errorf("the guest's log would say %q (%v)", got, err)
		}
	})
	// The task's own limit: three hours at the go command's pace, six at a named width (serially the suite took
	// 2 h 11 min).
	t.Run("the task's time limit follows the width", func(t *testing.T) {
		_, plainPS, _, code := nativeRunDryBoth(t, "windows")
		_, widePS, _, code2 := nativeRunDryBoth(t, "windows", "AII_WINDOWS_NATIVE_JOBS=1", "AII_WINDOWS_NATIVE_P=1")
		if code != 3 || code2 != 3 || !strings.Contains(plainPS, "-ExecutionTimeLimit (New-TimeSpan -Hours 3)") || !strings.Contains(widePS, "-ExecutionTimeLimit (New-TimeSpan -Hours 6)") {
			t.Errorf("exit %d, %d; the launchers' limits:\n%s\n---\n%s", code, code2, plainPS, widePS)
		}
	})
	t.Run("the queue's width alone", func(t *testing.T) {
		remote, out, code := nativeRunDry(t, "windows", "AII_WINDOWS_NATIVE_JOBS=2")
		if code != 3 || !strings.HasPrefix(remote, "export GOTOOLCHAIN=local GOFLAGS='-buildvcs=false' AII_NATIVE_JOBS=2\n"+says) {
			t.Errorf("exit %d (%s); the script:\n%s", code, out, remote)
		}
	})
	for _, bad := range []string{"0", "-1", "1;x", "one", "007"} {
		t.Run("not a whole number from 1: "+bad, func(t *testing.T) {
			remote, out, code := nativeRunDry(t, "windows", "AII_WINDOWS_NATIVE_P="+bad)
			if code != 2 || remote != "" || !strings.Contains(out, "must be whole numbers from 1") {
				t.Errorf("exit %d (%s); the script:\n%s", code, out, remote)
			}
		})
	}
	t.Run("a width past 99", func(t *testing.T) {
		remote, out, code := nativeRunDry(t, "windows", "AII_WINDOWS_NATIVE_JOBS=100")
		if code != 3 || !strings.HasPrefix(remote, "export GOTOOLCHAIN=local GOFLAGS='-buildvcs=false' AII_NATIVE_JOBS=100\n") {
			t.Errorf("exit %d (%s); the script:\n%s", code, out, remote)
		}
	})
	// Another host reads neither setting, so a value that is no number does not stop it.
	t.Run("another host's script does not take the Windows width, nor judge it", func(t *testing.T) {
		remote, out, code := nativeRunDry(t, "mac", "AII_WINDOWS_NATIVE_JOBS=1", "AII_WINDOWS_NATIVE_P=bad")
		if code != 3 || !strings.HasPrefix(remote, plain) || strings.Contains(remote, "native width") {
			t.Errorf("exit %d (%s); the script:\n%s", code, out, remote)
		}
	})
}

// The runner's own words: no environment makes a run stop at its scripts, and both return paths judge the log.
func TestTheNativeRunnerJudgesWhatItBroughtBack(t *testing.T) {
	src, err := os.ReadFile("native-run.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		`if [ "${1:-}" = --scripts-only ]; then scripts_only=1; shift; fi`,
		`. "$(dirname "$0")/native-judge.sh"`,
		`if ! native_width_ok "$host" "$log" "$wjobs" "$wp"; then`,
		`if [ -n "$scripts_only" ]; then echo "NATIVE_SCRIPTS_ONLY remote=$remote launcher=$launcher"; exit 3; fi`,
		`if ! native_refusal_ok "$log" "$rc"; then`,
		`rc=$(native_exit_of "$log") || {`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("native-run.sh no longer holds %q", want)
		}
	}
	// Both return paths end in skips, whose report refuses a run that said it passed with a fail event in its log.
	if !strings.Contains(s, `-whole -no-fail "$log"`) || strings.Count(s, "skips; record") != 2 {
		t.Error("native-run.sh's return paths do not both end in the report that counts fail events")
	}
	if strings.Contains(s, "AII_NATIVE_DRY_RUN") || strings.Count(s, "scripts_only=1") != 1 {
		t.Error("something other than the --scripts-only argument can make a run stop at its scripts")
	}
}

// What the runner asks of the log it brought back, on fixture logs (test/native-judge.sh).
func TestTheWindowsWidthIsReadBackFromItsLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the runner runs on the Linux and Mac hosts")
	}
	judge := func(t *testing.T, fn, log string, args ...string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "native.log")
		if err := os.WriteFile(p, []byte(log), 0o644); err != nil {
			t.Fatal(err)
		}
		lib, err := filepath.Abs("native-judge.sh")
		if err != nil {
			t.Fatal(err)
		}
		script := ". '" + lib + "'; " + fn + " \"$@\"; echo \" rc=$?\""
		for i, a := range args {
			if a == "LOG" {
				args[i] = p
			}
		}
		out, err := exec.Command("sh", append([]string{"-c", script, "sh"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	const serial = "native width jobs=1 goflags=-buildvcs=false -p=1\r\n"
	for _, c := range []struct {
		name, log, host, jobs, p, want string
	}{
		{"the width named, said once", "native session=1 interactive=True\n" + serial, "windows", "1", "1", "rc=0"},
		{"no width line", "native session=1 interactive=True\n", "windows", "1", "1", "rc=1"},
		{"the line twice", serial + serial, "windows", "1", "1", "rc=1"},
		{"another queue width", "native width jobs=8 goflags=-buildvcs=false -p=1\n", "windows", "1", "1", "rc=1"},
		{"no -p in the flags", "native width jobs=1 goflags=-buildvcs=false\n", "windows", "1", "1", "rc=1"},
		{"the flags empty, go env having failed", "native width jobs=1 goflags=\n", "windows", "1", "1", "rc=1"},
		{"-p=10 is not -p=1", "native width jobs=1 goflags=-buildvcs=false -p=10\n", "windows", "1", "1", "rc=1"},
		{"no width named", "native session=1 interactive=True\n", "windows", "", "", "rc=0"},
		{"another host", "", "linux", "1", "1", "rc=0"},
		{"a second -p after the first", "native width jobs=1 goflags=-buildvcs=false -p=1 -p=8\n", "windows", "1", "1", "rc=1"},
		{"a flag more", "native width jobs=1 goflags=-buildvcs=false -p=1 -race\n", "windows", "1", "1", "rc=1"},
		{"only -p named, the guest's own queue width", "native width jobs=4 goflags=-buildvcs=false -p=1\n", "windows", "", "1", "rc=0"},
		{"only -p named, the guest's default width", "native width jobs=default goflags=-buildvcs=false -p=1\n", "windows", "", "1", "rc=0"},
		{"only -p named, a queue width that is no number", "native width jobs=x8 goflags=-buildvcs=false -p=1\n", "windows", "", "1", "rc=1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := judge(t, "native_width_ok", c.log, c.host, "LOG", c.jobs, c.p); got != c.want {
				t.Errorf("native_width_ok said %q, want %q", got, c.want)
			}
		})
	}
}

// A REFUSAL IS NEVER A PASS: a log that says the run did not start, with an exit that says it
// passed, is refused; with its own status, or with no refusal in it, it is the run it says.
func TestARefusalWithAPassingExitIsNoPass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the runner runs on the Linux and Mac hosts")
	}
	lib, err := filepath.Abs("native-judge.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, log, rc, want string }{
		{"a refusal, exit 0", "NATIVE_NOT_STARTED 3 GiB free\nNATIVE_EXIT=0\n", "0", "1"},
		{"a refusal, its own status", "NATIVE_NOT_STARTED 3 GiB free\nNATIVE_EXIT=2\n", "2", "0"},
		{"no refusal, exit 0", "native session=1 interactive=True\nNATIVE_EXIT=0\n", "0", "0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "native.log")
			if err := os.WriteFile(p, []byte(c.log), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("sh", "-c", ". '"+lib+"'; native_refusal_ok \"$1\" \"$2\"; echo $?", "sh", p, c.rc).CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != c.want {
				t.Errorf("native_refusal_ok said %q (%v), want %s", out, err, c.want)
			}
		})
	}
}

// THE SUITE'S STATUS IS ONE CANONICAL STATUS: exactly one NATIVE_EXIT line, 0 to 255 written as
// a shell writes $?. A shell reads 00 and 256 as 0, so either one would turn a refusal or a failure into a pass.
func TestTheSuitesStatusIsOneCanonicalStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the runner runs on the Linux and Mac hosts")
	}
	lib, err := filepath.Abs("native-judge.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, log, want string }{
		{"0", "native session=1 interactive=True\nNATIVE_EXIT=0\n", "0 rc=0"},
		{"1, with a carriage return", "NATIVE_EXIT=1\r\n", "1 rc=0"},
		{"255", "NATIVE_EXIT=255\n", "255 rc=0"},
		{"00", "NATIVE_NOT_STARTED 3 GiB free\nNATIVE_EXIT=00\n", "rc=1"},
		{"256", "NATIVE_EXIT=256\n", "rc=1"},
		{"-1", "NATIVE_EXIT=-1\n", "rc=1"},
		{"empty", "NATIVE_EXIT=\n", "rc=1"},
		{"two lines", "NATIVE_EXIT=1\nNATIVE_EXIT=0\n", "rc=1"},
		{"none", "native session=1 interactive=True\n", "rc=1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "native.log")
			if err := os.WriteFile(p, []byte(c.log), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("sh", "-c", ". '"+lib+"'; s=$(native_exit_of \"$1\"); r=$?; echo $s rc=$r", "sh", p).CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != c.want {
				t.Errorf("native_exit_of said %q (%v), want %q", out, err, c.want)
			}
		})
	}
}
