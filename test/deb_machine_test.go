//go:build !windows

package test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"

	ps "github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// machine gives user's account a machine service: its directory with its
// state and lock, its two unit files, and (enabled) their enable links and
// the manager's record of them; places is what aii machine store -list
// answers there.
func (r *debRig) machine(t *testing.T, user string, enabled, active bool, places ...string) {
	t.Helper()
	home := r.home[user]
	writeFile(t, filepath.Join(home, ".aii", "machine", "state.json"), `{"identities":[]}`)
	writeFile(t, filepath.Join(home, ".aii", "machine", "machine.lock"), "")
	if len(places) == 0 {
		places = []string{filepath.Join(home, ".aii", "plugins")}
	}
	writeFile(t, filepath.Join(home, ".aii-store-places"), strings.Join(places, "\n")+"\n")
	units := filepath.Join(home, ".config", "systemd", "user")
	for _, u := range []string{"aii-os-machine.socket", "aii-os-machine.service"} {
		writeFile(t, filepath.Join(units, u), "[Unit]\n")
		m := filepath.Join(r.managers, r.uids[user], u)
		if enabled {
			wants := "default.target.wants"
			if strings.HasSuffix(u, ".socket") {
				wants = "sockets.target.wants"
			}
			if err := os.MkdirAll(filepath.Join(units, wants), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(units, u), filepath.Join(units, wants, u)); err != nil {
				t.Fatal(err)
			}
			writeFile(t, m+".enabled", "")
		}
		if active {
			writeFile(t, m+".active", "")
		}
	}
}

// The keys of the rig's store's root and view.
const (
	storeRootKey = "0123456789abcdef"
	storeViewKey = "fedcba9876543210"
)

// store makes a plugin store at dir as its writer leaves it: its lock, an
// archive, a blob, a root and a view with their records, and its trash.
func (r *debRig) store(t *testing.T, dir string) string {
	t.Helper()
	sha := strings.Repeat("ab", 32)
	for path, body := range map[string]string{
		".lock":                              "",
		"a/" + sha:                           "an archive",
		"b/" + sha:                           "a blob",
		"r/" + storeRootKey + "/lib/x.so":    "code",
		"r/" + storeRootKey + ".record.json": "{}",
		"v/" + storeViewKey + "/m.bin":       "a model",
		"v/" + storeViewKey + ".record.json": "{}",
		".trash/.keep":                       "",
	} {
		writeFile(t, filepath.Join(dir, path), body)
	}
	return dir
}

// treeHashes is every file under root whose path has prefix, with its
// content's hash.
func treeHashes(t *testing.T, root, prefix string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if !strings.HasPrefix(filepath.ToSlash(rel), prefix) {
			return nil
		}
		if d.Type().IsRegular() {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			out[rel] = hex.EncodeToString(sum[:])
		} else {
			out[rel] = d.Type().String()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func equalMaps(a, b map[string]string) bool {
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

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func (r *debRig) stash(t *testing.T, user string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.state, "accounts", r.uids[user]))
	if err != nil {
		t.Fatalf("no stash for %s: %v", user, err)
	}
	return string(b)
}

func TestRemoveStopsAndDisablesEachAccountsMachineServiceAndKeepsItsStore(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "mira/identity-0")
	r.machine(t, "vega", true, true)
	r.machine(t, "mira", true, true)
	stores := map[string]map[string]string{}
	for _, u := range []string{"vega", "mira"} {
		dir := r.store(t, filepath.Join(r.home[u], ".aii", "plugins"))
		stores[u] = treeHashes(t, dir, "")
	}
	r.run(t, nil, "prerm", "remove")
	got := r.done(t)
	var want []string
	for _, u := range []string{"vega", "mira"} {
		for _, unit := range []string{"aii-os-machine.socket", "aii-os-machine.service"} {
			want = append(want, "disabled "+r.uids[u]+" "+unit)
		}
	}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("prerm remove did %q, want each account's machine units disabled and stopped in its own manager: %q", got, want)
	}
	for _, u := range []string{"vega", "mira"} {
		if exists(filepath.Join(r.home[u], ".config/systemd/user/default.target.wants/aii-os-machine.service")) {
			t.Errorf("%s's machine service is still enabled", u)
		}
		if after := treeHashes(t, filepath.Join(r.home[u], ".aii", "plugins"), ""); !equalMaps(after, stores[u]) {
			t.Errorf("a remove touched %s's store", u)
		}
		if !exists(filepath.Join(r.home[u], ".aii", "machine", "state.json")) {
			t.Errorf("a remove touched %s's service directory", u)
		}
		if st := r.stash(t, u); !strings.Contains(st, "machine-enabled\n") || !strings.Contains(st, "store "+filepath.Join(r.home[u], ".aii", "plugins")+"\n") {
			t.Errorf("%s's stash: %q", u, st)
		}
	}
}

