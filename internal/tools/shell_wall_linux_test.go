//go:build linux && !android

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// linuxWall is a slot laid out as a stock one is (the home is the slot: its
// data directory, its configuration's directory and its logs lie inside
// it), a sibling slot beside it, a granted root, a temporary directory and a
// directory that is none of them, and a Registry whose shell is walled from
// the slot's kept places and the sibling.
type linuxWall struct {
	reg     *Registry
	home    string
	sibling string
	root    string
	outside string
	temp    string
}

// kept names the slot's kept files, by what they are.
func (f linuxWall) key() string       { return filepath.Join(f.home, "data", "identity.sec") }
func (f linuxWall) config() string    { return filepath.Join(f.home, "config", "config.json") }
func (f linuxWall) providers() string { return filepath.Join(f.home, "config", "providers.json") }
func (f linuxWall) vaultKey() string  { return filepath.Join(f.home, "vault", "a.key") }
func (f linuxWall) log() string       { return filepath.Join(f.home, "log", "aii.log") }
func (f linuxWall) siblingKey() string {
	return filepath.Join(f.sibling, "data", "identity.sec")
}

func newLinuxWall(t *testing.T) linuxWall {
	t.Helper()
	requireShellWall(t)
	base := t.TempDir()
	home := filepath.Join(base, "slot")
	data := filepath.Join(home, "data")
	logs := filepath.Join(home, "log")
	f := linuxWall{home: home, sibling: filepath.Join(base, "sibling"), root: filepath.Join(base, "granted"),
		outside: filepath.Join(base, "outside"), temp: filepath.Join(base, "temp")}
	for path, body := range map[string]string{
		f.key(): "KEPT SECRET\n",
		filepath.Join(data, "credentials", "a.json"): "KEPT SECRET\n",
		f.config():             "KEPT SECRET\n",
		f.config() + ".before": "KEPT SECRET\n",
		f.providers():          "KEPT SECRET\n",
		filepath.Join(home, "config", "README.md"): "the configuration's notes\n",
		f.vaultKey():                            "KEPT SECRET\n",
		filepath.Join(home, "vault", "b.key"):   "KEPT SECRET\n",
		f.siblingKey():                          "KEPT SECRET\n",
		filepath.Join(data, "ui", "frame.html"): "OPEN FRAME\n",
		f.log():                                 "a line of the log\n",
		filepath.Join(logs, "control.json"):     "{}\n",
		filepath.Join(home, "notes.md"):         "a note of the home\n",
		filepath.Join(f.root, "work.txt"):       "granted work\n",
		filepath.Join(f.outside, "open.txt"):    "OUTSIDE OK\n",
		filepath.Join(f.temp, ".keep"):          "",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{
		{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data, Except: []string{filepath.Join(data, "ui")}, Reason: "kept", Enforced: true},
		{ID: "sub.config", Kind: firewall.KindSubstrate, Path: f.config(), Reason: "kept", Enforced: true},
		{ID: "sub.config", Kind: firewall.KindSubstrate, Path: f.config() + ".", Stem: true, Reason: "kept", Enforced: true},
		{ID: "sub.providers", Kind: firewall.KindSubstrate, Path: f.providers(), Reason: "kept", Enforced: true},
		{ID: "sub.vault", Kind: firewall.KindSubstrate, Path: f.vaultKey(), Reason: "kept", Enforced: true},
		{ID: "sub.vault", Kind: firewall.KindSubstrate, Path: filepath.Join(home, "vault", "b.key"), Reason: "kept", Enforced: true},
		{ID: "sub.logs", Kind: firewall.KindSubstrate, Path: logs, Except: []string{filepath.Join(logs, "control.json")}, ReadOpen: true, Reason: "kept", Enforced: true},
	})
	f.reg = NewRegistry(home, policy, Timeouts{ShellSeconds: 30})
	f.reg.SetExtraRoots([]string{f.root})
	// The places the boundary alone keeps: the sibling slot, and the
	// process table. Asked at each call, as the app wires them.
	f.reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		return []*firewall.Rule{
			{ID: "sub.identity", Kind: firewall.KindSubstrate, Path: f.sibling, Shown: "other identities' slots", Reason: "kept", Enforced: true},
			{ID: "sub.processes", Kind: firewall.KindSubstrate, Path: "/proc", Shown: "/proc", Linkless: true, Reason: "kept", Enforced: true},
		}, nil
	})
	host := shellWallTemp
	shellWallTemp = []string{f.temp}
	t.Cleanup(func() { shellWallTemp = host })
	t.Cleanup(func() { f.reg.EndLeftJobs(0) })
	return f
}

