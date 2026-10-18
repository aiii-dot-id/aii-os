package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// offRig is a host as the operator's switch meets it: a home in the stock
// layout (its data directory inside it, kept as a place), a granted root,
// an ordinary folder outside every root, and the places the host keeps
// outside the home, each in a folder of its own. kept is every file a wall
// keeps, by name or by place; each holds the word KEPT. Everything else
// that carries MARK is open.
type offRig struct {
	r                           *Registry
	policy                      *firewall.Policy
	base, home, work, elsewhere string
	kept                        []string
}

func newOffRig(t *testing.T) offRig {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := offRig{base: base, home: filepath.Join(base, "vega"), work: filepath.Join(base, "work"), elsewhere: filepath.Join(base, "elsewhere")}
	put := func(body string, parts ...string) string {
		t.Helper()
		p := filepath.Join(parts...)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	keep := func(parts ...string) string {
		p := put("KEPT MARK\n", parts...)
		g.kept = append(g.kept, p)
		return p
	}
	put("home note MARK\n", g.home, "notes.txt")
	put("work MARK\n", g.work, "proj", "main.go")
	put("open MARK\n", g.elsewhere, "open.txt")

	// In the home: the data directory, a place; and a name, anywhere.
	keep(g.home, "data", "credentials", "k.key")
	keep(g.home, "sub", "ledger.jsonl")
	// Outside every root: a name; and the host's places, wherever they lie.
	keep(g.elsewhere, "providers.json")
	key := keep(base, "keys", "identity.sec")
	db := keep(base, "store", "aii.db")
	config := keep(base, "etc", "config.json")
	keep(base, "machine", "state.json")
	binary := keep(base, "bin", "aii-host")
	profile := keep(base, "creds", "token.json")

	place := func(id, path string) *firewall.Rule {
		return &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: path, Reason: "kept", Enforced: true}
	}
	g.policy = firewall.DefaultPolicy()
	g.policy.SetPlaces([]*firewall.Rule{
		place("sub.data", filepath.Join(g.home, "data")),
		place("sub.key", key), place("sub.db", db), place("sub.config", config),
		place("sub.machine", filepath.Join(base, "machine")),
		place("sub.binary", binary), place("sub.profile", profile),
	})
	g.r = NewRegistry(g.home, g.policy, Timeouts{})
	g.r.SetExtraRoots([]string{g.work})
	if _, extra := g.r.Roots(); len(extra) != 1 {
		t.Fatalf("the rig's granted root was refused: %v", extra)
	}
	return g
}