func TestRemoveReachesAnAccountWhoseManagerIsNotRunning(t *testing.T) {
	r := newDebRig(t, "vega/identity-0")
	r.machine(t, "vega", true, false)
	if err := os.RemoveAll(filepath.Join(r.managers, r.uids["vega"])); err != nil {
		t.Fatal(err)
	}
	_, calls := r.run(t, nil, "prerm", "remove")
	units := filepath.Join(r.home["vega"], ".config", "systemd", "user")
	for _, l := range []string{"sockets.target.wants/aii-os-machine.socket", "default.target.wants/aii-os-machine.service"} {
		if exists(filepath.Join(units, l)) {
			t.Errorf("with no manager running the enable link %s stayed", l)
		}
		want := "rm as vega -f " + filepath.Join(units, l)
		if !slices.Contains(calls, want) {
			t.Errorf("the link was not removed as the account: want %q in %q", want, calls)
		}
	}
	if !strings.Contains(r.stash(t, "vega"), "machine-enabled") {
		t.Fatal("the stash does not say the units were enabled")
	}
}

func TestReinstallRestoresTheMachineServiceItFound(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "mira/identity-0")
	r.machine(t, "vega", true, true)
	r.machine(t, "mira", false, false) // registered once, then switched off by its person
	r.run(t, nil, "prerm", "remove")
	r.run(t, nil, "postinst", "configure", "0.1.16")
	got := r.done(t)
	want := []string{"enabled " + r.uids["vega"] + " aii-os-machine.service", "enabled " + r.uids["vega"] + " aii-os-machine.socket"}
	if !slices.Equal(got, want) {
		t.Fatalf("a reinstall did %q, want only what it found enabled enabled again: %q", got, want)
	}
	if strings.Contains(r.stash(t, "vega"), "machine-enabled") {
		t.Fatal("the stash still says enabled once it was restored")
	}
	// A second configure has nothing more to restore.
	r.run(t, nil, "postinst", "configure", "0.1.16")
	if got := r.done(t); len(got) != 0 {
		t.Fatalf("a second configure did %q", got)
	}
}

func TestPurgeRemovesStoresServiceDirectoryAndUnitsAndNoIdentity(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "vega/identity-1")
	r.machine(t, "vega", true, true)
	home := r.home["vega"]
	store := r.store(t, filepath.Join(home, ".aii", "plugins"))
	writeFile(t, filepath.Join(home, ".aii", "identity-0", "data", "ledger.jsonl"), "{}\n")
	if err := os.MkdirAll(filepath.Join(home, ".aii", "by-name"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../identity-0", filepath.Join(home, ".aii", "by-name", "Vega")); err != nil {
		t.Fatal(err)
	}
	units := filepath.Join(home, ".config", "systemd", "user")
	if err := os.Symlink("/usr/lib/systemd/user/aii-os@.service", filepath.Join(units, "default.target.wants", "aii-os@identity-0.service")); err != nil {
		t.Fatal(err)
	}
	identities := treeHashes(t, home, ".aii/identity-")
	r.run(t, nil, "prerm", "remove")
	r.run(t, nil, "postrm", "remove")
	out, _ := r.run(t, nil, "postrm", "purge")
	for _, p := range []string{store, filepath.Join(home, ".aii", "machine"), filepath.Join(units, "aii-os-machine.service"), filepath.Join(units, "aii-os-machine.socket"),
		filepath.Join(units, "default.target.wants", "aii-os@identity-0.service"), r.state} {
		if exists(p) {
			t.Errorf("a purge left %s", p)
		}
	}
	if !equalMaps(treeHashes(t, home, ".aii/identity-"), identities) {
		t.Fatal("a purge changed an identity")
	}
	for _, p := range []string{filepath.Join(home, ".aii"), filepath.Join(home, ".aii", "by-name", "Vega")} {
		if !exists(p) {
			t.Errorf("a purge removed %s", p)
		}
	}
	if strings.Contains(out, "kept") {
		t.Errorf("a purge with nothing held said it kept something:\n%s", out)
	}
}