func requireShellWall(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("run as root the shell is not walled; the wall's tests run as an ordinary account")
	}
	for _, program := range []string{"bwrap", "nsenter"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Fatalf("%s is not on this host, and the shell's wall needs it: install it to run the wall's tests (%v)", program, err)
		}
	}
}

// walled runs a plain command through the shell tool exactly as a call the
// text check let through runs: ShellTool.Execute, with the wall the
// registry hands it at each call (Registry.shellWallData), in the home.
// This is the seam the wall is proved at: the text check is the
// registry's, in front of the tool (Registry.Execute), and is left out
// here, so that what the command meets is the wall alone.
func (f linuxWall) walled(t *testing.T, cmd string) Result {
	t.Helper()
	res, err := f.reg.shell.Execute(context.Background(), map[string]any{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused {
		t.Fatalf("%q: the shell was refused (%q), so nothing was asked of the wall", cmd, res.Error)
	}
	return res
}

func (f linuxWall) unwalled(t *testing.T, cmd string) Result {
	t.Helper()
	prev := shellAccountRoot
	shellAccountRoot = func() bool { return true }
	defer func() { shellAccountRoot = prev }()
	res, err := f.reg.shell.Execute(context.Background(), map[string]any{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// succeeded is a run that exited 0 and, when want is given, printed it.
func succeeded(res Result, want string) bool {
	return !res.Refused && res.ExitStatus != nil && *res.ExitStatus == 0 && strings.Contains(res.Output, want)
}

// refusedBy asserts a walled run failed with the system's own error and
// printed nothing kept, after its control succeeded unwalled.
func (f linuxWall) refusedBy(t *testing.T, cmd, control, systemSays string) Result {
	t.Helper()
	if res := f.unwalled(t, cmd); !succeeded(res, control) {
		t.Fatalf("control: %q without the wall must succeed and print %q, or its refusal behind the wall proves nothing: %+v", cmd, control, res)
	}
	res := f.walled(t, cmd)
	if strings.Contains(res.Output, "KEPT SECRET") {
		t.Fatalf("%q: a kept secret was read behind the wall:\n%s", cmd, res.Output)
	}
	if res.ExitStatus == nil || *res.ExitStatus == 0 || !strings.Contains(res.Output, systemSays) {
		t.Fatalf("%q: behind the wall it must fail with %q: %+v", cmd, systemSays, res)
	}
	return res
}

// allowed is a command behind the wall that must succeed.
func (f linuxWall) allowed(t *testing.T, cmd, want string) Result {
	t.Helper()
	res := f.walled(t, cmd)
	if !succeeded(res, want) {
		t.Fatalf("%q must succeed behind the wall and print %q: %+v", cmd, want, res)
	}
	return res
}

// q quotes a path for the shell.
func q(p string) string { return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'" }

// A KEPT FILE IS NOT THERE TO READ. A kept directory (the data directory, a
// directory whose every entry is kept) is an empty file system behind the
// wall, so what was in it is not found; a kept file beside files that are
// not kept (the configuration's directory here) is the null device, which
// refuses to open.
func TestOnLinuxTheWallRefusesTheKeptFilesAPlainCommandNames(t *testing.T) {
	f := newLinuxWall(t)
	f.refusedBy(t, "cat "+q(f.key()), "KEPT SECRET", "No such file or directory")
	f.refusedBy(t, "cat "+q(filepath.Join(f.home, "data", "credentials", "a.json")), "KEPT SECRET", "No such file or directory")
	f.refusedBy(t, "cat "+q(f.vaultKey()), "KEPT SECRET", "No such file or directory")
	f.refusedBy(t, "cat "+q(f.config()), "KEPT SECRET", "Permission denied")
	f.refusedBy(t, "cat "+q(f.config()+".before"), "KEPT SECRET", "Permission denied")
	f.refusedBy(t, "cat "+q(f.providers()), "KEPT SECRET", "Permission denied")
	// And what is beside them and not kept is read.
	f.allowed(t, "cat "+q(filepath.Join(f.home, "config", "README.md")), "the configuration's notes")
}

// ANOTHER IDENTITY'S HOME IS EMPTY BEHIND THE WALL: listed, it holds
// nothing, and what is in it is not found.
func TestOnLinuxTheWallHidesAnotherIdentitysHome(t *testing.T) {
	f := newLinuxWall(t)
	f.refusedBy(t, "ls "+q(filepath.Join(f.sibling, "data")), "identity.sec", "No such file or directory")
	f.refusedBy(t, "cat "+q(f.siblingKey()), "KEPT SECRET", "No such file or directory")
	if res := f.unwalled(t, "ls -A "+q(f.sibling)); !succeeded(res, "data") {
		t.Fatalf("control: without the wall the sibling's home lists its data directory: %+v", res)
	}
	if res := f.allowed(t, "ls -A "+q(f.sibling), ""); strings.TrimSpace(res.Output) != "" {
		t.Fatalf("behind the wall the sibling's home lists something:\n%s", res.Output)
	}
}

func TestOnLinuxTheWallRefusesAWriteOutsideTheHomeAndItsGrants(t *testing.T) {
	f := newLinuxWall(t)
	made := filepath.Join(f.outside, "made.txt")
	cmd := "touch " + q(made)
	if res := f.unwalled(t, cmd); !succeeded(res, "") {
		t.Fatalf("control: without the wall the write outside succeeds: %+v", res)
	}
	if err := os.Remove(made); err != nil {
		t.Fatal(err)
	}
	if res := f.walled(t, cmd); res.ExitStatus == nil || *res.ExitStatus == 0 || !strings.Contains(res.Output, "Read-only file system") {
		t.Fatalf("behind the wall a write outside must fail with the read-only file system: %+v", res)
	}
	if _, err := os.Stat(made); err == nil {
		t.Fatal("a file the shell made outside its home is on the host")
	}

	before, err := os.ReadFile(f.log())
	if err != nil {
		t.Fatal(err)
	}
	logCmd := "echo forged >> " + q(f.log())
	if res := f.unwalled(t, logCmd); !succeeded(res, "") {
		t.Fatalf("control: without the wall the log is written: %+v", res)
	}
	if err := os.WriteFile(f.log(), before, 0o600); err != nil {
		t.Fatal(err)
	}
	if res := f.walled(t, logCmd); res.ExitStatus == nil || *res.ExitStatus == 0 || !strings.Contains(res.Output, "Read-only file system") {
		t.Fatalf("behind the wall a write to the log must fail with the read-only file system: %+v", res)
	}
	if after, _ := os.ReadFile(f.log()); string(after) != string(before) {
		t.Fatalf("the log was written from behind the wall: %q", after)
	}
}

// WHAT THE WALL LEAVES OPEN WORKS BEHIND IT: the home, a granted root and
// the temporary directory are read and written; the dashboard surface
// inside the data directory and the log's switch are written; the log is
// read; the system's programs run; the null device takes a write. And
// through the whole tool, the text check in front, an everyday command
// runs behind the wall.
func TestOnLinuxBehindTheWallTheHomeTheGrantsTheTempAndTheSystemWork(t *testing.T) {
	f := newLinuxWall(t)
	f.allowed(t, "cat notes.md", "a note of the home")
	f.allowed(t, "echo hi > made.txt && cat made.txt", "hi")
	f.allowed(t, "echo w > "+q(filepath.Join(f.root, "made.txt"))+" && cat "+q(filepath.Join(f.root, "work.txt")), "granted work")
	f.allowed(t, "mktemp -p "+q(f.temp), f.temp)
	f.allowed(t, "echo NEW > data/ui/frame.html && cat data/ui/frame.html", "NEW")
	f.allowed(t, `echo '{"x":1}' > log/control.json && cat log/control.json`, `{"x":1}`)
	f.allowed(t, "cat log/aii.log", "a line of the log")
	f.allowed(t, "cat "+q(filepath.Join(f.outside, "open.txt")), "OUTSIDE OK")
	f.allowed(t, "echo gone > /dev/null && git --version && ls /usr/bin", "git version")
	if res := modelCall(t, f.reg, "shell", map[string]any{"command": "echo through the tool > via.txt && cat via.txt"}); !succeeded(res, "through the tool") {
		t.Fatalf("an everyday command through the whole tool did not run behind the wall: %+v", res)
	}
	for _, p := range []string{filepath.Join(f.home, "made.txt"), filepath.Join(f.root, "made.txt"), filepath.Join(f.home, "via.txt")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a file the shell wrote behind the wall is not on the host: %v", err)
		}
	}
}

// THE WALL CANNOT BE TAKEN DOWN FROM INSIDE IT. The command holds no
// capability, so the kernel refuses to unmount a mask (umount2, asked
// directly, so the answer is the kernel's and not the umount program's own
// check of who runs it), and after every attempt to undo it, by the system's
// own programs too, the kept file is still not found. (No mask exists
// without the wall, so the control of these is the read: without the wall
// the same command reads the key, so a mask that came down would show it.)
func TestOnLinuxTheWallCannotBeUndoneFromInsideIt(t *testing.T) {
	f := newLinuxWall(t)
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is not on this host; the test asks the kernel through it: %v", err)
	}
	f.allowed(t, "grep -E '^Cap(Eff|Prm):' /proc/self/status", "CapPrm:\t0000000000000000\nCapEff:\t0000000000000000")
	read := "cat " + q(f.key())
	if res := f.unwalled(t, read); !succeeded(res, "KEPT SECRET") {
		t.Fatalf("control: without the wall the key is read: %+v", res)
	}
	data := filepath.Join(f.home, "data")
	umount2 := `python3 -c 'import ctypes, os, sys; libc = ctypes.CDLL(None, use_errno=True); r = libc.umount2(sys.argv[1].encode(), 0); print("unmounted" if r == 0 else os.strerror(ctypes.get_errno()))' ` + q(data)
	if res := f.walled(t, umount2); !strings.Contains(res.Output, "Operation not permitted") {
		t.Fatalf("the kernel did not refuse to unmount the mask over the data directory: %+v", res)
	}
	for _, undo := range []string{umount2, "umount " + q(data), "umount -l " + q(data), "mount -o remount,rw /", "mount -t tmpfs none " + q(data)} {
		res := f.walled(t, undo+"; "+read)
		if strings.Contains(res.Output, "KEPT SECRET") || strings.Contains(res.Output, "unmounted") {
			t.Fatalf("%q took the wall down:\n%s", undo, res.Output)
		}
		if !strings.Contains(res.Output, "No such file or directory") {
			t.Fatalf("after %q the key is no longer hidden as it was:\n%s", undo, res.Output)
		}
	}
}

func TestOnLinuxTheWallKeepsTheProcessTableItsOwn(t *testing.T) {
	f := newLinuxWall(t)
	self := strconv.Itoa(os.Getpid())
	f.refusedBy(t, "kill -0 "+self+" && echo signalled", "signalled", "No such process")
	f.refusedBy(t, "cat /proc/"+self+"/cmdline", filepath.Base(os.Args[0]), "No such file or directory")
	f.refusedBy(t, "cat /proc/self/root"+q(f.key()), "KEPT SECRET", "No such file or directory")
	if res := f.walled(t, "cat /proc/1/root"+q(f.key())); strings.Contains(res.Output, "KEPT SECRET") || res.ExitStatus == nil || *res.ExitStatus == 0 {
		t.Fatalf("the key was read through the sandbox's first process: %+v", res)
	}
	res := f.allowed(t, "ps -e -o pid=,comm=", "ps")
	if strings.Count(strings.TrimSpace(res.Output), "\n") > 8 {
		t.Fatalf("the shell sees processes not its own:\n%s", res.Output)
	}
}

func TestOnLinuxTheWallsHolderCarriesNoneOfTheHostsEnvironment(t *testing.T) {
	t.Setenv("AII_WALL_SENTINEL", "KEPT SECRET")
	f := newLinuxWall(t)
	res := f.allowed(t, `for p in /proc/[0-9]*; do tr '\0' '\n' < "$p/environ"; done; echo read`, "read")
	if strings.Contains(res.Output, "KEPT SECRET") || !strings.Contains(res.Output, "PATH=") {
		t.Fatalf("the sandbox's processes' environments are not the scrubbed ones:\n%s", res.Output)
	}
}

func TestOnLinuxWithTheSandboxOffTheKeptPlacesAreStillRefused(t *testing.T) {
	f := newLinuxWall(t)
	f.reg.SetSandboxOff(true)
	if !f.reg.SandboxOff() {
		t.Fatal("the sandbox did not turn off")
	}
	f.allowed(t, "echo off > "+q(filepath.Join(f.outside, "made.txt"))+" && cat "+q(filepath.Join(f.outside, "made.txt")), "off")
	f.refusedBy(t, "cat "+q(f.key()), "KEPT SECRET", "No such file or directory")
	f.refusedBy(t, "cat "+q(f.config()), "KEPT SECRET", "Permission denied")
	f.refusedBy(t, "cat "+q(f.siblingKey()), "KEPT SECRET", "No such file or directory")
}

func TestOnLinuxAJobThatLeftItsGroupEndsWithTheIdentity(t *testing.T) {
	f := newLinuxWall(t)
	res := f.allowed(t, `setsid sleep 300 >/dev/null 2>&1 < /dev/null & sleep 0.2; pgrep -x sleep`, "")
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(res.Output), &pid); err != nil {
		t.Fatalf("the job's number: %q", res.Output)
	}
	host, alive := shellProcess(t, f.reg.shell, pid)
	if !alive {
		t.Fatal("rig: the job must be running before the identity ends")
	}
	f.allowed(t, "pgrep -x sleep", strconv.Itoa(pid))
	if _, err := f.reg.EndLeftJobs(time.Second); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); processLives(host); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("a job that left its group (%d here) outlived the identity's ending", host)
		}
	}
}