// WITH THE SANDBOX OFF THE IDENTITY'S OWN TOOLS REACH BEYOND EVERY ROOT.
// The operator's switch (Settings → Sandbox) lifts the boundary for read,
// write, edit, grep and ls: a path outside the home and every granted
// root, refused while the sandbox is on, is read and written with it off,
// and refused again the moment it is turned back on. Nothing is asked of
// a restart.
func TestWithTheSandboxOffTheIdentitysToolsReachBeyondEveryRoot(t *testing.T) {
	g := newOffRig(t)
	open := filepath.Join(g.elsewhere, "open.txt")
	fresh := filepath.Join(g.elsewhere, "made", "new.txt")
	outside := func(res Result) bool {
		return res.Refused && res.ReasonCode == ReasonPathRefused && strings.Contains(res.Error, ErrOutsideSandbox.Error())
	}

	if g.r.SandboxOff() {
		t.Fatal("a registry nobody switched has its sandbox on")
	}
	for tool, args := range map[string]map[string]any{
		"read":  {"file_path": open},
		"write": {"file_path": fresh, "content": "x"},
		"edit":  {"file_path": open, "old_string": "open", "new_string": "opened"},
		"grep":  {"pattern": "MARK", "path": g.elsewhere},
		"ls":    {"path": g.elsewhere},
	} {
		if res := modelCall(t, g.r, tool, args); !outside(res) {
			t.Fatalf("with the sandbox on, %s outside every root is refused as outside: %+v", tool, res)
		}
	}

	g.r.SetSandboxOff(true)
	if !g.r.SandboxOff() {
		t.Fatal("the switch did not take")
	}
	if res := modelCall(t, g.r, "read", map[string]any{"file_path": open}); res.Error != "" || !strings.Contains(res.Output, "open MARK") {
		t.Fatalf("read outside every root: %+v", res)
	}
	if res := write(t, g.r, map[string]any{"file_path": fresh, "content": "written outside"}); res.Error != "" || body(t, fresh) != "written outside" {
		t.Fatalf("write outside every root: %+v, file %q", res, body(t, fresh))
	}
	if res := modelCall(t, g.r, "edit", map[string]any{"file_path": fresh, "old_string": "written", "new_string": "edited"}); res.Error != "" || body(t, fresh) != "edited outside" {
		t.Fatalf("edit outside every root: %+v, file %q", res, body(t, fresh))
	}
	if res := modelCall(t, g.r, "grep", map[string]any{"pattern": "open M", "path": g.elsewhere}); res.Error != "" || !strings.Contains(res.Output, "open MARK") {
		t.Fatalf("grep outside every root: %+v", res)
	}
	if res := modelCall(t, g.r, "ls", map[string]any{"path": g.elsewhere}); res.Error != "" || !strings.Contains(res.Output, "open.txt") {
		t.Fatalf("ls outside every root: %+v", res)
	}
	moved := filepath.Join(g.elsewhere, "made", "moved.txt")
	if res := write(t, g.r, map[string]any{"file_path": fresh, "action": "move", "to": moved}); res.Error != "" || body(t, moved) != "edited outside" || !gone(fresh) {
		t.Fatalf("move outside every root: %+v", res)
	}
	if res := write(t, g.r, map[string]any{"file_path": moved, "action": "delete"}); res.Error != "" || !gone(moved) {
		t.Fatalf("delete outside every root: %+v", res)
	}
	// The home and the granted root are what they were.
	if res := modelCall(t, g.r, "read", map[string]any{"file_path": "notes.txt"}); !strings.Contains(res.Output, "home note") {
		t.Fatalf("the home with the sandbox off: %+v", res)
	}
	if res := modelCall(t, g.r, "read", map[string]any{"file_path": filepath.Join(g.work, "proj", "main.go")}); !strings.Contains(res.Output, "work MARK") {
		t.Fatalf("the granted root with the sandbox off: %+v", res)
	}

	g.r.SetSandboxOff(false)
	if res := modelCall(t, g.r, "read", map[string]any{"file_path": open}); !outside(res) {
		t.Fatalf("turned back on, the boundary holds at once: %+v", res)
	}
}

