package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// withProcessTable stands a process table up at dir for one test, or
// with "" takes it away: a host without one keeps no such place.
func withProcessTable(t *testing.T, dir string) {
	t.Helper()
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "no-process-table")
	}
	old := processTable
	processTable = dir
	t.Cleanup(func() { processTable = old })
}

// switchTestToken is the access token the switch tests' dashboards ask for.
const switchTestToken = "the-switch-tests-own-access-token"

// newSwitchApp is the least of a host the Sandbox card's switch moves: a
// slot in the stock layout that is the working directory, a configuration
// that saves, a machine directory of its own (never the account's), no
// process table, the registry under the host's one Ring 5 policy with its
// places named (as a start names them), and a floor that has been loaded.
// outside is a file outside the home and every root, holding "reachable".
func newSwitchApp(t *testing.T) (a *App, outside string) {
	t.Helper()
	return newSwitchAppWith(t, nil)
}

// newSwitchAppWith is newSwitchApp with the configuration changed by with
// before the host is made; home is the slot.
func newSwitchAppWith(t *testing.T, with func(cfg *Config, home string)) (a *App, outside string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "identity-0")
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	withProcessTable(t, "")
	cfg := defaultConfig()
	cfg.Tools.CWD = home
	cfg.SourcePath = filepath.Join(home, "config.json")
	cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = filepath.Join(home, "data", "identity.sec"), filepath.Join(home, "data", "ledger.jsonl"), filepath.Join(home, "data", "aii.db")
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
	if with != nil {
		with(cfg, home)
	}
	a = New(cfg)
	a.rings = ring.NewManager()
	// A dashboard that asks for its access token, as the switch requires
	// of the one that is serving. It is never started.
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	a.dashboard.SetAccessToken(true, switchTestToken)
	a.toolReg = tools.NewRegistry(home, a.ensureRing5Policy(), tools.Timeouts{})
	a.applySubstrate(*cfg)
	a.ring5Content = "PLATFORM POSTURE"
	a.loadRing5()
	outside = filepath.Join(t.TempDir(), "open.txt")
	if err := os.WriteFile(outside, []byte("reachable"), 0o600); err != nil {
		t.Fatal(err)
	}
	return a, outside
}

// THE SWITCH IS SAVED, THEN APPLIED AT ONCE, AND THE FLOOR SAYS SO. The
// operator's Allow reaches setSandboxOff: the setting is in the file, the
// identity's tools reach beyond every root without a restart, the card's
// state says off, and the identity's floor no longer tells them that
// paths outside their home fail. Turned back on, all of it is as it was,
// and the floor is the bytes it was before anyone touched the switch.
func TestTheSandboxSwitchIsSavedAppliedAtOnceAndSaidOnTheFloor(t *testing.T) {
	a, outside := newSwitchApp(t)
	floorOn := a.rings.GetContent(ring.Ring5)
	if !strings.HasPrefix(floorOn, "PLATFORM POSTURE\n\n## Your Walls") || !strings.Contains(floorOn, "\nYour file access is rooted at your home directory; paths outside fail.\n") {
		t.Fatalf("with the sandbox on the floor is the platform's text and the floor every identity had:\n%s", floorOn)
	}
	if out := toolRead(a, outside); !strings.Contains(out, "access denied: outside sandbox") {
		t.Fatalf("with the sandbox on a path outside every root is refused: %q", out)
	}
	if st, err := a.sandboxState(); err != nil || st.Off {
		t.Fatalf("the card's state with the sandbox on: %+v %v", st, err)
	}

	if err := a.setSandboxOff(true, true); err != nil {
		t.Fatalf("the operator's switch: %v", err)
	}
	if st, err := a.sandboxState(); err != nil || !st.Off || !a.toolReg.SandboxOff() || !a.configSnapshot().Tools.SandboxOff {
		t.Fatalf("off is the card's state, the registry's and the configuration's: %+v %v", st, err)
	}
	if out := toolRead(a, outside); !strings.Contains(out, "reachable") {
		t.Fatalf("with the sandbox off a path outside every root is read, live: %q", out)
	}
	floorOff := a.rings.GetContent(ring.Ring5)
	if !strings.Contains(floorOff, firewall.BoundaryLifted) || strings.Contains(floorOff, "paths outside fail") {
		t.Fatalf("the floor must say what their world now is:\n%s", floorOff)
	}
	// Every line of the floor with the sandbox on is still there, but the
	// sentence that said paths outside fail; what is added names what
	// stays closed.
	for _, line := range strings.Split(floorOn, "\n") {
		if line != "Your file access is rooted at your home directory; paths outside fail." && !strings.Contains(floorOff, line) {
			t.Fatalf("the floor lost the line %q with the sandbox off:\n%s", line, floorOff)
		}
	}
	saved, err := LoadConfig(a.cfg.SourcePath)
	if err != nil || !saved.Tools.SandboxOff {
		t.Fatalf("the setting is in the file a restart reads: %+v %v", saved, err)
	}

	if err := a.setSandboxOff(false, true); err != nil {
		t.Fatalf("turning the sandbox back on: %v", err)
	}
	if st, _ := a.sandboxState(); st.Off || a.toolReg.SandboxOff() {
		t.Fatal("the sandbox is on again")
	}
	if out := toolRead(a, outside); !strings.Contains(out, "access denied: outside sandbox") {
		t.Fatalf("on again, the boundary holds at once: %q", out)
	}
	if got := a.rings.GetContent(ring.Ring5); got != floorOn {
		t.Fatalf("on again, the floor is the bytes it was:\n%s", got)
	}
	onDisk, err := os.ReadFile(a.cfg.SourcePath)
	if err != nil || strings.Contains(string(onDisk), "sandbox_off") {
		t.Fatalf("on is the absent setting, as in every file written before the switch existed: %v\n%s", err, onDisk)
	}
}