func TestOnLinuxTheWallHoldsWhatTheNextCallMust(t *testing.T) {
	f := newLinuxWall(t)
	later := filepath.Join(filepath.Dir(f.home), "later")
	if err := os.Mkdir(later, 0o700); err != nil {
		t.Fatal(err)
	}
	write := "touch " + q(filepath.Join(later, "made.txt"))
	if res := f.walled(t, write); !strings.Contains(res.Output, "Read-only file system") {
		t.Fatalf("a root not granted yet is written: %+v", res)
	}
	f.reg.SetExtraRoots([]string{f.root, later})
	f.allowed(t, write, "")
	f.reg.SetExtraRoots([]string{f.root})
	if res := f.walled(t, write); !strings.Contains(res.Output, "Read-only file system") {
		t.Fatalf("a root taken back is still written: %+v", res)
	}

	if err := os.WriteFile(f.config()+".tmp", []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.config()+".tmp", f.config()); err != nil {
		t.Fatal(err)
	}
	f.refusedBy(t, "cat "+q(f.config()), "KEPT SECRET", "Permission denied")

	if err := os.WriteFile(f.vaultKey()+".tmp", []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.vaultKey()+".tmp", f.vaultKey()); err != nil {
		t.Fatal(err)
	}
	f.refusedBy(t, "cat "+q(f.vaultKey()), "KEPT SECRET", "No such file or directory")

	// An identity born beside it after the sandbox was built.
	born := filepath.Join(filepath.Dir(f.home), "born")
	if err := os.MkdirAll(filepath.Join(born, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(born, "data", "identity.sec"), []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Read before it is kept: the control.
	f.allowed(t, "cat "+q(filepath.Join(born, "data", "identity.sec")), "KEPT SECRET")
	f.reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		return []*firewall.Rule{
			{ID: "sub.identity", Kind: firewall.KindSubstrate, Path: f.sibling, Reason: "kept", Enforced: true},
			{ID: "sub.identity", Kind: firewall.KindSubstrate, Path: born, Reason: "kept", Enforced: true},
		}, nil
	})
	f.refusedBy(t, "cat "+q(filepath.Join(born, "data", "identity.sec")), "KEPT SECRET", "No such file or directory")
}