// WITH THE SANDBOX OFF THE SUBSTRATE IS STILL REFUSED TO EVERY TOOL. The
// switch lifts the boundary and nothing else. The wall around the
// identity's own record, keys, store, config and data directory was never
// the boundary's: it is the floor's, a name in the home and a place
// wherever it lies, and every one of them is refused with the sandbox off
// exactly as with it on. Every place the floor names is tried (the list a
// grant is held to, rootExposesSubstrate), with each tool and each of
// write's actions, and a walk from a folder that holds them all names
// none of them and reads none.
func TestWithTheSandboxOffTheSubstrateIsStillRefusedToEveryTool(t *testing.T) {
	g := newOffRig(t)
	g.r.SetSandboxOff(true)
	open := filepath.Join(g.elsewhere, "open.txt")
	protected := func(res Result) bool {
		return res.Refused && res.ReasonCode == ReasonPathRefused && strings.Contains(res.Error, "protected path")
	}

	// The rig tries every place the floor names.
	for _, place := range g.policy.Places() {
		covered := false
		for _, f := range g.kept {
			covered = covered || within(place, f)
		}
		if !covered {
			t.Fatalf("no kept file of the rig lies in the place %s", place)
		}
	}

	for _, f := range g.kept {
		for name, call := range map[string]struct {
			tool string
			args map[string]any
		}{
			"read":         {"read", map[string]any{"file_path": f}},
			"write":        {"write", map[string]any{"file_path": f, "content": "SPRAY"}},
			"delete":       {"write", map[string]any{"file_path": f, "action": "delete"}},
			"move away":    {"write", map[string]any{"file_path": f, "action": "move", "to": filepath.Join(g.elsewhere, "taken")}},
			"copy away":    {"write", map[string]any{"file_path": f, "action": "copy", "to": filepath.Join(g.elsewhere, "taken")}},
			"move onto":    {"write", map[string]any{"file_path": open, "action": "move", "to": f}},
			"copy onto":    {"write", map[string]any{"file_path": open, "action": "copy", "to": f}},
			"edit":         {"edit", map[string]any{"file_path": f, "old_string": "KEPT", "new_string": "SPRAY"}},
			"grep":         {"grep", map[string]any{"pattern": "KEPT", "path": f}},
			"ls":           {"ls", map[string]any{"path": f}},
			"ls of folder": {"ls", map[string]any{"path": filepath.Dir(f)}},
		} {
			res := modelCall(t, g.r, call.tool, call.args)
			if name == "ls of folder" {
				// The folder may be open; what it lists never names the kept file.
				if strings.Contains(res.Output, filepath.Base(f)) {
					t.Errorf("%s: a listing of its folder names it: %+v", f, res)
				}
				continue
			}
			if !protected(res) || strings.Contains(res.Output, "KEPT") {
				t.Errorf("%s of %s must be refused as a protected path with the sandbox off: %+v", name, f, res)
			}
		}
		if body(t, f) != "KEPT MARK\n" {
			t.Fatalf("%s was changed: %q", f, body(t, f))
		}
	}
	if body(t, open) != "open MARK\n" || !gone(filepath.Join(g.elsewhere, "taken")) {
		t.Fatal("a refused move or copy changed something")
	}
	// The kept directories themselves, listed or searched.
	for _, dir := range []string{filepath.Join(g.home, "data"), filepath.Join(g.home, "data", "credentials"), filepath.Join(g.base, "machine")} {
		for _, tool := range []string{"ls", "grep"} {
			if res := modelCall(t, g.r, tool, map[string]any{"path": dir, "pattern": "KEPT"}); !protected(res) {
				t.Errorf("%s of the kept directory %s: %+v", tool, dir, res)
			}
		}
	}

	// A walk from the folder that holds the home and every place: it is
	// open to the file tools, which ask about each file they reach.
	res := modelCall(t, g.r, "grep", map[string]any{"pattern": "MARK", "path": g.base})
	if res.Error != "" || strings.Contains(res.Output, "KEPT") {
		t.Fatalf("a search from above every place read a kept file: %+v", res)
	}
	for _, want := range []string{"open MARK", "home note MARK", "work MARK", "protected by the substrate floor"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("the search from above must show %q:\n%s", want, res.Output)
		}
	}
	res = modelCall(t, g.r, "ls", map[string]any{"path": g.base, "recursive": true})
	if res.Error != "" || !strings.Contains(res.Output, "open.txt") {
		t.Fatalf("a listing from above every place: %+v", res)
	}
	for _, name := range []string{"k.key", "ledger.jsonl", "providers.json", "identity.sec", "aii.db", "state.json", "aii-host", "token.json"} {
		if strings.Contains(res.Output, name) {
			t.Errorf("the listing from above names the kept %s:\n%s", name, res.Output)
		}
	}
	// A delete of a folder that holds a kept file takes nothing; one that
	// holds the home is refused before anything is surveyed.
	keys := filepath.Join(g.base, "keys")
	if res := write(t, g.r, map[string]any{"file_path": keys, "action": "delete", "recursive": true}); !strings.Contains(res.Error, "protected by the substrate floor; nothing was deleted") {
		t.Fatalf("deleting the folder that holds the key: %+v", res)
	}
	if res := write(t, g.r, map[string]any{"file_path": g.base, "action": "delete", "recursive": true}); !res.Refused || res.ReasonCode != ReasonRootInside {
		t.Fatalf("deleting the folder that holds the home: %+v", res)
	}
	for _, f := range g.kept {
		if body(t, f) != "KEPT MARK\n" {
			t.Fatalf("%s did not survive: %q", f, body(t, f))
		}
	}
}

