//go:build (linux && !android) || (darwin && !ios) || windows

package uninstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// THE ACCOUNT, IN TEMPORARY DIRECTORIES, WITH ITS MANAGERS FAKED. Every slot,
// the service directory and the store are real files under the test's own
// home (HOME points there, so the machine package finds the account's
// default store beside it); the registrations, what runs, and the service's
// lock are the fake's, and every call is recorded in order. Nothing reaches
// a real launchd, user manager, Run key or task scheduler.

type account struct {
	t                *testing.T
	home, root, mdir string
	volume           string // another "volume", where one slot lies behind a link
	store            string

	calls      []string
	registered map[string]bool   // slots the startup registrations name
	running    map[string]bool   // slot directories a process runs
	stuck      map[string]error  // SlotGone's answer for a slot that stays
	dev        map[string]string // a slot's development service directory
	keys       map[string]*crypto.KeyPair

	machineRegistered bool
	serviceUp         bool
	comesBack         bool // a successor holds the lock again after every stop
	failOnce          map[string]error
	programs          []install.InstalledProgram
	procs             []Process
}

// newAccount makes an account with two registered slots — identity-0 here,
// identity-1 on another volume behind a link — the name links, a file of
// the person's own, a running machine service's directory and its store
// holding an entry.
func newAccount(t *testing.T) *account {
	t.Helper()
	home := realDir(t, t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	a := &account{t: t, home: home, root: filepath.Join(home, install.Dir), volume: realDir(t, t.TempDir()),
		registered: map[string]bool{}, running: map[string]bool{}, stuck: map[string]error{}, dev: map[string]string{},
		keys: map[string]*crypto.KeyPair{}, failOnce: map[string]error{}, machineRegistered: true, serviceUp: true}
	a.mdir = filepath.Join(a.root, install.MachineDirName)
	a.store = filepath.Join(a.root, "plugins")
	a.slot("identity-0", filepath.Join(a.root, "identity-0"))
	far := filepath.Join(a.volume, "slot")
	a.slot("identity-1", far)
	if err := os.MkdirAll(filepath.Join(a.root, install.ByName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(far, filepath.Join(a.root, "identity-1")); err != nil {
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
		// Making a link needs a privilege a Windows account may lack: the
		// slot lies in ~/.aii there, and the link is the other rows'.
		if err := os.Rename(far, filepath.Join(a.root, "identity-1")); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Symlink(filepath.Join("..", "identity-0"), filepath.Join(a.root, install.ByName, "Vega")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(a.root, "notes.txt"), "the person's own file")
	if err := os.MkdirAll(a.mdir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(a.mdir, "state.json"), `{"identities":[]}`)
	w, err := plugstore.OpenWriter(a.store)
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := w.Blob(strings.Repeat("ab", 32))
	write(t, blob, "a model's bytes")
	w.Close()
	return a
}

func realDir(t *testing.T, d string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// slot makes a slot's world at dir: a record, a key, a config.
func (a *account) slot(name, dir string) {
	write(a.t, filepath.Join(dir, "data", "ledger.jsonl"), `{"seq":1,"of":"`+name+`"}`+"\n")
	write(a.t, filepath.Join(dir, "data", "identity.sec"), "the key of "+name)
	write(a.t, filepath.Join(dir, "config", "config.json"), `{"dashboard":{"port":8180}}`)
	a.registered[name] = true
	a.running[filepath.Join(a.root, name)] = true
}

func (a *account) call(c string) { a.calls = append(a.calls, c) }

// fail answers the one failure set for op, once.
func (a *account) fail(op string) error {
	if err, ok := a.failOnce[op]; ok {
		delete(a.failOnce, op)
		return err
	}
	return nil
}

func (a *account) env() Env {
	return Env{
		OwnAccount: func() error { return nil },
		Root:       func() (string, error) { return a.root, nil },
		MachineDir: func() (string, error) { return a.mdir, nil },
		StartupSlots: func() ([]install.StartupSlot, error) {
			var out []install.StartupSlot
			for s, on := range a.registered {
				if on {
					out = append(out, install.StartupSlot{Slot: s, Dir: filepath.Join(a.root, s)})
				}
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
			return out, nil
		},
		SlotDirs: install.SlotDirs,
		Name: func(dir string) string {
			if filepath.Base(dir) == "identity-0" {
				return "Vega"
			}
			return ""
		},
		SlotKey: func(dir string) (*crypto.KeyPair, error) {
			if k := a.keys[filepath.Base(dir)]; k != nil {
				return k, nil
			}
			return nil, fs.ErrNotExist
		},
		SlotService: func(dir string) (string, error) { return a.dev[filepath.Base(dir)], nil },
		SlotRunning: func(dir string) (bool, error) { return a.running[dir], nil },
		Unregister: func(s string) error {
			a.call("unregister " + s)
			if err := a.fail("unregister " + s); err != nil {
				return err
			}
			a.registered[s] = false
			a.running[filepath.Join(a.root, s)] = false
			return nil
		},
		SlotGone: func(s string) error {
			a.call("slotgone " + s)
			if a.registered[s] {
				return errors.New("still registered")
			}
			return a.stuck[s]
		},
		Forget: func(_ context.Context, _, slot string, _ *crypto.KeyPair, _ string) error {
			a.call("forget " + filepath.Base(slot))
			return nil
		},
		MachineRegistration: func() (string, bool, error) { return "/opt/aii/aii", a.machineRegistered, nil },
		UnregisterMachine: func() error {
			a.call("unregister machine")
			if err := a.fail("unregister machine"); err != nil {
				return err
			}
			a.machineRegistered = false
			a.serviceUp = a.comesBack
			return nil
		},
		MachineGone: func() error {
			a.call("machinegone")
			if a.machineRegistered {
				return errors.New("still registered")
			}
			return nil
		},
		ServiceRuns: func(string) (bool, error) { return a.serviceUp, nil },
		StorePlaces: machine.StorePlaces,
		StoreSize:   machine.StoreSize,
		Remove: func(dir string) (machine.Removal, error) {
			a.call("remove")
			if err := a.fail("remove"); err != nil {
				return machine.Removal{}, err
			}
			return machine.Remove(dir)
		},
		Programs:  func() []install.InstalledProgram { return a.programs },
		Processes: func() ([]Process, error) { return a.procs, nil },
		Leftovers: updates.Leftovers,
		Sweep: func() ([]string, error) {
			a.call("sweep")
			return nil, nil
		},
		Shortcuts: func([]install.InstalledProgram) ([]string, []Stay) {
			a.call("shortcuts")
			return nil, nil
		},
		Self: os.Getuid(),
	}
}

// hashes is every file and link under dir with its content's hash: a
// slot's tree, compared byte for byte.
func hashes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = "link " + target
		case d.Type().IsRegular():
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			out[rel] = hex.EncodeToString(sum[:])
		default:
			out[rel] = "dir"
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// everything is the account's whole tree, slots and all, by relative path,
// a link's target written relative to the account's directories too, so
// two accounts' trees compare.
func (a *account) everything() map[string]string {
	all := map[string]string{}
	for k, v := range hashes(a.t, a.home) {
		all[k] = strings.ReplaceAll(strings.ReplaceAll(v, a.volume, "{volume}"), a.home, "{home}")
	}
	for k, v := range hashes(a.t, a.volume) {
		all["volume/"+k] = v
	}
	return all
}

func run(t *testing.T, a *account) Report {
	t.Helper()
	r, err := Run(a.env(), false)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func steps(r Report) []string {
	var out []string
	for _, d := range r.Done {
		out = append(out, d.Step)
	}
	return out
}

// THE ORDER IS SLOTS, SERVICE, STORE, APP: every slot is unregistered and
// found gone before the service is touched, the service is gone before the
// store, and the store before the app.
func TestTheOrderIsSlotsServiceStoreApp(t *testing.T) {
	a := newAccount(t)
	r := run(t, a)
	if r.ExitCode() != 0 || !slices.Equal(steps(r), []string{StepSlots, StepService, StepStore, StepApp}) {
		t.Fatalf("the run: exit %d, steps %v, stopped %+v", r.ExitCode(), steps(r), r.Stopped)
	}
	at := func(c string) int { return slices.Index(a.calls, c) }
	lastSlot := max(at("slotgone identity-0"), at("slotgone identity-1"))
	if at("unregister identity-0") < 0 || at("unregister identity-1") < 0 || lastSlot > at("unregister machine") ||
		at("machinegone") > at("remove") || at("remove") > at("sweep") || at("sweep") > at("shortcuts") || at("unregister machine") > at("machinegone") {
		t.Fatalf("out of order: %q", a.calls)
	}
	if exists(a.mdir) || exists(a.store) {
		t.Fatal("the service directory or the store stayed")
	}
}

// A SLOT THAT STAYS STOPS THE RUN BEFORE THE SERVICE: every slot is still
// attempted, and nothing of the service or the store is touched.
func TestASlotThatStaysStopsBeforeTheService(t *testing.T) {
	a := newAccount(t)
	a.stuck["identity-0"] = errors.New("launchd still holds it")
	before := a.everything()
	r := run(t, a)
	if r.Stopped == nil || r.Stopped.Step != StepSlots || !strings.Contains(r.Stopped.Reason, "identity-0") || r.ExitCode() != 3 {
		t.Fatalf("a slot that stays: %+v", r.Stopped)
	}
	if !slices.Contains(a.calls, "unregister identity-1") {
		t.Fatalf("the other slot was not attempted: %q", a.calls)
	}
	for _, c := range a.calls {
		if c == "unregister machine" || c == "remove" || c == "sweep" {
			t.Fatalf("a later step ran after the slots stopped: %q", a.calls)
		}
	}
	if !exists(a.mdir) || !exists(a.store) {
		t.Fatal("the service directory or the store went while a slot stayed")
	}
	after := a.everything()
	for p, h := range before {
		if strings.Contains(p, "identity-") || strings.HasPrefix(p, "volume/") {
			if after[p] != h {
				t.Fatalf("a slot's %s changed", p)
			}
		}
	}
}

// A REGISTERED IDENTITY THAT TAKES A MOMENT TO LEAVE IS NOT ONE RUNNING BY
// HAND: once its manager no longer holds it, its process is given the
// settling wait to exit (launchd signals it and lets it go), and only one
// still there after the wait stops the run.
func TestAnIdentityLeavingAfterItsStopIsWaitedFor(t *testing.T) {
	a := newAccount(t)
	e := a.env()
	e.Settle = 2 * time.Second
	slow := filepath.Join(a.root, "identity-0")
	asked := 0
	base := e.SlotRunning
	e.SlotRunning = func(dir string) (bool, error) {
		if dir == slow && !a.registered["identity-0"] {
			asked++
			return asked < 4, nil // gone on the fourth look
		}
		return base(dir)
	}
	r, err := Run(e, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode() != 0 || asked < 4 {
		t.Fatalf("a slot leaving after its stop: exit %d after %d looks, stopped %+v", r.ExitCode(), asked, r.Stopped)
	}
	// One that never leaves stops the run once the wait is spent.
	b := newAccount(t)
	eb := b.env()
	eb.Settle = 300 * time.Millisecond
	eb.SlotRunning = func(dir string) (bool, error) {
		return dir == filepath.Join(b.root, "identity-0") || b.running[dir], nil
	}
	if r, err := Run(eb, false); err != nil || r.Stopped == nil || r.Stopped.Step != StepSlots {
		t.Fatalf("a slot that never leaves: %+v %v", r.Stopped, err)
	}
}

// AN IDENTITY RUNNING BY HAND KEEPS EVERYTHING: it is named and never
// killed, and the service and the store stay, since it may be using them.
// One that runs against a development service of its own does not block.
func TestAnIdentityRunningByHandKeepsEverything(t *testing.T) {
	a := newAccount(t)
	byHand := filepath.Join(a.root, "identity-2")
	write(t, filepath.Join(byHand, "data", "ledger.jsonl"), "{}\n")
	a.running[byHand] = true
	r := run(t, a)
	if r.Stopped == nil || r.Stopped.Step != StepSlots || !strings.Contains(r.Stopped.Reason, "identity-2 runs outside its registration") {
		t.Fatalf("an identity started by hand: %+v", r.Stopped)
	}
	if slices.Contains(a.calls, "unregister machine") || !exists(a.store) || !exists(a.mdir) {
		t.Fatalf("the service or the store was touched while an identity ran by hand: %q", a.calls)
	}
	// Against its own development service it holds nothing removed here.
	a2 := newAccount(t)
	dev := filepath.Join(a2.root, "identity-3")
	write(t, filepath.Join(dev, "data", "ledger.jsonl"), "{}\n")
	a2.running[dev] = true
	a2.dev["identity-3"] = filepath.Join(dev, "devmachine")
	r2 := run(t, a2)
	if r2.ExitCode() != 0 {
		t.Fatalf("a development service's identity blocked the uninstall: %+v", r2.Stopped)
	}
	found := false
	for _, s := range r2.Stays {
		found = found || (s.What == StayDevelop && s.Path == a2.dev["identity-3"])
	}
	if !found {
		t.Fatalf("the development service's directory is not named as staying: %+v", r2.Stays)
	}
}

// A SERVICE THAT COMES BACK KEEPS THE STORE: a successor that holds the
// lock after every stop (a handover in flight) is stopped again, twice, and
// then the run stops before the store.
func TestAServiceThatComesBackKeepsTheStore(t *testing.T) {
	a := newAccount(t)
	a.comesBack = true
	r := run(t, a)
	if r.Stopped == nil || r.Stopped.Step != StepService {
		t.Fatalf("a service that comes back: %+v", r.Stopped)
	}
	n := 0
	for _, c := range a.calls {
		if c == "unregister machine" {
			n++
		}
	}
	if n != 3 || slices.Contains(a.calls, "remove") || !exists(a.store) {
		t.Fatalf("stopped %d times (want 3: once, then twice again), the store must stay: %q", n, a.calls)
	}
}

// UNINSTALL TWICE IS THE SAME AS ONCE: the second run finds nothing to do,
// does nothing, and says it is done.
func TestUninstallTwiceIsTheSameAsOnce(t *testing.T) {
	a := newAccount(t)
	if r := run(t, a); r.ExitCode() != 0 {
		t.Fatalf("the first run: %+v", r.Stopped)
	}
	once := a.everything()
	a.calls = nil
	r := run(t, a)
	if r.ExitCode() != 0 || !slices.Equal(steps(r), []string{StepSlots, StepService, StepStore, StepApp}) {
		t.Fatalf("the second run: %+v %v", r.Stopped, steps(r))
	}
	if !mapsEqual(a.everything(), once) {
		t.Fatal("the second run changed the account")
	}
	if slices.Contains(a.calls, "unregister identity-0") || slices.Contains(a.calls, "unregister machine") {
		t.Fatalf("the second run unregistered what was not registered: %q", a.calls)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// A STOP AFTER ANY STEP, THEN A RERUN, ENDS WHERE ONE RUN ENDS.
func TestAStopAfterEachStepThenARerunEndsWhereOneRunEnds(t *testing.T) {
	one := newAccount(t)
	if r := run(t, one); r.ExitCode() != 0 {
		t.Fatal(r.Stopped)
	}
	want := one.everything()
	for _, op := range []string{"unregister identity-1", "unregister machine", "remove"} {
		a := newAccount(t)
		a.failOnce[op] = errors.New("interrupted")
		if r := run(t, a); r.ExitCode() != 3 {
			t.Fatalf("a failure at %s did not stop the run: %+v", op, r)
		}
		if r := run(t, a); r.ExitCode() != 0 {
			t.Fatalf("the rerun after %s stopped: %+v", op, r.Stopped)
		}
		if got := a.everything(); !mapsEqual(got, want) {
			t.Fatalf("after a stop at %s and a rerun the account is not where one run leaves it:\n got  %v\n want %v", op, keys(got), keys(want))
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// EVERY SLOT'S TREE IS BYTE-IDENTICAL after an uninstall, the slot on
// another volume behind a link among them; the service directory and the
// store are gone.
func TestEverySlotsTreeIsByteIdentical(t *testing.T) {
	a := newAccount(t)
	near, far := filepath.Join(a.root, "identity-0"), realPath(filepath.Join(a.root, "identity-1"))
	before := []map[string]string{hashes(t, near), hashes(t, far)}
	link, _ := os.Readlink(filepath.Join(a.root, "identity-1"))
	if r := run(t, a); r.ExitCode() != 0 {
		t.Fatal(r.Stopped)
	}
	for i, d := range []string{near, far} {
		if !mapsEqual(hashes(t, d), before[i]) {
			t.Fatalf("the slot at %s changed", d)
		}
	}
	if got, _ := os.Readlink(filepath.Join(a.root, "identity-1")); got != link {
		t.Fatal("the link to the slot on another volume changed")
	}
	if exists(a.mdir) || exists(a.store) {
		t.Fatal("the service directory or the store stayed")
	}
}

// TWO ACCOUNTS ARE INDEPENDENT: one account's uninstall touches nothing of
// another's — its slots, its service directory, its store, its
// registrations.
func TestTwoAccountsAreIndependent(t *testing.T) {
	b := newAccount(t)
	bBefore := b.everything()
	bRegistered := map[string]bool{}
	for k, v := range b.registered {
		bRegistered[k] = v
	}
	a := newAccount(t) // HOME is now a's
	if r := run(t, a); r.ExitCode() != 0 {
		t.Fatal(r.Stopped)
	}
	if !mapsEqual(b.everything(), bBefore) || len(b.calls) != 0 || !b.machineRegistered {
		t.Fatal("one account's uninstall touched another's")
	}
	for k, v := range bRegistered {
		if b.registered[k] != v {
			t.Fatal("one account's uninstall changed another's registrations")
		}
	}
}

// ANOTHER ACCOUNT RUNNING A SHARED BUNDLE IS NAMED, and the updater's
// leftovers beside it stay; with nobody else running it they are removed,
// and the program itself never is.
func TestAnotherAccountRunningTheSharedBundleIsNamed(t *testing.T) {
	for _, shared := range []bool{true, false} {
		a := newAccount(t)
		apps := realDir(t, t.TempDir())
		exe := filepath.Join(apps, "AII OS.app", "Contents", "MacOS", "aii")
		write(t, exe, "the program")
		left := updates.Leftovers(exe)
		for _, l := range left {
			write(t, l, "the updater's")
		}
		a.programs = []install.InstalledProgram{{Exe: exe, Version: "0.1.16"}}
		if shared {
			a.procs = []Process{{UID: os.Getuid() + 1, Exe: exe}, {UID: os.Getuid(), Exe: exe}}
		}
		r := run(t, a)
		if r.ExitCode() != 0 || len(r.Programs) != 1 {
			t.Fatalf("shared=%v: %+v", shared, r)
		}
		p := r.Programs[0]
		if shared && (!slices.Equal(p.SharedWith, []int{os.Getuid() + 1}) || !p.LeftKept) {
			t.Fatalf("another account's use of the bundle is not named, or its leftovers not kept: %+v", p)
		}
		for _, l := range left {
			if exists(l) != shared {
				t.Fatalf("shared=%v: the leftover %s exists=%v", shared, l, exists(l))
			}
		}
		if !exists(exe) {
			t.Fatal("the program itself was removed")
		}
	}
}

// THE REPORT NAMES WHAT STAYS, as the golden report says it (synthetic
// names; the test's directories written as {home} and {volume}).
func TestTheReportNamesWhatStays(t *testing.T) {
	a := newAccount(t)
	r := run(t, a)
	var kept []Stay
	for _, s := range r.Stays {
		if s.What != StayLinger && s.What != StayJournal {
			kept = append(kept, s)
		}
	}
	r.Stays = kept
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(strings.ReplaceAll(filepath.ToSlash(string(raw)), filepath.ToSlash(a.home), "{home}"), filepath.ToSlash(a.volume), "{volume}")
	if runtime.GOOS == "windows" {
		t.Skip("the golden report is written with the other rows' paths; TestEverySlotsTreeIsByteIdentical and the plan's test hold Windows")
	}
	golden := filepath.Join("testdata", "report.golden.json")
	if os.Getenv("AII_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got+"\n" != string(want) {
		t.Fatalf("the report:\n%s\nwant:\n%s", got, want)
	}
	if runtime.GOOS == "linux" {
		var linger bool
		for _, s := range planStays(t, a) {
			linger = linger || s.What == StayLinger
		}
		if !linger {
			t.Fatal("on Linux the report does not name lingering as staying")
		}
	}
}

// THE LAUNCHER'S FIRST-START LOCK IS NAMED AS WHAT IT IS. It lies directly
// under ~/.aii, where a name the uninstall does not know is reported as
// staying because it is "not made by AII OS" — which of this file would be
// false. It stays, as everything under ~/.aii but the service's does, and
// the plan and the run both say whose it is.
func TestTheLaunchersFirstStartLockStaysAndIsNotCalledSomeoneElses(t *testing.T) {
	a := newAccount(t)
	lock := filepath.Join(a.root, install.FirstStartLock)
	write(t, lock, "")
	why := func(stays []Stay) string {
		for _, s := range stays {
			if s.Path == lock {
				return s.Why
			}
		}
		return ""
	}
	for name, stays := range map[string][]Stay{"a plan": planStays(t, a), "a run": run(t, a).Stays} {
		if got := why(stays); !strings.HasPrefix(got, "the launcher's lock") {
			t.Fatalf("%s says of the launcher's lock: %q", name, got)
		}
	}
	if !exists(lock) {
		t.Fatal("the run removed a file under ~/.aii that is not the service's")
	}
}

// planStays is a plan's stays: what a run would leave.
func planStays(t *testing.T, a *account) []Stay {
	t.Helper()
	r, err := Run(a.env(), true)
	if err != nil {
		t.Fatal(err)
	}
	return r.Stays
}

// A PLAN CHANGES NOTHING: it names the identities that would stop, the
// steps and the bytes the stores hold, and touches nothing.
func TestAPlanChangesNothing(t *testing.T) {
	a := newAccount(t)
	before := a.everything()
	r, err := Run(a.env(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Plan || len(a.calls) != 0 || !mapsEqual(a.everything(), before) {
		t.Fatalf("a plan acted: %q", a.calls)
	}
	if len(r.Identities) != 2 || !r.Identities[0].Running || r.Identities[0].Name != "Vega" || r.FreedBytes == 0 {
		t.Fatalf("the plan does not name the identities that would stop, or the store's size: %+v", r)
	}
	if !slices.ContainsFunc(r.Steps, func(s string) bool { return strings.HasPrefix(s, "stop and unregister identity-0 (Vega)") }) {
		t.Fatalf("the plan's steps: %q", r.Steps)
	}
}

func TestUninstallRefusesUnderSudoAndInASandboxHome(t *testing.T) {
	a := newAccount(t)
	t.Cleanup(install.AccountHomeForTest(a.home))
	e := a.env()
	e.OwnAccount = install.OwnAccount
	sandbox := filepath.Join(a.root, "identity-0", "sandbox")
	if err := os.MkdirAll(sandbox, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", sandbox)
	t.Setenv("USERPROFILE", sandbox)
	var foreign *install.ForeignAccountError
	if _, err := Run(e, false); !errors.As(err, &foreign) {
		t.Fatalf("an uninstall from an identity's sandbox: %v", err)
	}
	if runtime.GOOS != "windows" {
		t.Setenv("HOME", a.home)
		t.Setenv("SUDO_USER", "nobody")
		if _, err := Run(e, false); !errors.As(err, &foreign) {
			t.Fatalf("an uninstall under sudo: %v", err)
		}
	}
	if len(a.calls) != 0 || !exists(a.store) {
		t.Fatalf("a refused uninstall acted: %q", a.calls)
	}
}

// FORGET IS ASKED ONLY WHILE THE SERVICE IS UP, and only of a born slot of
// the account's service: what it holds of a slot goes with its state
// anyway.
func TestForgetIsAskedOfBornSlotsWhileTheServiceIsUp(t *testing.T) {
	a := newAccount(t)
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	a.keys["identity-0"] = kp
	run(t, a)
	if !slices.Contains(a.calls, "forget identity-0") || slices.Contains(a.calls, "forget identity-1") {
		t.Fatalf("forget: %q", a.calls)
	}
	b := newAccount(t)
	b.keys["identity-0"] = kp
	b.serviceUp = false
	run(t, b)
	if slices.Contains(b.calls, "forget identity-0") {
		t.Fatalf("forget was asked of a service that is not up: %q", b.calls)
	}
}