// The text check is the fast refusal in front: through the whole tool, a
// command that names a kept place is refused by it, with the place named,
// before any wall is asked.
func TestOnLinuxTheTextCheckStillRefusesFirst(t *testing.T) {
	f := newLinuxWall(t)
	res := modelCall(t, f.reg, "shell", map[string]any{"command": "cat data/identity.sec"})
	if !res.Refused || res.ReasonCode == ReasonShellWallUnavailable {
		t.Fatalf("a command naming the key is not refused by the text check: %+v", res)
	}
}

func TestOnLinuxWithoutBubblewrapTheShellIsRefusedAndNothingRuns(t *testing.T) {
	f := newLinuxWall(t)
	marker := filepath.Join(f.home, "ran.txt")
	cmd := `echo ran > ran.txt`
	f.allowed(t, cmd, "")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("control: the command did not run with bubblewrap where it is")
	}
	os.Remove(marker)
	// A PATH with nsenter on it and no bubblewrap.
	bin := t.TempDir()
	nsenter, _ := exec.LookPath("nsenter")
	bash, _ := exec.LookPath("bash")
	for _, p := range []string{nsenter, bash} {
		if err := os.Symlink(p, filepath.Join(bin, filepath.Base(p))); err != nil {
			t.Fatal(err)
		}
	}
	f.reg.EndLeftJobs(0) // a sandbox already built is not asked for bubblewrap again
	t.Setenv("PATH", bin)
	res := modelCall(t, f.reg, "shell", map[string]any{"command": cmd})
	if !res.Refused || res.ReasonCode != ReasonShellWallUnavailable || !strings.Contains(res.Error, shellWallNoBubblewrap) {
		t.Fatalf("with no bubblewrap the shell must be refused, saying what to install: %+v", res)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("with no bubblewrap the command ran")
	}
}