// THE SWITCH IS NOT A PLUGIN'S. Admit is the rule a plugin's files are
// held to (broker.Sandbox), and it answers the same with the sandbox off
// as with it on: the home and the granted roots, nothing beyond them.
// Only the identity's own tools, which ask admitOwn, follow the switch.
func TestAdmitHoldsToTheRootsWhetherTheSandboxIsOnOrOff(t *testing.T) {
	g := newOffRig(t)
	open := filepath.Join(g.elsewhere, "open.txt")
	for _, off := range []bool{false, true} {
		g.r.SetSandboxOff(off)
		for _, path := range []string{open, g.elsewhere, g.base, filepath.Join(g.base, "machine", "state.json")} {
			if _, _, err := g.r.Admit("org.example.reader fs.read", path); !errors.Is(err, ErrOutsideSandbox) {
				t.Fatalf("sandbox off=%v: Admit(%s) must stay outside the sandbox, got %v", off, path, err)
			}
		}
		for path, want := range map[string]string{"notes.txt": g.home, filepath.Join(g.work, "proj", "main.go"): g.work} {
			if dir, _, err := g.r.Admit("org.example.reader fs.read", path); err != nil || dir != want {
				t.Fatalf("sandbox off=%v: Admit(%s) = %q %v, want it held by %s", off, path, dir, err, want)
			}
		}
		var sub *SubstrateError
		if _, _, err := g.r.Admit("org.example.reader fs.read", "data/credentials/k.key"); !errors.As(err, &sub) {
			t.Fatalf("sandbox off=%v: a kept place in the home: %v", off, err)
		}
	}
	// The identity's own door, by contrast, follows the switch (on now off).
	if _, _, err := g.r.admitOwn(g.r.policy, "read", open); err != nil {
		t.Fatalf("the identity's own door with the sandbox off: %v", err)
	}
	var sub *SubstrateError
	if _, _, err := g.r.admitOwn(g.r.policy, "read", filepath.Join(g.base, "machine", "state.json")); !errors.As(err, &sub) || sub.Rule.ID != "sub.machine" {
		t.Fatalf("the identity's own door keeps a place outside the home: %v", err)
	}
	g.r.SetSandboxOff(false)
	if _, _, err := g.r.admitOwn(g.r.policy, "read", open); !errors.Is(err, ErrOutsideSandbox) {
		t.Fatalf("the identity's own door with the sandbox on is Admit: %v", err)
	}
}

// WHILE SAFE HOLDS THE SANDBOX IS ON, AT EVERY CHECK. An identity whose
// record cannot be trusted keeps SAFE's read-only surface, and that
// surface is the home and the granted roots, not the whole account: with
// the operator's switch off and SAFE holding, read, grep and ls are
// refused a path outside every root and the shell's wall refuses one,
// from the first check after SAFE began. The switch itself is not moved:
// out of SAFE it is in force again.
func TestWhileSAFEHoldsTheSandboxIsOnAtEveryCheck(t *testing.T) {
	g := newOffRig(t)
	open := filepath.Join(g.elsewhere, "open.txt")
	safe := false
	g.r.SetSafeSource(func() (string, bool) { return "the record did not verify", safe })
	g.r.SetSandboxOff(true)
	if res := modelCall(t, g.r, "read", map[string]any{"file_path": open}); !strings.Contains(res.Output, "open MARK") || !g.r.SandboxOff() {
		t.Fatalf("out of SAFE, with the sandbox off: %+v", res)
	}

	safe = true
	if g.r.SandboxOff() {
		t.Fatal("while SAFE holds the sandbox is on whatever the switch says")
	}
	for tool, args := range map[string]map[string]any{
		"read": {"file_path": open},
		"grep": {"pattern": "MARK", "path": g.elsewhere},
		"ls":   {"path": g.elsewhere},
	} {
		if res := modelCall(t, g.r, tool, args); !res.Refused || !strings.Contains(res.Error, ErrOutsideSandbox.Error()) {
			t.Errorf("in SAFE %s outside every root must be refused as outside: %+v", tool, res)
		}
	}
	if why := g.r.shellRefusal(`cat "` + open + `"`); !strings.Contains(why, "is outside the sandbox") {
		t.Errorf("in SAFE the shell's wall holds the boundary: %q", why)
	}
	if res := modelCall(t, g.r, "read", map[string]any{"file_path": "notes.txt"}); !strings.Contains(res.Output, "home note") {
		t.Fatalf("SAFE's read-only surface is still the home: %+v", res)
	}

	safe = false
	if res := modelCall(t, g.r, "read", map[string]any{"file_path": open}); !strings.Contains(res.Output, "open MARK") {
		t.Fatalf("out of SAFE the operator's switch is in force again: %+v", res)
	}
}