func TestPurgeKeepsAPinnedStoreAndSaysSo(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "mira/identity-0", "nova/identity-0")
	for _, u := range []string{"vega", "mira"} {
		r.machine(t, u, true, true)
	}
	pinned := r.store(t, filepath.Join(r.home["vega"], ".aii", "plugins"))
	pin, err := ps.Store{Dir: pinned}.PinView(storeViewKey) // an identity started by hand runs from it
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()
	network := r.store(t, filepath.Join(r.home["mira"], ".aii", "plugins"))
	writeFile(t, network+".fstype", "nfs\n")
	r.machine(t, "nova", true, true, filepath.Join(r.dir, "unmounted-volume", "plugins"))
	r.run(t, nil, "prerm", "remove")
	out, _ := r.run(t, nil, "postrm", "purge")
	for _, kept := range []string{pinned, network} {
		if !exists(filepath.Join(kept, "v", "fedcba9876543210.record.json")) {
			t.Errorf("the store %s was removed", kept)
		}
		if !strings.Contains(out, "kept "+kept) {
			t.Errorf("the purge did not say it kept %s:\n%s", kept, out)
		}
	}
	if !strings.Contains(out, "kept "+filepath.Join(r.dir, "unmounted-volume", "plugins")+": the directory that holds it is not there") {
		t.Errorf("a store on a volume not there was not named:\n%s", out)
	}
	for _, u := range []string{"vega", "mira", "nova"} {
		if !exists(filepath.Join(r.home[u], ".aii", "machine", "state.json")) {
			t.Errorf("%s's service directory went while a store it names stayed", u)
		}
	}
}

func TestPurgeHoldsEveryLockThroughWhatItGuards(t *testing.T) {
	r := newDebRig(t, "vega/identity-0")
	r.machine(t, "vega", true, false)
	home := r.home["vega"]
	m := filepath.Join(home, ".aii", "machine")
	store := r.store(t, filepath.Join(home, ".aii", "plugins"))
	r.run(t, nil, "prerm", "remove")
	pins := map[string]*ps.Pin{}
	pinErrs := map[string]error{}
	var service, writer *os.File
	var serviceErr, writerErr error
	env, wait := r.window(store, func() {
		st := ps.Store{Dir: store}
		pins["root"], pinErrs["root"] = st.PinRoot(storeRootKey)
		pins["view"], pinErrs["view"] = st.PinView(storeViewKey)
		service, serviceErr = holdLock(filepath.Join(m, "machine.lock"))
		writer, writerErr = holdLock(filepath.Join(store, ps.LockFile))
	})
	defer func() {
		for _, p := range pins {
			p.Close()
		}
		for _, f := range []*os.File{service, writer} {
			if f != nil {
				f.Close()
			}
		}
	}()
	out, _ := r.run(t, env, "postrm", "purge")
	if !wait() {
		t.Fatalf("rig: the purge never removed anything inside the store:\n%s", out)
	}
	for what, key := range map[string]string{"root": "r/" + storeRootKey, "view": "v/" + storeViewKey} {
		if !errors.Is(pinErrs[what], ps.ErrCollecting) {
			t.Errorf("a reader pinned the store's %s between the purge's probe and its removal: %v", what, pinErrs[what])
		}
		if pinErrs[what] == nil && !exists(filepath.Join(store, key)) {
			t.Errorf("the purge removed the store's %s under that pin:\n%s", what, out)
		}
	}
	if !errors.Is(serviceErr, syscall.EWOULDBLOCK) {
		t.Errorf("a machine service took its lock while the purge removed its stores: %v", serviceErr)
	}
	if !errors.Is(writerErr, syscall.EWOULDBLOCK) {
		t.Errorf("a competing writer took the store's lock while the purge removed the store: %v", writerErr)
	}
	if serviceErr == nil && !exists(filepath.Join(m, "state.json")) {
		t.Errorf("the purge removed the service's state under its running service:\n%s", out)
	}
	if serviceErr != nil && pinErrs["root"] != nil && pinErrs["view"] != nil && (exists(store) || exists(m)) {
		t.Errorf("with nothing held but by the purge itself, the store or the service directory stayed:\n%s", out)
	}
}