// A BUBBLEWRAP THAT CANNOT BUILD THE WALL (a host that confines user
// namespaces, a container that masks /proc) refuses the shell with
// bubblewrap's own words, and nothing runs. Stood in for by a bubblewrap
// that says what a confined one says and builds nothing.
func TestOnLinuxABubblewrapThatCannotBuildTheWallRefusesTheShell(t *testing.T) {
	f := newLinuxWall(t)
	bin := t.TempDir()
	stub := "#!/bin/sh\necho 'bwrap: setting up uid map: Permission denied' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "bwrap"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	res := modelCall(t, f.reg, "shell", map[string]any{"command": `echo ran > ran.txt`})
	if !res.Refused || res.ReasonCode != ReasonShellWallUnavailable || !strings.Contains(res.Error, shellWallNotBuilt) || !strings.Contains(res.Error, "setting up uid map") {
		t.Fatalf("a wall bubblewrap cannot build must refuse the shell with its words: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(f.home, "ran.txt")); err == nil {
		t.Fatal("a wall that was not built let the command run")
	}
}

// A WALL NOT BUILT WITHIN ITS BOUND IS GIVEN UP AT THE BOUND, and what the
// bubblewrap that never built it started in its group ends with it. Stood
// in for by a bubblewrap that starts a long job, which holds its error
// output, writes the job's number, and waits on it: before the holder had a
// process group of its own and a WaitDelay, the refusal waited out the job
// (thirty seconds), and the job ran on.
func TestOnLinuxAWallNotBuiltWithinItsBoundRefusesAtItAndLeavesNothing(t *testing.T) {
	f := newLinuxWall(t)
	bin := t.TempDir()
	pidFile := filepath.Join(bin, "job.pid")
	slow := "#!/bin/sh\nsleep 30 &\necho $! > " + q(pidFile) + "\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "bwrap"), []byte(slow), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.reg.SetShellWallSettings(func() ShellWallSettings { return ShellWallSettings{StartSeconds: 1} })
	began := time.Now()
	res := modelCall(t, f.reg, "shell", map[string]any{"command": `echo ran > ran.txt`})
	took := time.Since(began)
	if !res.Refused || !strings.Contains(res.Error, "not built within 1s") {
		t.Fatalf("a wall not built within its bound must refuse the shell: %+v", res)
	}
	if took > time.Second+holderWaitDelay+time.Second {
		t.Fatalf("the refusal came %s after the call, past the bound (1s) and the holder's wait delay (%s)", took, holderWaitDelay)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("rig: the stand-in did not start its job: %v", err)
	}
	job, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); processLives(job); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(job, syscall.SIGKILL)
			t.Fatalf("the job the unbuilt wall's bubblewrap started (%d) outlived the refusal", job)
		}
	}

	detached := "#!/bin/sh\nsetsid sleep 30 &\necho $! > " + q(pidFile) + "\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "bwrap"), []byte(detached), 0o700); err != nil {
		t.Fatal(err)
	}
	began = time.Now()
	res = modelCall(t, f.reg, "shell", map[string]any{"command": `echo ran > ran.txt`})
	took = time.Since(began)
	if raw, err := os.ReadFile(pidFile); err == nil {
		if job, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			t.Cleanup(func() { _ = syscall.Kill(job, syscall.SIGKILL) })
		}
	}
	if !res.Refused || !strings.Contains(res.Error, "not built within 1s") {
		t.Fatalf("a wall not built within its bound must refuse the shell: %+v", res)
	}
	if took > time.Second+holderWaitDelay+time.Second {
		t.Fatalf("a job that left the holder's group held the refusal %s after the call, past the bound (1s) and the holder's wait delay (%s)", took, holderWaitDelay)
	}
}