// A SWITCH THAT CANNOT BE SAVED LEAVES THE SANDBOX ON. Saved first, then
// applied: a setting the file does not hold would be off until the next
// start and on after it, and the operator would have been told neither.
// The door answers with the error, which the card shows, and nothing in
// the running host has moved.
func TestASandboxSwitchThatCannotBeSavedLeavesTheSandboxOn(t *testing.T) {
	a, outside := newSwitchApp(t)
	floorOn := a.rings.GetContent(ring.Ring5)
	// The configuration cannot be replaced: a directory that is not empty
	// stands where the file goes. The provider table beside it still reads,
	// so what must stay closed can be named and the save is what fails.
	if err := os.MkdirAll(filepath.Join(a.cfg.SourcePath, "in-the-way"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := a.setSandboxOff(true, true)
	if err == nil || !strings.Contains(err.Error(), "sandbox switch") {
		t.Fatalf("a save that failed must be reported as the switch's failure: %v", err)
	}
	if a.toolReg.SandboxOff() || a.configSnapshot().Tools.SandboxOff {
		t.Fatal("the failed save turned the sandbox off")
	}
	if st, _ := a.sandboxState(); st.Off {
		t.Fatal("the card's state says off after a failed save")
	}
	if out := toolRead(a, outside); !strings.Contains(out, "access denied: outside sandbox") {
		t.Fatalf("the boundary holds after a failed save: %q", out)
	}
	if got := a.rings.GetContent(ring.Ring5); got != floorOn {
		t.Fatalf("the floor moved after a failed save:\n%s", got)
	}
}

func TestTheSandboxSwitchOutlastsARestartAndKeepsTheSubstrateAndAPluginsReach(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SwitchTest",
		KeyPath:    filepath.Join(data, "identity.sec"),
		LedgerPath: filepath.Join(data, "ledger.jsonl"),
		DBPath:     filepath.Join(data, "aii.db"),
	}).Ledger.Close()
	outside := filepath.Join(t.TempDir(), "open.txt")
	for p, body := range map[string]string{
		filepath.Join(home, "notes.md"):                         "mine",
		filepath.Join(data, "credentials", "profiles", "k.key"): "SECRET",
		outside: "reachable",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(home)
	withProcessTable(t, "")
	machineDir := filepath.Join(t.TempDir(), "machine") // its own, never the account's
	newCfg := func() *Config {
		cfg := defaultConfig()
		cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = "data/identity.sec", "data/ledger.jsonl", "data/aii.db"
		cfg.LLM = withTestProvider(t, home, "test", "https://127.0.0.1:1", "m", "sk-x")
		cfg.SourcePath = filepath.Join(home, "config.json")
		cfg.Dashboard.Port = 0
		cfg.Dashboard.RequireToken = true // the sandbox is off only while the dashboard asks for its token
		cfg.Tools.CWD = "."
		cfg.Machine.Dir = machineDir
		return cfg
	}
	first := New(newCfg())
	if err := startLiveForTest(first); err != nil {
		t.Fatal(err)
	}
	if out := toolRead(first, outside); !strings.Contains(out, "access denied: outside sandbox") {
		stopApp(t, first)
		t.Fatalf("a live identity's sandbox is on until the operator turns it off: %q", out)
	}
	if err := first.setSandboxOff(true, true); err != nil {
		stopApp(t, first)
		t.Fatalf("the operator's switch: %v", err)
	}
	stopApp(t, first)

	saved, err := LoadConfig(filepath.Join(home, "config.json"))
	if err != nil || !saved.Tools.SandboxOff {
		t.Fatalf("the file a restart reads holds the setting: %v", err)
	}
	cfg := newCfg()
	cfg.Tools.SandboxOff = saved.Tools.SandboxOff
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)

	if !app.toolReg.SandboxOff() {
		t.Fatal("the restarted host did not read the saved setting")
	}
	if floor := app.rings.GetContent(ring.Ring5); !strings.Contains(floor, firewall.BoundaryLifted) || strings.Contains(floor, "paths outside fail") {
		t.Fatalf("the restarted identity's floor must say the boundary is off:\n%s", floor)
	}
	if out := toolRead(app, outside); !strings.Contains(out, "reachable") {
		t.Fatalf("after the restart a path outside every root is read: %q", out)
	}
	// The substrate, in the stock layout, with the sandbox off.
	for _, spelled := range []string{"data/credentials/profiles/k.key", filepath.Join(data, "credentials", "profiles", "k.key"), "data/ledger.jsonl", "data/identity.sec", "data/aii.db", "config.json", "providers.json"} {
		if out := toolRead(app, spelled); strings.Contains(out, "SECRET") || !strings.Contains(out, "access denied: protected path") {
			t.Errorf("read of %s must be refused as substrate with the sandbox off: %q", spelled, out)
		}
		if out := toolWrite(app, spelled, "SPRAY"); !strings.Contains(out, "access denied: protected path") {
			t.Errorf("write of %s must be refused as substrate with the sandbox off: %q", spelled, out)
		}
	}
	res, _ := app.toolReg.Execute(context.Background(), "shell", map[string]any{"command": "cat data/credentials/profiles/k.key"})
	if strings.Contains(res.Output, "SECRET") || !strings.Contains(res.Error, "access denied") {
		t.Fatalf("the shell's check refuses the data directory with the sandbox off: %+v", res)
	}
	if out := toolRead(app, "notes.md"); !strings.Contains(out, "mine") {
		t.Fatalf("the home stays the identity's: %q", out)
	}
	// A plugin granted files: the door it is given, asked as the broker asks.
	door := newPluginSandbox(app.toolReg, app.configSnapshot().Identity.LedgerPath)
	if _, _, err := door.Admit("org.example.reader fs.read", outside); !errors.Is(err, tools.ErrOutsideSandbox) {
		t.Fatalf("a plugin's files stay in the home and the granted roots with the sandbox off: %v", err)
	}
	if _, _, err := door.Admit("org.example.reader fs.read", "notes.md"); err != nil {
		t.Fatalf("a plugin's files reach the home as before: %v", err)
	}
	var kept *tools.SubstrateError
	if _, _, err := door.Admit("org.example.reader fs.read", "data/credentials/profiles/k.key"); !errors.As(err, &kept) {
		t.Fatalf("a plugin's files never reach the data directory: %v", err)
	}

	// The operator edits config.json by hand: back on, live.
	edited := app.configSnapshot()
	edited.Tools.SandboxOff = false
	b, err := json.Marshal(&edited)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(edited.SourcePath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	app.reloadConfig()
	if app.toolReg.SandboxOff() {
		t.Fatal("a hand edit back to on must apply live")
	}
	if out := toolRead(app, outside); !strings.Contains(out, "access denied: outside sandbox") {
		t.Fatalf("after the hand edit the boundary holds: %q", out)
	}
	if floor := app.rings.GetContent(ring.Ring5); strings.Contains(floor, firewall.BoundaryLifted) || !strings.Contains(floor, "paths outside fail") {
		t.Fatalf("after the hand edit the floor says paths outside fail again:\n%s", floor)
	}
}

// recordSlots writes the machine service's state as it records slots, so
// a slot that is not beside this identity's own is listed too.
func recordSlots(t *testing.T, machineDir string, slots ...string) {
	t.Helper()
	if err := os.MkdirAll(machineDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var recorded []map[string]string
	for i, slot := range slots {
		recorded = append(recorded, map[string]string{"slot": slot, "identity": string(rune('a' + i)), "public_key": "k", "how": "registered", "at": "2026-10-09T00:00:00Z"})
	}
	state, err := json.Marshal(map[string]any{"identities": recorded})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(machineDir, "state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWithTheSandboxOffOtherIdentitiesAndTheProcessTableStayClosed(t *testing.T) {
	real := func(dir string) string {
		t.Helper()
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	root, other, elsewhere, proc := real(t.TempDir()), real(t.TempDir()), real(t.TempDir()), filepath.Join(real(t.TempDir()), "proc")
	home, sibling, recorded := filepath.Join(root, "identity-0"), filepath.Join(root, "identity-1"), filepath.Join(other, "identity-9")
	data := filepath.Join(home, "data")
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SiblingTest",
		KeyPath:    filepath.Join(data, "identity.sec"),
		LedgerPath: filepath.Join(data, "ledger.jsonl"),
		DBPath:     filepath.Join(data, "aii.db"),
	}).Ledger.Close()
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
	link := func(target string, parts ...string) string {
		t.Helper()
		p := filepath.Join(parts...)
		if err := os.Symlink(target, p); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
		return p
	}
	var slotFiles, procFiles []string
	for _, rel := range []string{"data/identity.sec", "data/ledger.jsonl", "data/aii.db", "data/snapshot.key", "data/credentials/anthropic.json",
		"data/backups/snap-1/aii.db", "config.json", "providers.json", "notes.md", "work/plan.txt"} {
		slotFiles = append(slotFiles, put("KEPT MARK\n", sibling, filepath.FromSlash(rel)))
	}
	slotFiles = append(slotFiles, put("KEPT MARK\n", recorded, "data", "credentials", "k.key"), put("KEPT MARK\n", recorded, "notes.md"))
	for _, rel := range []string{"918273645/environ", "918273645/task/7/environ", "77/environ"} {
		procFiles = append(procFiles, put("KEPT MARK\n", proc, filepath.FromSlash(rel)))
	}
	link("918273645", proc, "self")
	link(filepath.Join("918273645", "task", "7"), proc, "thread-self")
	open := put("open MARK\n", elsewhere, "open.txt")
	put("open MARK\n", root, "shared", "readme.txt")
	sep := string(filepath.Separator)
	// The same files, spelled the other ways a path can be: through a link
	// in a folder the identity may use, through a ".." walk, through the
	// table's own links, and by a name only the shell would resolve.
	slotFiles = append(slotFiles,
		filepath.Join(link(filepath.Join(sibling, "data", "credentials"), elsewhere, "to-credentials"), "anthropic.json"),
		filepath.Join(link(sibling, elsewhere, "to-slot"), "notes.md"),
		sibling+sep+"work"+sep+".."+sep+"data"+sep+"snapshot.key")
	// And a second name for a file inside a slot, made outside every root:
	// a hard link is that file. The wall walks the kept directories for a
	// file with more than one name (firewall.Policy.Look), and a slot is
	// one of them while the sandbox is off.
	hard := filepath.Join(elsewhere, "hard-anthropic.json")
	if err := os.Link(filepath.Join(sibling, "data", "credentials", "anthropic.json"), hard); err != nil {
		t.Fatalf("a hard link in a temporary directory: %v", err)
	}
	slotFiles = append(slotFiles, hard)
	procFiles = append(procFiles,
		filepath.Join(proc, "self", "environ"), filepath.Join(proc, "thread-self", "environ"),
		filepath.Join(link(filepath.Join(proc, "self"), elsewhere, "to-proc"), "environ"),
		link(filepath.Join(proc, "918273645", "environ"), elsewhere, "to-env"),
		proc+sep+"77"+sep+".."+sep+"self"+sep+"environ",
		filepath.Join(proc, "$PPID", "environ"))

	t.Chdir(home)
	withProcessTable(t, proc)
	machineDir := filepath.Join(t.TempDir(), "machine")
	recordSlots(t, machineDir, home, recorded)
	cfg := defaultConfig()
	cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = "data/identity.sec", "data/ledger.jsonl", "data/aii.db"
	cfg.LLM = withTestProvider(t, home, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.SourcePath = filepath.Join(home, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Dashboard.RequireToken = true // the sandbox is off only while the dashboard asks for its token
	cfg.Tools.CWD = "."
	cfg.Machine.Dir = machineDir
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	ctx := context.Background()
	call := func(tool string, args map[string]any) tools.Result {
		t.Helper()
		res, err := app.toolReg.ExecuteModel(ctx, tool, args)
		if err != nil {
			t.Fatalf("%s %v: %v", tool, args, err)
		}
		return res
	}
	shell := func(command string, cwd any) tools.Result {
		t.Helper()
		args := map[string]any{"command": command}
		if cwd != nil {
			args["cwd"] = cwd
		}
		return call("shell", args)
	}
	quoted := func(path string) string { return `"` + path + `"` }

	// The floor as any load after the start renders it: the start's own
	// load runs before the places are named, and is repeated only when
	// there are granted roots or the sandbox is off.
	app.loadRing5()
	floorOn := app.rings.GetContent(ring.Ring5)
	if strings.Contains(floorOn, "other identities") || strings.Contains(floorOn, proc) {
		t.Fatalf("with the sandbox on the floor names neither: they lie outside the boundary\n%s", floorOn)
	}
	if err := app.setSandboxOff(true, true); err != nil {
		t.Fatalf("the operator's switch: %v", err)
	}
	if res := call("read", map[string]any{"file_path": open}); !strings.Contains(res.Output, "open MARK") {
		t.Fatalf("the sandbox is off: a file outside every root is read: %+v", res)
	}
	if res := call("read", map[string]any{"file_path": filepath.Join(root, "shared", "readme.txt")}); !strings.Contains(res.Output, "open MARK") {
		t.Fatalf("a folder beside the slots that is no slot is open: %+v", res)
	}

	// Every tool, and each of write's actions, on every kept file.
	taken := filepath.Join(elsewhere, "taken")
	refused := func(f, rule string) {
		t.Helper()
		for name, c := range map[string]struct {
			tool string
			args map[string]any
		}{
			"read":      {"read", map[string]any{"file_path": f}},
			"write":     {"write", map[string]any{"file_path": f, "content": "SPRAY"}},
			"delete":    {"write", map[string]any{"file_path": f, "action": "delete"}},
			"move away": {"write", map[string]any{"file_path": f, "action": "move", "to": taken}},
			"copy away": {"write", map[string]any{"file_path": f, "action": "copy", "to": taken}},
			"move onto": {"write", map[string]any{"file_path": open, "action": "move", "to": f}},
			"copy onto": {"write", map[string]any{"file_path": open, "action": "copy", "to": f}},
			"edit":      {"edit", map[string]any{"file_path": f, "old_string": "KEPT", "new_string": "SPRAY"}},
			"grep":      {"grep", map[string]any{"pattern": "KEPT", "path": f}},
			"ls":        {"ls", map[string]any{"path": f}},
		} {
			res := call(c.tool, c.args)
			if !res.Refused || res.ReasonCode != tools.ReasonPathRefused || !strings.Contains(res.Error, "protected path ("+rule+":") || strings.Contains(res.Output, "KEPT") {
				t.Errorf("%s of %s must be refused as %s with the sandbox off: %+v", name, f, rule, res)
			}
		}
		if res := shell("cat "+quoted(f), nil); !res.Refused || strings.Contains(res.Output, "KEPT") {
			t.Errorf("the shell must be refused %s as an argument: %+v", f, res)
		}
	}
	for _, f := range slotFiles {
		refused(f, "sub.identity")
	}
	for _, f := range procFiles {
		refused(f, "sub.processes")
	}
	for _, dir := range []string{sibling, filepath.Join(sibling, "data"), recorded, proc, filepath.Join(proc, "self"), filepath.Join(proc, "918273645")} {
		for _, tool := range []string{"ls", "grep"} {
			if res := call(tool, map[string]any{"path": dir, "pattern": "KEPT"}); !res.Refused || !strings.Contains(res.Error, "protected path") {
				t.Errorf("%s of the kept directory %s: %+v", tool, dir, res)
			}
		}
	}
	for _, f := range append(slotFiles[:12:12], procFiles[:3]...) {
		if b, err := os.ReadFile(f); err != nil || string(b) != "KEPT MARK\n" {
			t.Fatalf("%s was changed: %q %v", f, b, err)
		}
	}
	if _, err := os.Lstat(taken); !os.IsNotExist(err) {
		t.Fatal("a refused move or copy took something")
	}

	// A walk from above them all, which the file tools may make: it reads
	// and names none of it.
	res := call("grep", map[string]any{"pattern": "MARK", "path": root})
	if res.Error != "" || strings.Contains(res.Output, "KEPT") || !strings.Contains(res.Output, "open MARK") {
		t.Fatalf("a search from the folder that holds every slot: %+v", res)
	}
	res = call("ls", map[string]any{"path": root, "recursive": true})
	for _, name := range []string{"identity-1", "plan.txt", "anthropic.json", "snapshot.key"} {
		if res.Error != "" || strings.Contains(res.Output, name) {
			t.Errorf("a listing from the folder that holds every slot names %s: %+v", name, res)
		}
	}
	if res := call("write", map[string]any{"file_path": other, "action": "delete", "recursive": true}); !strings.Contains(res.Error, "protected by the substrate floor; nothing was deleted") {
		t.Fatalf("deleting the folder that holds a recorded slot: %+v", res)
	}

	// The shell: a glob, the working directory, a cd, a relative walk, a
	// walk in full, a folder that holds a slot.
	for _, c := range []struct {
		why, command string
		cwd          any
	}{
		{"a glob over the slots", "cat " + quoted(filepath.Join(root, "identity-*", "notes.md")), nil},
		{"a glob over the table", "cat " + quoted(filepath.Join(proc, "*", "environ")), nil},
		{"a slot as the working directory", "cat notes.md", sibling},
		{"a folder in a slot as the working directory", "ls", filepath.Join(sibling, "work")},
		{"the table's own entry as the working directory", "cat environ", filepath.Join(proc, "self")},
		{"the folder that holds a recorded slot as the working directory", "cat identity-9/notes.md", other},
		{"a cd into a slot", "cd " + quoted(sibling) + " && cat notes.md", nil},
		{"a cd into the table", "cd " + quoted(proc) + " && cat self/environ", nil},
		{"a cd to the folder that holds a recorded slot", "cd " + quoted(other) + " && cat identity-9/notes.md", nil},
		{"a relative walk to the slot beside", "cat ../identity-1/notes.md", nil},
		{"a recursive search of the folder that holds a recorded slot", "grep -r KEPT " + quoted(other), nil},
		{"a recursive search of the folder that holds every slot", "grep -r KEPT " + quoted(root), nil},
	} {
		if res := shell(c.command, c.cwd); !res.Refused || strings.Contains(res.Output, "KEPT") {
			t.Errorf("%s (%q, cwd %v) must be refused: %+v", c.why, c.command, c.cwd, res)
		}
	}
	if res := shell("cat "+quoted(open), nil); res.Refused || res.Output != "open MARK\n" {
		t.Fatalf("the shell still runs outside every root: %+v", res)
	}

	// What the identity is told, and what it is not.
	floorOff := app.rings.GetContent(ring.Ring5)
	if strings.Count(floorOff, "- **other identities' slots** — ") != 1 || !strings.Contains(floorOff, "- **"+proc+"** — ") || !strings.Contains(floorOff, firewall.BoundaryLifted) {
		t.Fatalf("the floor names what stays closed, each once:\n%s", floorOff)
	}
	for _, leak := range []string{"identity-1", "identity-9", "918273645"} {
		if strings.Contains(floorOff, leak) {
			t.Errorf("the floor says %q, which is another identity's slot or a process's number:\n%s", leak, floorOff)
		}
	}

	// A slot made while the host runs is closed at the identity's next call.
	born := put("KEPT MARK\n", root, "identity-5", "data", "credentials", "k.key")
	var read llm.ToolCall
	read.Type = "function"
	read.Function.Name = "read"
	args, err := json.Marshal(map[string]any{"file_path": born})
	if err != nil {
		t.Fatal(err)
	}
	read.Function.Arguments = string(args)
	if obs := app.executeToolCall(ctx, read); !obs.Refused || !strings.Contains(obs.Text, "protected path (sub.identity:") || strings.Contains(obs.Text, "KEPT") {
		t.Fatalf("a slot made while the host runs must be closed at the next call: %+v", obs)
	}

	if err := app.setSandboxOff(false, true); err != nil {
		t.Fatalf("turning the sandbox back on: %v", err)
	}
	if got := app.rings.GetContent(ring.Ring5); got != floorOn {
		t.Fatalf("turned back on, the floor is the bytes it was: %q, want %q", got, floorOn)
	}
	if res := call("read", map[string]any{"file_path": filepath.Join(sibling, "notes.md")}); !strings.Contains(res.Error, "outside sandbox") {
		t.Fatalf("turned back on, the other identity's slot is outside the boundary like everything else: %+v", res)
	}
}

// A SWITCH THAT CANNOT KEEP THE OTHER IDENTITIES CLOSED IS REFUSED, AND A
// RUNNING HOST THAT NO LONGER CAN PUTS THE BOUNDARY BACK UP. Which slots
// are other identities' is read from the machine service's state. When
// that does not read, the switch is refused with a typed reason that
// names what it requires, and nothing is saved. When it stops reading in
// a host that runs with the sandbox off, the boundary is up again before
// the next call is judged, the floor says paths outside fail, and the
// card's state says why the saved setting is not in force. A start with
// the setting saved and the state unreadable begins with the sandbox on.
func TestASandboxThatCannotKeepTheOtherIdentitiesClosedStaysOn(t *testing.T) {
	if !machine.Supported {
		t.Skip("this host runs no machine service: it has no other identities to list")
	}
	a, outside := newSwitchApp(t)
	machineDir := a.cfg.Machine.Dir
	state := filepath.Join(machineDir, "state.json")
	tear := func() {
		t.Helper()
		if err := os.MkdirAll(machineDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(state, []byte("{torn"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	held := func(err error, why string) *SandboxHeldError {
		t.Helper()
		var h *SandboxHeldError
		if !errors.As(err, &h) || h.Why != why {
			t.Fatalf("want the sandbox held for %s, got %v", why, err)
		}
		return h
	}
	floorOn := a.rings.GetContent(ring.Ring5)

	tear()
	h := held(a.setSandboxOff(true, true), sandboxHeldSiblings)
	if !strings.Contains(h.Error(), "requires every other identity's slot on this account to be listed") {
		t.Fatalf("the refusal names what turning the sandbox off requires: %v", h)
	}
	if a.toolReg.SandboxOff() || a.configSnapshot().Tools.SandboxOff {
		t.Fatal("the refused switch moved something")
	}
	if _, err := os.Stat(a.cfg.SourcePath); !os.IsNotExist(err) {
		t.Fatalf("the refused switch saved something: %v", err)
	}

	// The state reads: the switch takes. Then it stops reading.
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	if err := a.setSandboxOff(true, true); err != nil || !a.toolReg.SandboxOff() {
		t.Fatalf("with the state readable the switch takes: %v", err)
	}
	if st, _ := a.sandboxState(); !st.Off || st.Held != "" || st.HeldWhy != "" {
		t.Fatalf("off and in force: %+v", st)
	}
	tear()
	a.keepSandboxOffPlaces() // what the dispatch does before every call
	if a.toolReg.SandboxOff() {
		t.Fatal("the boundary must be back up once the other identities can no longer be listed")
	}
	if out := toolRead(a, outside); !strings.Contains(out, "access denied: outside sandbox") {
		t.Fatalf("the next call is judged with the boundary up: %q", out)
	}
	if got := a.rings.GetContent(ring.Ring5); got != floorOn {
		t.Fatalf("held on, the floor says paths outside fail again:\n%s", got)
	}
	st, err := a.sandboxState()
	if err != nil || st.Off || st.HeldWhy != sandboxHeldSiblings || !strings.Contains(st.Held, "cannot be listed") || !a.configSnapshot().Tools.SandboxOff {
		t.Fatalf("the saved setting stays off and the card's state says why it is not in force: %+v %v", st, err)
	}

	// A start that reads the saved setting and cannot list them begins on.
	a.applySavedSandbox(a.configSnapshot())
	if a.toolReg.SandboxOff() {
		t.Fatal("a start with the state unreadable began with the sandbox off")
	}
	// Readable again: nothing turns it off but the operator, and the card says so.
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	a.keepSandboxOffPlaces()
	if st, _ := a.sandboxState(); st.Off || st.HeldWhy != sandboxHeldEarlier || !strings.Contains(st.Held, "Tick the box") {
		t.Fatalf("held until the operator turns it off again: %+v", st)
	}
	if err := a.setSandboxOff(true, true); err != nil || !a.toolReg.SandboxOff() {
		t.Fatalf("the operator turns it off again: %v", err)
	}
}

// A FOLDER THE OPERATOR GRANTED IS AS IT WAS, SANDBOX OFF AS ON. What is
// kept while the sandbox is off is what the boundary alone had kept. A
// folder inside another identity's slot that the operator granted with
// the sandbox on stays open with it off, and the rest of that slot is
// closed around it: turning the sandbox off refuses nothing that was
// admitted with it on, and the card is told what that leaves unkept. A
// grant made WHILE the sandbox is off is another matter: the roots door
// then asks every kept place, so that a grant is not the way to open one.
func TestAGrantedFolderIsAsItWasWithTheSandboxOff(t *testing.T) {
	if !machine.Supported {
		t.Skip("this host runs no machine service: it has no other identities to list")
	}
	a, _ := newSwitchApp(t)
	slot, err := filepath.EvalSymlinks(filepath.Dir(a.cfg.Tools.CWD))
	if err != nil {
		t.Fatal(err)
	}
	slot = filepath.Join(slot, "identity-1")
	for rel, body := range map[string]string{"work/plan.txt": "shared plan", "more/notes.txt": "more notes", "data/credentials/k.key": "KEPT"} {
		p := filepath.Join(slot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	work, more := filepath.Join(slot, "work"), filepath.Join(slot, "more")
	if err := a.setSandboxRoots([]string{work}); err != nil {
		t.Fatalf("with the sandbox on, a folder in another identity's slot is the operator's to grant: %v", err)
	}
	if out := toolRead(a, filepath.Join(work, "plan.txt")); !strings.Contains(out, "shared plan") {
		t.Fatalf("the granted folder with the sandbox on: %q", out)
	}
	if err := a.setSandboxOff(true, true); err != nil {
		t.Fatalf("a granted folder does not stand in the switch's way: %v", err)
	}
	if out := toolRead(a, filepath.Join(work, "plan.txt")); !strings.Contains(out, "shared plan") {
		t.Fatalf("the granted folder must stay open with the sandbox off: %q", out)
	}
	for _, kept := range []string{filepath.Join(slot, "data", "credentials", "k.key"), filepath.Join(more, "notes.txt")} {
		if out := toolRead(a, kept); !strings.Contains(out, "access denied: protected path (sub.identity:") {
			t.Fatalf("the rest of the other identity's slot is closed around the grant: %s: %q", kept, out)
		}
	}
	// The card is told what the grant left unkept, to name it in the
	// warning and while the sandbox is off.
	if st, err := a.sandboxState(); err != nil || !slices.Equal(st.NotKept, []string{work}) {
		t.Fatalf("the state names the granted folder that lies in a kept place: %+v %v", st, err)
	}
	// With the sandbox off the roots door asks every kept place: a folder
	// in another identity's slot, the slot whole, a folder that holds it
	// and the process table are refused in the door's own words, and
	// nothing is changed. The folder already granted is as it was.
	table := filepath.Join(t.TempDir(), "proc")
	if err := os.Mkdir(table, 0o700); err != nil {
		t.Fatal(err)
	}
	withProcessTable(t, table)
	a.applySubstrate(a.configSnapshot())
	for _, root := range []string{more, slot, filepath.Dir(slot), realPath(table)} {
		if err := a.setSandboxRoots([]string{work, root}); err == nil || !strings.Contains(err.Error(), "would expose the identity's own substrate") {
			t.Fatalf("with the sandbox off a grant of %s must be refused as a grant of a kept place is: %v", root, err)
		}
	}
	if _, roots := a.toolReg.Roots(); !slices.Equal(roots, []string{work}) {
		t.Fatalf("a refused grant changed the roots: %v", roots)
	}
	if out := toolRead(a, filepath.Join(more, "notes.txt")); !strings.Contains(out, "access denied: protected path (sub.identity:") {
		t.Fatalf("the folder refused as a root stays closed: %q", out)
	}
	if err := a.setSandboxRoots([]string{work}); err != nil {
		t.Fatalf("the folder already granted is as it was, and is taken again: %v", err)
	}
	if out := toolRead(a, filepath.Join(work, "plan.txt")); !strings.Contains(out, "shared plan") {
		t.Fatalf("the granted folder must stay open: %q", out)
	}
	// With the sandbox on again the grant is the operator's, as it was.
	if err := a.setSandboxOff(false, true); err != nil {
		t.Fatal(err)
	}
	if err := a.setSandboxRoots([]string{work, more}); err != nil {
		t.Fatalf("with the sandbox on the operator may grant a folder there: %v", err)
	}
	if out := toolRead(a, filepath.Join(more, "notes.txt")); !strings.Contains(out, "more notes") {
		t.Fatalf("the folder granted with the sandbox on is open: %q", out)
	}
	if out := toolRead(a, filepath.Join(slot, "data", "credentials", "k.key")); !strings.Contains(out, "access denied") {
		t.Fatalf("the slot's own data stays closed: %q", out)
	}
}

// ON A HOST THAT HAS A PROCESS TABLE, THE HOST'S OWN ENTRY IS CLOSED WITH
// THE SANDBOX OFF: by "self", by its number, by its thread, and with it
// every other process's entry, since another identity's host and this
// host's children are there too and a number is a thing a shell assembles.
// The read tool runs inside the host, so "/proc/self/environ" is the
// host's own environment, which the shell's scrubbed environment exists
// to keep from the identity.
func TestTheHostsOwnProcessEntryIsClosedWithTheSandboxOff(t *testing.T) {
	if _, err := os.Lstat("/proc/self/environ"); err != nil {
		t.Skipf("this host has no process table: %v", err)
	}
	a, _ := newSwitchApp(t)
	withProcessTable(t, "/proc")
	if err := a.setSandboxOff(true, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/proc/self/environ", fmt.Sprintf("/proc/%d/environ", os.Getpid()), "/proc/thread-self/environ",
		fmt.Sprintf("/proc/%d/task/%d/environ", os.Getpid(), os.Getpid()), "/proc/self/mem", "/proc/1/environ", "/proc/1/../self/environ", "/proc/cpuinfo", "/proc"} {
		if out := toolRead(a, path); !strings.Contains(out, "access denied: protected path (sub.processes:") {
			t.Errorf("read of %s must be refused as the process table: %q", path, out)
		}
	}
	if floor := a.rings.GetContent(ring.Ring5); !strings.Contains(floor, "- **/proc** — ") {
		t.Fatalf("the floor names the process table:\n%s", floor)
	}
}

// SAFE ENTERED WHILE THE HOST RUNS WITH THE SANDBOX OFF CLOSES IT AT THE
// NEXT CALL. A call already running is not stopped: a walk already
// admitted finishes, and a shell command's children run on. An identity whose record can no longer be trusted keeps SAFE's
// read-only surface, and that is the home and the granted roots: the
// next read outside every root is refused, the floor says paths outside
// fail, the card's state says SAFE holds the sandbox on and that the
// saved setting is off, and the switch is refused with a typed reason.
// The saved setting is not touched, so a start that passes begins with
// the sandbox off again.
func TestSAFEEnteredWhileRunningClosesTheSandboxAtTheNextCall(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SafeSwitch",
		KeyPath:    filepath.Join(data, "identity.sec"),
		LedgerPath: filepath.Join(data, "ledger.jsonl"),
		DBPath:     filepath.Join(data, "aii.db"),
	}).Ledger.Close()
	outside := filepath.Join(t.TempDir(), "open.txt")
	for p, body := range map[string]string{filepath.Join(home, "notes.md"): "mine", outside: "reachable"} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(home)
	withProcessTable(t, "")
	machineDir := filepath.Join(t.TempDir(), "machine")
	newCfg := func() *Config {
		cfg := defaultConfig()
		cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = "data/identity.sec", "data/ledger.jsonl", "data/aii.db"
		cfg.LLM = withTestProvider(t, home, "test", "https://127.0.0.1:1", "m", "sk-x")
		cfg.SourcePath = filepath.Join(home, "config.json")
		cfg.Dashboard.Port = 0
		cfg.Dashboard.RequireToken = true // the sandbox is off only while the dashboard asks for its token
		cfg.Tools.CWD = "."
		cfg.Machine.Dir = machineDir
		return cfg
	}
	first := New(newCfg())
	if err := startLiveForTest(first); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stopApp(t, first)
		}
	}()
	if err := first.setSandboxOff(true, true); err != nil {
		t.Fatal(err)
	}
	if out := toolRead(first, outside); !strings.Contains(out, "reachable") {
		t.Fatalf("running with the sandbox off: %q", out)
	}

	first.enterSafe("test: the record did not verify")
	if out := toolRead(first, outside); !strings.Contains(out, "access denied: outside sandbox") {
		t.Fatalf("the first read after SAFE began must be held to the roots: %q", out)
	}
	if out := toolRead(first, "notes.md"); !strings.Contains(out, "mine") {
		t.Fatalf("SAFE's read-only surface is still the home: %q", out)
	}
	if first.toolReg.SandboxOff() {
		t.Fatal("while SAFE holds the sandbox is on")
	}
	if floor := first.rings.GetContent(ring.Ring5); strings.Contains(floor, firewall.BoundaryLifted) || !strings.Contains(floor, "paths outside fail") {
		t.Fatalf("in SAFE the floor says paths outside fail:\n%s", floor)
	}
	st, err := first.sandboxState()
	if err != nil || st.Off || st.HeldWhy != sandboxHeldSafe || !strings.Contains(st.Held, "SAFE holds") || !strings.Contains(st.Held, "The saved setting is off") || !first.configSnapshot().Tools.SandboxOff {
		t.Fatalf("the card's state says SAFE holds the sandbox on, and the saved setting stays off: %+v %v", st, err)
	}
	var held *SandboxHeldError
	if err := first.setSandboxOff(true, true); !errors.As(err, &held) || held.Why != sandboxHeldSafe || first.toolReg.SandboxOff() {
		t.Fatalf("the switch is refused while SAFE holds, typed: %v", err)
	}
	stopApp(t, first)
	stopped = true

	// A start that passes: the saved setting applies again.
	saved, err := LoadConfig(filepath.Join(home, "config.json"))
	if err != nil || !saved.Tools.SandboxOff {
		t.Fatalf("SAFE did not touch the saved setting: %v", err)
	}
	cfg := newCfg()
	cfg.Tools.SandboxOff = saved.Tools.SandboxOff
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	if reason, safe := app.SafeMode(); safe {
		t.Fatalf("the restart was to pass, and began in SAFE: %s", reason)
	}
	if !app.toolReg.SandboxOff() || !strings.Contains(toolRead(app, outside), "reachable") {
		t.Fatal("a start that passes applies the saved setting again")
	}
	if st, _ := app.sandboxState(); !st.Off || st.Held != "" {
		t.Fatalf("out of SAFE nothing holds the sandbox: %+v", st)
	}
}

// A SAFE START HOLDS THE SANDBOX ON WHATEVER THE SAVED SETTING SAYS. The
// saved roots are applied in SAFE; the saved switch is not. The floor
// says paths outside fail, the card's state says why, the switch is
// refused, and turning the saved setting back on is taken.
func TestASAFEStartHoldsTheSandboxOnWhateverTheSavedSettingSays(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeStart")
	outside := filepath.Join(t.TempDir(), "open.txt")
	if err := os.WriteFile(outside, []byte("reachable"), 0o600); err != nil {
		t.Fatal(err)
	}
	withProcessTable(t, "")
	cfg := safebootConfig(t, dir, "SafeStart", keyPath, ledgerPath, dbPath)
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
	cfg.Tools.SandboxOff = true
	a := New(cfg)
	t.Cleanup(func() { stopApp(t, a) })
	a.ring5Content = "PLATFORM POSTURE"
	if err := a.startSafeBoot(safeRecord, "test: SAFE at the start"); err != nil {
		t.Fatalf("SAFE start: %v", err)
	}
	if a.toolReg.SandboxOff() {
		t.Fatal("a SAFE start began with the sandbox off")
	}
	if out := toolRead(a, outside); !strings.Contains(out, "access denied: outside sandbox") {
		t.Fatalf("in SAFE a read outside every root is refused: %q", out)
	}
	if floor := a.rings.GetContent(ring.Ring5); strings.Contains(floor, firewall.BoundaryLifted) || !strings.Contains(floor, "paths outside fail") {
		t.Fatalf("in SAFE the floor says paths outside fail:\n%s", floor)
	}
	st, err := a.sandboxState()
	if err != nil || st.Off || st.HeldWhy != sandboxHeldSafe || !strings.Contains(st.Held, "SAFE holds") || !strings.Contains(st.Held, "The saved setting is off") {
		t.Fatalf("the card's state in SAFE: %+v %v", st, err)
	}
	var held *SandboxHeldError
	if err := a.setSandboxOff(true, true); !errors.As(err, &held) || held.Why != sandboxHeldSafe {
		t.Fatalf("the switch is refused in SAFE, typed: %v", err)
	}
	if err := a.setSandboxOff(false, true); err != nil {
		t.Fatalf("turning the saved setting on is taken in SAFE: %v", err)
	}
	if st, _ := a.sandboxState(); st.Off || a.configSnapshot().Tools.SandboxOff || st.HeldWhy != sandboxHeldSafe || !strings.Contains(st.Held, "cannot be turned off") {
		t.Fatalf("in SAFE the card says the sandbox cannot be turned off, whatever is saved: %+v", st)
	}
}

// THE SANDBOX IS TURNED OFF ONLY BY A PAGE SIGNED IN WITH THE ACCESS TOKEN,
// AND IS OFF ONLY WHILE THE DASHBOARD ASKS FOR ONE. With no token asked
// for, the dashboard's socket admits any client that sets a Host and an
// Origin, and the identity's own shell is such a client: it could turn
// the sandbox off with no warning shown to anyone. So the switch is
// refused, typed, with nothing saved, while no token is asked for, and
// from a connection that did not sign in with it; the card's state says
// why by the code the card decides on, whatever the saved setting is; a
// host that stops asking for its token puts the boundary back up before
// the next tool call is judged; a start with "off" saved and no token
// asked for begins on; and asking for the token again turns nothing off
// by itself. Turning the sandbox ON is taken from any page, token or not.
func TestTheSandboxIsTurnedOffOnlyWhileTheDashboardAsksForItsAccessToken(t *testing.T) {
	a, outside := newSwitchApp(t)
	floorOn := a.rings.GetContent(ring.Ring5)
	refused := func(err error, saying string) {
		t.Helper()
		var h *SandboxHeldError
		if !errors.As(err, &h) || h.Why != sandboxHeldToken || !strings.Contains(err.Error(), saying) {
			t.Fatalf("want the switch refused for the access token, saying %q: %v", saying, err)
		}
		if a.toolReg.SandboxOff() || a.configSnapshot().Tools.SandboxOff {
			t.Fatal("a refused switch turned the sandbox off or saved the setting")
		}
		if out := toolRead(a, outside); !strings.Contains(out, "access denied: outside sandbox") {
			t.Fatalf("a refused switch opened a path outside: %q", out)
		}
	}

	// No token is asked for: refused, and the card's state says so with
	// the saved setting on.
	a.dashboard.SetAccessToken(false, switchTestToken)
	refused(a.setSandboxOff(true, true), "Ask for an access token")
	st, err := a.sandboxState()
	if err != nil || st.Off || st.HeldWhy != sandboxHeldToken || !strings.HasPrefix(st.Held, "The sandbox stays on: turning it off requires this dashboard to ask for its access token") || !strings.Contains(st.Held, "in Settings, under Dashboard, with Ask for an access token") {
		t.Fatalf("with no token asked for the card's state says the box cannot turn the sandbox off, and where to turn the token on: %+v %v", st, err)
	}
	// Turning it on asks for nothing.
	if err := a.setSandboxOff(false, false); err != nil {
		t.Fatalf("turning the sandbox on is taken from any page: %v", err)
	}

	// A token is asked for, and the page that asks did not sign in with it.
	a.dashboard.SetAccessToken(true, switchTestToken)
	refused(a.setSandboxOff(true, false), "signed in with this dashboard's access token")
	if st, _ := a.sandboxState(); st.HeldWhy != "" || st.Held != "" {
		t.Fatalf("with the token asked for nothing holds the sandbox on: %+v", st)
	}

	// Signed in: off.
	if err := a.setSandboxOff(true, true); err != nil || !a.toolReg.SandboxOff() {
		t.Fatalf("a signed-in page turns the sandbox off: %v", err)
	}
	if out := toolRead(a, outside); !strings.Contains(out, "reachable") {
		t.Fatalf("the sandbox is off: %q", out)
	}

	// The host stops asking for its token while the sandbox is off: the
	// boundary is up before the next call is judged, and the saved
	// setting is kept and said to be held.
	a.dashboard.SetAccessToken(false, switchTestToken)
	a.keepSandboxOffPlaces() // what the dispatch does before every call
	if a.toolReg.SandboxOff() {
		t.Fatal("the boundary must be back up once the dashboard asks for no token")
	}
	if out := toolRead(a, outside); !strings.Contains(out, "access denied: outside sandbox") {
		t.Fatalf("the next call is judged with the boundary up: %q", out)
	}
	if got := a.rings.GetContent(ring.Ring5); got != floorOn {
		t.Fatalf("held on, the floor says paths outside fail again:\n%s", got)
	}
	st, err = a.sandboxState()
	if err != nil || st.Off || st.HeldWhy != sandboxHeldToken || !strings.HasPrefix(st.Held, "The saved setting is off, but the sandbox stays on: turning it off requires this dashboard to ask for its access token") || !a.configSnapshot().Tools.SandboxOff {
		t.Fatalf("the saved setting stays off and the state says why it is not in force: %+v %v", st, err)
	}
	// A start that reads the saved setting with no token asked for begins on.
	a.applySavedSandbox(a.configSnapshot())
	if a.toolReg.SandboxOff() {
		t.Fatal("a start with no token asked for began with the sandbox off")
	}
	// Asked for again: nothing turns the sandbox off but the operator.
	a.dashboard.SetAccessToken(true, switchTestToken)
	a.keepSandboxOffPlaces()
	if st, _ := a.sandboxState(); st.Off || st.HeldWhy != sandboxHeldEarlier || !strings.Contains(st.Held, "Tick the box") {
		t.Fatalf("held until the operator turns it off again: %+v", st)
	}
	if err := a.setSandboxOff(true, true); err != nil || !a.toolReg.SandboxOff() {
		t.Fatalf("the operator turns it off again: %v", err)
	}
	// And on, from a page that signed in with nothing.
	if err := a.setSandboxOff(false, false); err != nil || a.toolReg.SandboxOff() || a.configSnapshot().Tools.SandboxOff {
		t.Fatalf("turning the sandbox on asks for nothing: %v", err)
	}
}