// A MACHINE SERVICE THAT HOLDS ITS LOCK KEEPS ITS ACCOUNT WHOLE, as
// machine.Remove refuses while one runs (ErrServiceRuns): one started by
// hand, or one the remove could not stop. Its stores and its state stay,
// and the purge says why; another account's purge goes on.
func TestPurgeKeepsAnAccountWhoseMachineServiceHoldsItsLock(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "mira/identity-0")
	for _, u := range []string{"vega", "mira"} {
		r.machine(t, u, true, false)
		r.store(t, filepath.Join(r.home[u], ".aii", "plugins"))
	}
	r.run(t, nil, "prerm", "remove")
	m := filepath.Join(r.home["vega"], ".aii", "machine")
	service, err := holdLock(filepath.Join(m, "machine.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	out, _ := r.run(t, nil, "postrm", "purge")
	for _, p := range []string{filepath.Join(m, "state.json"), filepath.Join(m, "machine.lock"),
		filepath.Join(r.home["vega"], ".aii", "plugins", "v", storeViewKey+".record.json")} {
		if !exists(p) {
			t.Errorf("a purge removed %s while the account's machine service held its lock", p)
		}
	}
	if !strings.Contains(out, "kept "+m+" and every store it names: a machine service") {
		t.Errorf("the purge did not say why it kept the account:\n%s", out)
	}
	if !exists(filepath.Join(r.state, "accounts", r.uids["vega"])) {
		t.Errorf("the kept account's stash went:\n%s", out)
	}
	if exists(filepath.Join(r.home["mira"], ".aii", "plugins")) || exists(filepath.Join(r.home["mira"], ".aii", "machine")) {
		t.Errorf("one account's running service kept another's store or service directory:\n%s", out)
	}
}

func TestPurgeRemovesAPlacedStoreStashedAtRemoveAndNothingElse(t *testing.T) {
	r := newDebRig(t, "vega/identity-0")
	elsewhere := filepath.Join(r.dir, "big-volume")
	placed := r.store(t, filepath.Join(elsewhere, "plugins"))
	beside := filepath.Join(elsewhere, "photos", "a.jpg")
	writeFile(t, beside, "the person's")
	writeFile(t, filepath.Join(placed, "notes.txt"), "the person's too")
	r.machine(t, "vega", true, true, placed)
	r.run(t, nil, "prerm", "remove")
	out, _ := r.run(t, nil, "postrm", "purge")
	for _, own := range []string{".lock", "a", "b", "r", "v", ".trash"} {
		if exists(filepath.Join(placed, own)) {
			t.Errorf("the placed store's %s stayed", own)
		}
	}
	for _, theirs := range []string{beside, filepath.Join(placed, "notes.txt")} {
		if !exists(theirs) {
			t.Errorf("a purge removed %s, which the store did not make", theirs)
		}
	}
	if !strings.Contains(out, "kept "+placed+": it holds names the store did not make") {
		t.Errorf("the purge did not say what stayed:\n%s", out)
	}
}

func TestPurgeNeverRemovesAsRootInAHome(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "mira/identity-0")
	r.machine(t, "vega", true, true)
	r.machine(t, "mira", true, false)
	r.store(t, filepath.Join(r.home["vega"], ".aii", "plugins"))
	r.store(t, filepath.Join(r.home["mira"], ".aii", "plugins"))
	r.run(t, nil, "prerm", "remove")
	_, calls := r.run(t, nil, "postrm", "purge")
	n := 0
	for _, c := range calls {
		if !strings.HasPrefix(c, "rm as ") && !strings.HasPrefix(c, "rmdir as ") && !strings.HasPrefix(c, "flock as ") {
			continue
		}
		if strings.HasPrefix(c, "flock as root ") {
			t.Errorf("%q: a lock in a home is taken as its account, never as root", c)
		}
		n++
		for user, home := range r.home {
			if strings.Contains(c, home+string(filepath.Separator)) || strings.HasSuffix(c, home) {
				if !strings.Contains(c, " as "+user+" ") {
					t.Errorf("%q acts in %s's home as someone else", c, user)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("rig: the purge removed nothing at all")
	}
}

func TestPurgeWalksAHomeOutsideHome(t *testing.T) {
	r := newDebRig(t)
	outside := filepath.Join(r.dir, "srv", "lyra")
	r.account(t, "lyra", outside)
	r.machine(t, "lyra", true, true)
	store := r.store(t, filepath.Join(outside, ".aii", "plugins"))
	r.run(t, nil, "prerm", "remove")
	if got := r.done(t); len(got) != 2 {
		t.Fatalf("a remove did not reach a home outside /home: %q", got)
	}
	r.run(t, nil, "postrm", "purge")
	if exists(store) || exists(filepath.Join(outside, ".aii", "machine")) {
		t.Fatal("a purge did not reach a home outside /home")
	}
}

// A PURGE KEEPS THE SERVICE'S STATE WHEN WHERE ITS STORES LIE IS NOT
// KNOWN: aii machine store -list refused at remove (a state that does not
// read), or no remove stashed anything. The default store goes; the state,
// which may name another, stays and is named.
func TestPurgeKeepsTheServiceStateWhenItsStoresAreNotKnown(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "mira/identity-0")
	for _, u := range []string{"vega", "mira"} {
		r.machine(t, u, true, true)
		r.store(t, filepath.Join(r.home[u], ".aii", "plugins"))
	}
	writeFile(t, filepath.Join(r.home["vega"], ".aii-store-list-fails"), "")
	r.run(t, nil, "prerm", "remove")
	if st := r.stash(t, "vega"); !strings.Contains(st, "stores-unknown") {
		t.Fatalf("a refused store list is not stashed as unknown: %q", st)
	}
	if err := os.Remove(filepath.Join(r.state, "accounts", r.uids["mira"])); err != nil {
		t.Fatal(err)
	}
	out, _ := r.run(t, nil, "postrm", "purge")
	for _, u := range []string{"vega", "mira"} {
		if exists(filepath.Join(r.home[u], ".aii", "plugins")) {
			t.Errorf("%s's default store stayed", u)
		}
		m := filepath.Join(r.home[u], ".aii", "machine")
		if !exists(filepath.Join(m, "state.json")) || !strings.Contains(out, "kept "+m+": where its stores lie was not read") {
			t.Errorf("%s's state was not kept and named:\n%s", u, out)
		}
	}
}

// A HOME ITS OWN ACCOUNT DOES NOT OWN IS NOT WALKED: a directory under
// /home whose owner's entry names another home is nobody's to act in.
func TestAHomeNotItsAccountsIsNotWalked(t *testing.T) {
	r := newDebRig(t, "vega/identity-0")
	r.machine(t, "vega", true, true)
	stray := filepath.Join(r.homes, "stray")
	writeFile(t, filepath.Join(stray, ".uid"), r.uids["vega"]+"\n") // vega's uid owns it; vega's home is elsewhere
	writeFile(t, filepath.Join(stray, ".aii", "machine", "state.json"), "{}")
	strayStore := r.store(t, filepath.Join(stray, ".aii", "plugins"))
	r.run(t, nil, "prerm", "remove")
	r.run(t, nil, "postrm", "purge")
	if !exists(filepath.Join(strayStore, ".lock")) || !exists(filepath.Join(stray, ".aii", "machine", "state.json")) {
		t.Fatal("a purge acted in a home its account's entry does not name")
	}
}

func TestAPurgeWhoseRemovalFailsKeepsTheStateAndTheStashItNeeds(t *testing.T) {
	r := newDebRig(t, "vega/identity-0", "mira/identity-0")
	placed := r.store(t, filepath.Join(r.dir, "big-volume", "plugins"))
	r.machine(t, "vega", true, false, placed)
	r.machine(t, "mira", true, false)
	r.store(t, filepath.Join(r.home["mira"], ".aii", "plugins"))
	r.run(t, nil, "prerm", "remove")
	out, _ := r.run(t, []string{"AII_RIG_RM_FAILS=" + filepath.Join(placed, "b")}, "postrm", "purge")
	m := filepath.Join(r.home["vega"], ".aii", "machine")
	if !exists(filepath.Join(placed, "b", strings.Repeat("ab", 32))) {
		t.Fatal("rig: the refused removal removed the blob")
	}
	if !strings.Contains(out, "kept "+placed+": "+filepath.Join(placed, "b")+", the store's own, could not be removed") {
		t.Errorf("the purge did not say which of the store's own names stayed:\n%s", out)
	}
	if strings.Contains(out, "kept "+placed+": it holds names the store did not make") {
		t.Errorf("the purge called the store's own blob a name the store did not make:\n%s", out)
	}
	if !exists(filepath.Join(m, "state.json")) || !strings.Contains(out, "kept "+m+": a store it names is kept") {
		t.Errorf("the service's state, the only record of where the placed store lies, was not kept and named:\n%s", out)
	}
	if st, err := os.ReadFile(filepath.Join(r.state, "accounts", r.uids["vega"])); err != nil || !strings.Contains(string(st), "store "+placed+"\n") {
		t.Errorf("the stash that names the placed store went (%v):\n%s", err, out)
	}
	if exists(filepath.Join(r.state, "accounts", r.uids["mira"])) || exists(filepath.Join(r.home["mira"], ".aii", "machine")) {
		t.Errorf("an account whose purge finished kept its stash or its service directory:\n%s", out)
	}

	// The service's own names: one that cannot go keeps state.json.
	s := newDebRig(t, "vega/identity-0")
	s.machine(t, "vega", true, false)
	s.store(t, filepath.Join(s.home["vega"], ".aii", "plugins"))
	m = filepath.Join(s.home["vega"], ".aii", "machine")
	writeFile(t, filepath.Join(m, "relaunch.json"), "{}")
	s.run(t, nil, "prerm", "remove")
	out, _ = s.run(t, []string{"AII_RIG_RM_FAILS=" + filepath.Join(m, "relaunch.json")}, "postrm", "purge")
	if !exists(filepath.Join(m, "state.json")) || !strings.Contains(out, "kept "+m+": "+filepath.Join(m, "relaunch.json")+", the service's own, could not be removed") {
		t.Errorf("a service name that could not be removed did not keep the state and say so:\n%s", out)
	}
	if !exists(filepath.Join(s.state, "accounts", s.uids["vega"])) {
		t.Errorf("the stash went while the service's state stayed:\n%s", out)
	}
}

// THE PACKAGE'S POSTRM CARRIES THE HELPER WHOLE, AS BUILT: build-deb.sh
// puts aii-units.sh in place of postrm's marker line (postrm runs after the
// installed helper is gone), so the helper has one source. Its own
// function is run here on the tree's files.
func TestThePackagesPostrmCarriesTheHelperAsBuilt(t *testing.T) {
	build := readPackaging(t, "deb", "build-deb.sh")
	start := strings.Index(build, "aii_units_into_postrm() {")
	end := strings.Index(build[start:], "\n}\n")
	if start < 0 || end < 0 || !strings.Contains(build, `aii_units_into_postrm > "$stage/DEBIAN/postrm"`) {
		t.Fatal("build-deb.sh does not build postrm with the helper in it")
	}
	if strings.Contains(stripComments(build), "packaging/deb/postrm \"$stage") {
		t.Fatal("build-deb.sh copies postrm as it is, without the helper")
	}
	fn := build[start : start+end+3]
	cmd := exec.Command("sh", "-c", fn+"aii_units_into_postrm")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	helper := readPackaging(t, "deb", "aii-units.sh")
	if strings.Contains(string(out), "#AII_UNITS_SH#") || !strings.Contains(string(out), helper) {
		t.Fatalf("the built postrm does not carry the helper whole in place of its marker:\n%s", out)
	}
	if err := exec.Command("sh", "-n", "-c", string(out)).Run(); err != nil {
		t.Fatalf("the built postrm does not parse: %v", err)
	}

	// A postrm without its marker is refused, not built: the first 0.1.16
	// build's derivation took the line out as a comment, and its postrm
	// called aii_each_home and defined nothing.
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, "packaging", "deb"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(bare, "packaging", "deb", "postrm"), strings.Replace(readPackaging(t, "deb", "postrm"), "#AII_UNITS_SH#\n", "", 1))
	writeFile(t, filepath.Join(bare, "packaging", "deb", "aii-units.sh"), helper)
	cmd = exec.Command("sh", "-c", fn+"aii_units_into_postrm")
	cmd.Dir = bare
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "#AII_UNITS_SH# line exactly once") {
		t.Fatalf("a postrm without its marker was built: %v\n%s", err, out)
	}
}