func TestOnLinuxARootIdentitysShellIsAsItWas(t *testing.T) {
	if os.Geteuid() != 0 {
		prev := shellAccountRoot
		shellAccountRoot = func() bool { return true }
		t.Cleanup(func() { shellAccountRoot = prev })
	}
	home := t.TempDir()
	key := filepath.Join(home, "data", "identity.sec")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.data", Kind: firewall.KindSubstrate, Path: filepath.Dir(key), Reason: "kept", Enforced: true}})
	reg := NewRegistry(home, policy, Timeouts{ShellSeconds: 30})
	t.Cleanup(func() { reg.EndLeftJobs(0) })
	if d := reg.shell.Description(); strings.Contains(d, "wall") {
		t.Fatalf("a root identity's shell is told of a wall it does not have: %q", d)
	}
	res, err := reg.shell.Execute(context.Background(), map[string]any{"command": "cat " + q(key)})
	if err != nil || !strings.Contains(res.Output, "KEPT SECRET") {
		t.Fatalf("a root identity's shell ran behind a wall, against the rule: %+v %v", res, err)
	}
	if s := reg.shell.holder.state; s != nil && s.cur != nil {
		t.Fatal("a sandbox was built for a root identity's shell")
	}
}

// An ordinary account's shell is told of its wall, in the sentence the
// operator approved (shellWallSentenceLinux).
func TestOnLinuxTheShellSaysItRunsBehindTheWall(t *testing.T) {
	restore := ShellWallAsAccountForTest()
	defer restore()
	if d := (&ShellTool{}).Description(); !strings.Contains(d, shellWallSentenceLinux) {
		t.Fatalf("the shell does not say it runs behind the wall: %q", d)
	}
}

