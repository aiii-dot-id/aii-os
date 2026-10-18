//go:build !windows

package test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type debRig struct {
	dir, log, scripts, homes, systemd, managers, effects, policy string
	state, passwd                                                string
	uids                                                         map[string]string
	home                                                         map[string]string
}

func newDebRig(t *testing.T, slots ...string) *debRig {
	t.Helper()
	r := &debRig{dir: t.TempDir(), uids: map[string]string{}, home: map[string]string{}}
	r.log = filepath.Join(r.dir, "calls.log")
	r.scripts = filepath.Join(r.dir, "scripts")
	r.homes = filepath.Join(r.dir, "home")
	r.systemd = filepath.Join(r.dir, "run-systemd")
	r.managers = filepath.Join(r.dir, "managers")
	r.effects = filepath.Join(r.dir, "effects.log")
	r.policy = filepath.Join(r.dir, "policy-rc.d") // absent unless a test writes one
	r.state = filepath.Join(r.dir, "var-lib-aii-os")
	r.passwd = filepath.Join(r.dir, "passwd")
	bin := filepath.Join(r.dir, "bin")
	for _, d := range []string{r.scripts, r.homes, r.systemd, r.managers, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(r.passwd, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, slot := range slots { // "user/identity-N"
		user := filepath.Dir(slot)
		r.account(t, user, filepath.Join(r.homes, user))
		if err := os.MkdirAll(filepath.Join(r.home[user], ".aii", filepath.Base(slot)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rewrite := func(name string, pairs ...string) {
		t.Helper()
		body := readPackaging(t, "deb", name)
		for i := 0; i < len(pairs); i += 2 {
			if n := strings.Count(stripComments(body), pairs[i]); n != 1 {
				t.Fatalf("packaging/deb/%s's code names %q %d times; the rig rewrites the one use", name, pairs[i], n)
			}
			body = strings.ReplaceAll(body, pairs[i], pairs[i+1])
		}
		if err := os.WriteFile(filepath.Join(r.scripts, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Quoted: the temp tree's path may hold a space, which an installed
	// system's paths do not.
	q := func(p string) string { return `"` + p + `"` }
	helper := filepath.Join(r.scripts, "aii-units.sh")
	aii := filepath.Join(bin, "aii")
	rewrite("aii-units.sh", "/home/* /root", q(r.homes)+"/* "+q(filepath.Join(r.dir, "root")), "/usr/sbin/policy-rc.d", q(r.policy),
		"/var/lib/aii-os", q(r.state), "/usr/bin/aii", q(aii))
	for _, s := range []string{"prerm", "postinst"} {
		rewrite(s, "/usr/share/aii-os/aii-units.sh", q(helper), "/run/systemd/system", q(r.systemd))
	}
	// postrm carries the helper whole, as build-deb.sh puts it there.
	rewrite("postrm")
	helperText, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	postrm := filepath.Join(r.scripts, "postrm")
	body, err := os.ReadFile(postrm)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	flock := flockStandInEnv + "=1; export " + flockStandInEnv + "; exec " + q(self) + " \"$@\"\n"
	if real, err := exec.LookPath("flock"); err == nil {
		flock = "exec " + q(real) + " \"$@\"\n"
	}
	if n := strings.Count(string(body), "\n#AII_UNITS_SH#\n"); n != 1 {
		t.Fatalf("postrm carries the helper's marker line %d times; build-deb.sh puts the helper in place of the one", n)
	}
	body = []byte(strings.Replace(string(body), "\n#AII_UNITS_SH#\n", "\n"+string(helperText)+"\n", 1))
	if err := os.WriteFile(postrm, body, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"deb-systemd-invoke": debSystemdInvokeStub,
		"systemctl":          systemctlStub,
		"stat":               statStub,
		"getent":             "#!/bin/sh\n# getent passwd [uid]: the rig's account database\nif [ -n \"$2\" ]; then grep \"^[^:]*:x:$2:\" \"$PASSWD\" || exit 2; else cat \"$PASSWD\"; fi\n",
		"runuser":            runuserStub,
		"rm":                 rmStub,
		"rmdir":              "#!/bin/sh\necho \"rmdir as ${AII_AS:-root} $*\" >> \"$CALLS\"\nexec /bin/rmdir \"$@\"\n",
		"flock":              "#!/bin/sh\necho \"flock as ${AII_AS:-root} $*\" >> \"$CALLS\"\n" + flock,
		"aii":                "#!/bin/sh\n# aii machine store -list, as the account (runuser set HOME); a state that does not read is .aii-store-list-fails\n[ \"$*\" = \"machine store -list\" ] || exit 2\n[ -e \"$HOME/.aii-store-list-fails\" ] && exit 1\ncat \"$HOME/.aii-store-places\" 2>/dev/null\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// account adds an account whose home is home: a uid of its own (the stat
// stub answers with it), its line in the account database, and a running
// manager in which nothing runs yet.
func (r *debRig) account(t *testing.T, user, home string) {
	t.Helper()
	if r.uids[user] != "" {
		return
	}
	r.uids[user] = strconv.Itoa(1001 + len(r.uids))
	r.home[user] = home
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".uid"), []byte(r.uids[user]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(r.managers, r.uids[user]), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(r.passwd, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(user + ":x:" + r.uids[user] + ":" + r.uids[user] + "::" + home + ":/bin/sh\n"); err != nil {
		t.Fatal(err)
	}
}

// statStub answers stat -c %u <home> with the home's account, and
// stat -f -c %T <path> with the path's file system type.
const statStub = `#!/bin/sh
if [ "$1" = -f ]; then
  if [ -f "$4.fstype" ]; then cat "$4.fstype"; else echo ext2/ext3; fi
  exit 0
fi
cat "$3/.uid"
`

// runuserStub records who it runs as, sets HOME from the account database
// as runuser does, and runs the rest.
const runuserStub = `#!/bin/sh
echo "runuser $*" >> "$CALLS"
[ "$1" = -u ] || exit 2
user=$2; shift 2
[ "$1" = -- ] && shift
HOME=$(grep "^$user:" "$PASSWD" | cut -d: -f6)
AII_AS=$user
export HOME AII_AS
exec "$@"
`

// rmStub records who removes what and removes it. Given AII_RIG_WINDOW
// and AII_RIG_WINDOW_IN, its first removal inside that directory first
// marks $AII_RIG_WINDOW.reached and waits (at most 30 s) for
// $AII_RIG_WINDOW.go: the test acts in that gap (debRig.window). Given
// AII_RIG_RM_FAILS, a removal naming that path fails as a permission
// refusal does, removing nothing.
const rmStub = `#!/bin/sh
echo "rm as ${AII_AS:-root} $*" >> "$CALLS"
for a in "$@"; do
  if [ -n "$AII_RIG_RM_FAILS" ] && [ "$a" = "$AII_RIG_RM_FAILS" ]; then
    echo "rm: cannot remove '$a': Permission denied" >&2
    exit 1
  fi
done
if [ -n "$AII_RIG_WINDOW" ] && [ ! -e "$AII_RIG_WINDOW.reached" ]; then
  case "$*" in
    *"$AII_RIG_WINDOW_IN"/*)
      : > "$AII_RIG_WINDOW.reached"
      i=0
      while [ ! -e "$AII_RIG_WINDOW.go" ] && [ "$i" -lt 600 ]; do sleep 0.05; i=$((i + 1)); done ;;
  esac
fi
exec /bin/rm "$@"
`

// flockStandInEnv asks this package's test binary to be the rig's flock,
// on a host without util-linux's (macOS).
const flockStandInEnv = "AII_TEST_FLOCK_STAND_IN"

func init() {
	if os.Getenv(flockStandInEnv) == "1" {
		os.Exit(flockStandIn(os.Args[1:]))
	}
}

// flockStandIn is the part of util-linux flock(1) the maintainer scripts
// use, on the kernel's flock(2) as util-linux's is: "[-n] -x|-s <fd>" locks
// a descriptor its caller holds open, and keeps nothing itself; "[-n]
// -x|-s <file> <command>..." holds the file's lock while the command runs.
// A lock held elsewhere is exit 1, as flock -n's is.
func flockStandIn(args []string) int {
	how, wait := syscall.LOCK_EX, 0
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-n":
			wait = syscall.LOCK_NB
		case "-x":
			how = syscall.LOCK_EX
		case "-s":
			how = syscall.LOCK_SH
		default:
			fmt.Fprintf(os.Stderr, "flock stand-in: %s is not modelled\n", args[0])
			return 64
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return 64
	}
	if fd, err := strconv.Atoi(args[0]); err == nil && len(args) == 1 {
		if err := syscall.Flock(fd, how|wait); err != nil {
			fmt.Fprintf(os.Stderr, "flock: %v\n", err)
			return 1
		}
		return 0
	}
	if len(args) < 2 {
		return 64
	}
	f, err := os.OpenFile(args[0], os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flock: %v\n", err)
		return 1
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), how|wait); err != nil {
		return 1
	}
	cmd := exec.Command(args[1], args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, flockStandInEnv+"=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		return 127
	}
	return 0
}

// holdLock takes path's lock exclusively without waiting, as a machine
// service holds its own; the test closes it.
func holdLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// window runs act in the gap rmStub leaves before the first removal inside
// dir, during the script run with the environment it answers; wait, once
// that script has ended, says whether the gap came.
func (r *debRig) window(dir string, act func()) (env []string, wait func() bool) {
	mark := filepath.Join(r.dir, "window")
	stop, acted := make(chan struct{}), make(chan bool, 1)
	go func() {
		for !exists(mark + ".reached") {
			select {
			case <-stop:
				acted <- false
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
		act()
		_ = os.WriteFile(mark+".go", nil, 0o644)
		acted <- true
	}()
	return []string{"AII_RIG_WINDOW=" + mark, "AII_RIG_WINDOW_IN=" + dir}, func() bool { close(stop); return <-acted }
}

// systemctlStub records the call and, for a user manager's try-restart or
// restart, does to the modelled units what the manager would.
const systemctlStub = `#!/bin/sh
echo "systemctl $*" >> "$CALLS"
uid=; action=; units=
while [ $# -gt 0 ]; do
  case "$1" in
    --quiet|--user|--global|--no-legend|--now|--) ;;
    --machine) shift; uid=${1%@} ;;
    try-restart|restart|stop|daemon-reload|enable|disable) action=$1 ;;
    *) units="$units $1" ;;
  esac
  shift
done
home=$(grep "^[^:]*:x:$uid:" "$PASSWD" 2>/dev/null | cut -d: -f6)
link() { case "$1" in *.socket) echo "$home/.config/systemd/user/sockets.target.wants/$1" ;; *) echo "$home/.config/systemd/user/default.target.wants/$1" ;; esac; }
case "$action" in
  try-restart|restart)
    [ -d "$MANAGERS/$uid" ] || exit 1
    for u in $units; do
      if [ -e "$MANAGERS/$uid/$u.active" ]; then echo "restarted $uid $u" >> "$EFFECTS"
      elif [ "$action" = restart ]; then echo "STARTED $uid $u" >> "$EFFECTS"; fi
    done ;;
  enable|disable)
    [ -n "$uid" ] && [ -d "$MANAGERS/$uid" ] || exit 1
    for u in $units; do
      if [ "$action" = enable ]; then
        : > "$MANAGERS/$uid/$u.enabled"; : > "$MANAGERS/$uid/$u.active"
        mkdir -p "$(dirname "$(link "$u")")"; /bin/ln -sf "$home/.config/systemd/user/$u" "$(link "$u")"
        echo "enabled $uid $u" >> "$EFFECTS"
      else
        /bin/rm -f "$MANAGERS/$uid/$u.enabled" "$MANAGERS/$uid/$u.active" "$(link "$u")"
        echo "disabled $uid $u" >> "$EFFECTS"
      fi
    done ;;
esac
exit 0
`

// debSystemdInvokeStub records the call and models the shipped helper's
// "--user restart": every unit that is enabled or active, one list kept
// across the managers, each unit on it restarted if running and STARTED if
// not.
const debSystemdInvokeStub = `#!/bin/sh
echo "deb-systemd-invoke $*" >> "$CALLS"
[ "${FAKE_DSI_RC:-0}" = 0 ] || exit "$FAKE_DSI_RC"
[ "$1" = --user ] && shift
action=$1; shift
if [ "$action" = restart ]; then
  list=
  for m in "$MANAGERS"/*; do
    [ -d "$m" ] || continue
    uid=$(basename "$m")
    for u in "$@"; do
      if [ -e "$m/$u.enabled" ] || [ -e "$m/$u.active" ]; then list="$list $u"; fi
    done
    for u in $list; do
      if [ -e "$m/$u.active" ]; then echo "restarted $uid $u" >> "$EFFECTS"; else echo "STARTED $uid $u" >> "$EFFECTS"; fi
    done
  done
fi
exit 0
`

// unit sets one identity's unit in its account's manager: running or not,
// enabled or not. An identity stopped with "aii stop" is not running and
// still enabled.
func (r *debRig) unit(t *testing.T, user, slot string, active, enabled bool) {
	t.Helper()
	base := filepath.Join(r.managers, r.uids[user], "aii-os@"+slot+".service")
	for suffix, on := range map[string]bool{".active": active, ".enabled": enabled} {
		if !on {
			_ = os.Remove(base + suffix)
			continue
		}
		if err := os.WriteFile(base+suffix, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// done is what the last script did to the modelled units, sorted.
func (r *debRig) done(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(r.effects)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(b), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	return lines
}

// run executes one maintainer script with dpkg's arguments; it answers the
// script's output and the calls it made, in order.
func (r *debRig) run(t *testing.T, env []string, script string, args ...string) (string, []string) {
	t.Helper()
	for _, f := range []string{r.log, r.effects} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", append([]string{filepath.Join(r.scripts, script)}, args...)...)
	cmd.Dir = r.dir
	cmd.Env = append(append(os.Environ(), "PATH="+filepath.Join(r.dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CALLS="+r.log, "MANAGERS="+r.managers, "EFFECTS="+r.effects, "PASSWD="+r.passwd, "DPKG_ROOT=", "FAKE_DSI_RC=", "SUDO_USER="), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v exited %v — a maintainer script that fails aborts the package operation:\n%s", script, args, err, out)
	}
	b, err := os.ReadFile(r.log)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(calls) == 1 && calls[0] == "" {
		calls = nil
	}
	return string(out), calls
}

const debUnits = "aii-os@identity-0.service aii-os@identity-1.service"

func TestTheDebianMaintainerScriptsParse(t *testing.T) {
	for _, s := range []string{"postinst", "prerm", "postrm", "aii-units.sh"} {
		if out, err := exec.Command("sh", "-n", filepath.Join(repoRoot(t), "packaging", "deb", s)).CombinedOutput(); err != nil {
			t.Errorf("packaging/deb/%s does not parse: %v\n%s", s, err, out)
		}
	}
}

func TestPrermStopsIdentitiesOnRemoveOnly(t *testing.T) {
	// Two users, overlapping slot names: one unit name each, once.
	r := newDebRig(t, "vega/identity-0", "vega/identity-1", "mira/identity-0")
	if _, calls := r.run(t, nil, "prerm", "remove"); strings.Join(calls, "\n") != "deb-systemd-invoke --user stop "+debUnits {
		t.Fatalf("prerm remove made %q, want one stop of every identity unit", calls)
	}
	// An upgrade must NOT stop identities: the process keeps its old inode
	// across the unpack, and postinst's restart moves it to the new binary.
	for _, args := range [][]string{{"upgrade", "0.1.14"}, {"deconfigure", "in-favour", "x", "1"}, {"failed-upgrade", "0.1.13"}} {
		if _, calls := r.run(t, nil, "prerm", args...); len(calls) != 0 {
			t.Errorf("prerm %v made %q — only a removal stops identities", args, calls)
		}
	}
	if _, calls := r.run(t, []string{"DPKG_ROOT=/target"}, "prerm", "remove"); len(calls) != 0 {
		t.Errorf("prerm acted inside a DPKG_ROOT: %q", calls)
	}
	if err := os.Remove(r.systemd); err != nil {
		t.Fatal(err)
	}
	if _, calls := r.run(t, nil, "prerm", "remove"); len(calls) != 0 {
		t.Errorf("prerm acted with no systemd running: %q", calls)
	}
	empty := newDebRig(t)
	if _, calls := empty.run(t, nil, "prerm", "remove"); len(calls) != 0 {
		t.Errorf("prerm acted with no identities: %q", calls)
	}
	// A stop that fails does not fail the removal.
	if _, calls := r.run(t, []string{"FAKE_DSI_RC=1"}, "prerm", "upgrade", "0.1.14"); len(calls) != 0 {
		t.Errorf("prerm upgrade made %q", calls)
	}
	failing := newDebRig(t, "vega/identity-0")
	if _, calls := failing.run(t, []string{"FAKE_DSI_RC=1"}, "prerm", "remove"); len(calls) != 1 {
		t.Errorf("prerm remove with a failing stop made %q", calls)
	}

	// A DIRECTORY NAME IS NOT A UNIT NAME UNLESS IT IS A SLOT'S. The names
	// are an account's own to choose and are joined into root's command
	// line: "identity-0 pipewire" became a second unit to stop, in every
	// account's manager. Only identity-<digits> is a slot.
	hostile := newDebRig(t, "vega/identity-0", "mira/identity-0 pipewire", "mira/identity-1\nufw", "mira/identity-x", "mira/identity-", "mira/identity-2 --system")
	if _, calls := hostile.run(t, nil, "prerm", "remove"); strings.Join(calls, "\n") != "deb-systemd-invoke --user stop aii-os@identity-0.service" {
		t.Fatalf("prerm remove took a directory's name into root's command line: %q", calls)
	}
}

func TestPostinstReloadsThenRestartsWhatIsRunning(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "mira/identity-1")
	r.unit(t, "vega", "identity-0", true, true)
	r.unit(t, "mira", "identity-1", true, true)
	// The reload comes first, or the restart uses the old unit. Then each
	// account's manager is asked about that account's own slot.
	want := []string{
		"systemctl --global daemon-reload",
		"deb-systemd-invoke --user daemon-reload",
		"systemctl --quiet --user --machine " + r.uids["mira"] + "@ try-restart aii-os@identity-1.service",
		"systemctl --quiet --user --machine " + r.uids["vega"] + "@ try-restart aii-os@identity-0.service",
	}
	restarted := []string{
		"restarted " + r.uids["vega"] + " aii-os@identity-0.service",
		"restarted " + r.uids["mira"] + " aii-os@identity-1.service",
	}
	sort.Strings(restarted)
	for _, args := range [][]string{{"configure", "0.1.13"}, {"abort-upgrade", "0.1.14"}, {"abort-remove"}} {
		out, calls := r.run(t, nil, "postinst", args...)
		if strings.Join(calls, "\n") != strings.Join(want, "\n") {
			t.Errorf("postinst %v made %q, want %q — the reload must come first, or the restart uses the old unit", args, calls, want)
		}
		if got := r.done(t); strings.Join(got, "\n") != strings.Join(restarted, "\n") {
			t.Errorf("postinst %v did %q, want each running identity restarted in its own account: %q", args, got, restarted)
		}
		if len(args) > 1 && strings.Contains(out, "AII OS is installed.") {
			t.Errorf("postinst %v greeted an upgrade as a first install:\n%s", args, out)
		}
	}
	// A first install: greeted, and the same restart.
	out, calls := r.run(t, nil, "postinst", "configure", "")
	if !strings.Contains(out, "AII OS is installed.") || len(calls) != 4 {
		t.Errorf("a first configure printed %q and made %q", out, calls)
	}
	// No identities yet: only the global reload.
	fresh := newDebRig(t)
	if _, calls := fresh.run(t, nil, "postinst", "configure", ""); strings.Join(calls, "\n") != "systemctl --global daemon-reload" {
		t.Errorf("a first install with no identities made %q", calls)
	}
	// A reload that fails does not fail the configure, and the restart
	// still follows.
	if _, calls := r.run(t, []string{"FAKE_DSI_RC=1"}, "postinst", "configure", "0.1.13"); len(calls) != 4 {
		t.Errorf("postinst with a failing reload made %q", calls)
	}
	if _, calls := r.run(t, []string{"DPKG_ROOT=/target"}, "postinst", "configure", "0.1.13"); strings.Join(calls, "\n") != "systemctl --global daemon-reload" {
		t.Errorf("postinst restarted identities inside a DPKG_ROOT: %q", calls)
	}
}

// AN UPGRADE STARTS NO IDENTITY THAT WAS STOPPED. An identity stopped with
// "aii stop" is not running and still enabled. The shipped helper restarts
// every unit that is enabled or active, and a restart starts a unit that is
// not running: so a package upgrade started identities their operator had
// stopped, and, because the helper never clears its list between accounts,
// started in a second account's manager a name that was running only in the
// first. The package asks each account's manager to try-restart that
// account's own slots, which restarts what runs and starts nothing.
func TestAnUpgradeStartsNoIdentityThatWasStopped(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "vega/identity-1", "mira/identity-0", "nova/identity-2")
	r.unit(t, "vega", "identity-0", true, true)  // running
	r.unit(t, "vega", "identity-1", false, true) // stopped with "aii stop": still enabled
	r.unit(t, "mira", "identity-0", false, true) // the same name as vega's running one, stopped here
	r.unit(t, "nova", "identity-2", true, false) // started by hand, never enabled: it is running
	want := []string{
		"restarted " + r.uids["nova"] + " aii-os@identity-2.service",
		"restarted " + r.uids["vega"] + " aii-os@identity-0.service",
	}
	sort.Strings(want)
	for _, args := range [][]string{{"configure", "0.1.14"}, {"configure", ""}, {"abort-upgrade", "0.1.14"}} {
		if _, calls := r.run(t, nil, "postinst", args...); len(calls) == 0 {
			t.Fatalf("postinst %v made no call at all", args)
		}
		got := r.done(t)
		for _, line := range got {
			if strings.HasPrefix(line, "STARTED ") {
				t.Errorf("postinst %v started an identity that was not running: %q", args, line)
			}
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("postinst %v did %q, want exactly the running identities restarted: %q", args, got, want)
		}
	}

	// An account whose manager is not running has nothing running in it:
	// nothing is done there and the configure does not fail.
	if err := os.RemoveAll(filepath.Join(r.managers, r.uids["nova"])); err != nil {
		t.Fatal(err)
	}
	r.run(t, nil, "postinst", "configure", "0.1.14")
	if got := r.done(t); len(got) != 1 || got[0] != "restarted "+r.uids["vega"]+" aii-os@identity-0.service" {
		t.Errorf("with one account's manager down postinst did %q", got)
	}

	// The local policy forbids the action (a chroot, an image being built):
	// nothing is restarted, as the helper itself would have it.
	if err := os.WriteFile(r.policy, []byte("#!/bin/sh\nexit 101\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, calls := r.run(t, nil, "postinst", "configure", "0.1.14"); len(calls) != 2 {
		t.Errorf("with the local policy forbidding it postinst made %q, want the two reloads only", calls)
	}
	if got := r.done(t); len(got) != 0 {
		t.Errorf("with the local policy forbidding it postinst did %q", got)
	}
	// A policy that allows it changes nothing.
	if err := os.WriteFile(r.policy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.run(t, nil, "postinst", "configure", "0.1.14")
	if got := r.done(t); len(got) != 1 {
		t.Errorf("with the local policy allowing it postinst did %q", got)
	}
}

func TestPostrmNeverTouchesAnIdentity(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "vega/identity-1")
	// What a purge does act on: the machine service and a store, beside
	// the identities, so the purge makes calls and the identities are
	// still the same bytes.
	r.machine(t, "vega", true, false)
	store := r.store(t, filepath.Join(r.home["vega"], ".aii", "plugins"))
	for _, slot := range []string{"identity-0", "identity-1"} {
		writeFile(t, filepath.Join(r.home["vega"], ".aii", slot, "data", "ledger.jsonl"), "{\"seq\":1}\n")
		writeFile(t, filepath.Join(r.home["vega"], ".aii", slot, "data", "identity.sec"), "a key")
	}
	before := treeHashes(t, r.home["vega"], ".aii/identity-")
	for _, args := range [][]string{{"remove"}, {"purge"}} {
		_, calls := r.run(t, nil, "postrm", args...)
		if len(calls) == 0 || calls[0] != "systemctl --global daemon-reload" {
			t.Errorf("postrm %v made %q", args, calls)
		}
		if args[0] == "remove" && len(calls) != 1 {
			t.Errorf("postrm remove acted beyond the reload: %q", calls)
		}
	}
	if _, calls := r.run(t, nil, "postrm", "upgrade", "0.1.14"); len(calls) != 0 {
		t.Errorf("postrm upgrade made %q", calls)
	}
	if after := treeHashes(t, r.home["vega"], ".aii/identity-"); !equalMaps(after, before) {
		t.Fatalf("an identity changed across remove and purge:\n before %v\n after  %v", before, after)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("rig: the purge did not act at all (the store stayed): %v", err)
	}
}