// The devices are the operator's to add to (tools.shell_wall_devices): the
// sandbox's /dev holds the default ones, and a node the operator names
// beside them; naming one drops none of the default.
func TestOnLinuxTheWallsDevicesAreTheDefaultAndWhatTheSettingAdds(t *testing.T) {
	f := newLinuxWall(t)
	added := ""
	for _, node := range []string{"/dev/kmsg", "/dev/loop-control", "/dev/console"} {
		if _, err := os.Stat(node); err == nil && !slices.Contains(DefaultShellWallDevices(), node) {
			added = node
			break
		}
	}
	if added == "" {
		t.Fatal("rig: none of the candidate device nodes is on this host")
	}
	name := filepath.Base(added)
	if res := f.allowed(t, `ls /dev`, "urandom"); strings.Contains(res.Output, name) {
		t.Fatalf("rig: the default /dev already holds %s:\n%s", added, res.Output)
	}
	f.reg.SetShellWallSettings(func() ShellWallSettings { return ShellWallSettings{Devices: []string{added}} })
	res := f.allowed(t, `ls /dev`, name)
	for _, d := range DefaultShellWallDevices() {
		if !strings.Contains(res.Output, filepath.Base(d)) {
			t.Fatalf("A DEVICE THE OPERATOR ADDED REPLACED THE DEFAULT ONES: %s is gone from the sandbox:\n%s", d, res.Output)
		}
	}
	if strings.Contains(res.Output, "sda") || strings.Contains(res.Output, "nvme") {
		t.Fatalf("the sandbox holds a disk no setting names:\n%s", res.Output)
	}
}

func processLives(pid int) bool {
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return false
	}
	s := procState(status)
	return s != "Z" && s != "X"
}
