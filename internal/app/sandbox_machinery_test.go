//go:build !windows

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// machineryRig is a host whose own machinery lies outside the identity's
// home, in places this test owns: a person's home of its own (so the
// files that start the program and the credentials the host borrows are
// never the real account's), a configuration outside the slot, trust
// roots, a worker program and a dev section the configuration names, a
// logs directory, a store the machine service is moving away from, and
// two extractions in the system's temporary directory. kept is every file
// the floor must keep with the sandbox off; made are those of them the rig
// wrote itself, each holding KEPT (the updater's files beside this test's
// binary are another test's to make or not).
type machineryRig struct {
	a                             *App
	home, person, ext, cfgDir     string
	kept, made, dirs, stems       []string
	logs                          []string
	inHome, inRoot, granted, open string
}

func newMachineryRig(t *testing.T) *machineryRig {
	t.Helper()
	real := func(dir string) string {
		t.Helper()
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	g := &machineryRig{person: real(t.TempDir()), ext: real(t.TempDir()), cfgDir: real(t.TempDir())}
	t.Setenv("HOME", g.person)
	t.Setenv("SUDO_USER", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(g.person, "run"))
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
		g.kept, g.made = append(g.kept, p), append(g.made, p)
		return p
	}
	g.granted = filepath.Join(g.ext, "granted")
	g.open = put("open MARK\n", g.ext, "elsewhere", "open.txt")

	var draining string
	g.a, _ = newSwitchAppWith(t, func(cfg *Config, home string) {
		g.home = real(home)
		// The configuration outside the slot, its set-aside copies beside it.
		cfg.SourcePath = filepath.Join(g.cfgDir, "config.json")
		keep(cfg.SourcePath + configBeforeUpdate)
		keep(cfg.SourcePath + configRolledBack)
		// What the configuration names for the host to run or trust.
		cfg.Plugins.WorkerBinary = keep(g.ext, "bin", "worker")
		cfg.Plugins.CertifierRoot = keep(g.ext, "roots", "certifier.json")
		cfg.Witness.PlatformPubkeyPath = keep(g.ext, "roots", "witness.json")
		cfg.Plugins.DevSection = &DevSectionConfig{ID: "org.example.dev", Path: filepath.Join(g.ext, "devsection")}
		keep(cfg.Plugins.DevSection.Path, "index.html")
		g.dirs = append(g.dirs, cfg.Plugins.DevSection.Path)
		// One trust root in the home and one in a granted folder: within
		// the identity's reach with the sandbox on, and so with it off.
		g.inHome = put("in the home\n", home, "roots", "reviewer.json")
		cfg.Plugins.ReviewerRoot = g.inHome
		g.inRoot = put("in a granted folder\n", g.granted, "roots", "platform.json")
		cfg.Plugins.PlatformRoot = g.inRoot
		// A store the machine service is moving away from.
		draining = filepath.Join(g.ext, "old-store")
		keep(draining, "runtime", "lib.so")
		g.dirs = append(g.dirs, draining)
		state, err := json.Marshal(map[string]any{"draining": []string{draining}})
		if err != nil {
			t.Fatal(err)
		}
		put(string(state), cfg.Machine.Dir, "state.json")
		// The logs, outside the slot: read stays open, a write is refused.
		cfg.Logs.Dir = filepath.Join(g.ext, "logs")
		g.logs = append(g.logs, put("LOG LINE MARK\n", cfg.Logs.Dir, "aii.log"),
			put("LOG LINE MARK\n", filepath.Dir(LogControlPathIn(identityHomeFromConfig(cfg.SourcePath))), "control.json"))
	})

	// What the updater keeps beside the program, by the updater's own
	// naming. None of it is made here: the program is this test's binary.
	files, stems, err := updates.Beside()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 || len(stems) < 1 {
		t.Fatalf("the updater names its lock, record and previous program, and how a staged file begins: %v %v", files, stems)
	}
	g.kept = append(g.kept, files...)
	g.stems = append(g.stems, stems...)
	// What starts the program, under the rig's own person.
	files, stems, err = install.StartFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(append([]string(nil), files...), stems...) {
		if !inside(g.person, p) {
			t.Fatalf("a start file is named outside the rig's own person, where this test must not write: %s", p)
		}
	}
	g.kept = append(g.kept, files...)
	g.stems = append(g.stems, stems...)
	// The credentials the shipped provider table has the host borrow.
	keep(g.person, ".claude", ".credentials.json")
	keep(g.person, ".codex", "auth.json")
	// The machine service's link, where it is a file of its own.
	if link, err := machine.LinkPath(g.a.cfg.Machine.Dir); err == nil && !inside(g.a.cfg.Machine.Dir, link) {
		g.kept = append(g.kept, link)
	}
	// The host's temporary extractions.
	for _, prefix := range []string{pluginhost.ArtifactDirPrefix, sections.ExtractDirPrefix} {
		dir, err := os.MkdirTemp("", prefix+"org.example.rig-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		dir = real(dir)
		keep(dir, "index.html")
		g.dirs = append(g.dirs, dir)
		g.stems = append(g.stems, filepath.Join(real(os.TempDir()), prefix))
	}
	return g
}

func (g *machineryRig) call(t *testing.T, tool string, args map[string]any) tools.Result {
	t.Helper()
	res, err := g.a.toolReg.ExecuteModel(context.Background(), tool, args)
	if err != nil {
		t.Fatalf("%s %v: %v", tool, args, err)
	}
	return res
}

func (g *machineryRig) shell(t *testing.T, command string, cwd any) tools.Result {
	t.Helper()
	args := map[string]any{"command": command}
	if cwd != nil {
		args["cwd"] = cwd
	}
	return g.call(t, "shell", args)
}

// WITH THE SANDBOX OFF THE HOST'S OWN MACHINERY STAYS CLOSED. The boundary
// was all that kept what updates the program, what a rollback runs, the
// programs beside it, the files that start it, the trust roots and worker
// program the configuration names, the credentials the host borrows, the
// configuration's set-aside copies, a store the machine service is moving
// away from, and the host's temporary extractions. An identity that could
// write one of these would choose what runs at its next start. With the
// sandbox off every one of them is refused to every tool, to each of
// write's actions and to the shell, by its path, by a name the host
// begins and the system finishes, through a link, a glob, a working
// directory, a cd and a relative walk; and the floor says so. What the
// operator had already put within the identity's reach, in the home or in
// a granted folder, is as it was. With the sandbox on nothing here is a
// place, the floor is the bytes it was, and each of these paths is
// refused as outside, in the words it always was.
func TestWithTheSandboxOffTheHostsOwnMachineryStaysClosed(t *testing.T) {
	g := newMachineryRig(t)
	a := g.a
	if err := a.setSandboxRoots([]string{g.granted}); err != nil {
		t.Fatal(err)
	}
	quoted := func(path string) string { return `"` + path + `"` }
	// Every path the floor must keep: the files, an entry under each
	// directory, and a name of the kind each stem begins.
	targets := append([]string(nil), g.kept...)
	for _, stem := range g.stems {
		// A name the stem begins, an entry under such a name, and the
		// drop-in directory a service manager reads beside a unit.
		targets = append(targets, stem+"rig-entry", filepath.Join(stem+"rig-entry", "inner"), filepath.Join(stem+".d", "override.conf"))
	}

	// On: nothing here is a place; each is outside, as it always was.
	floorOn := a.rings.GetContent(ring.Ring5)
	for _, shown := range []string{"your program's update files", "the files that start your program", "your host's temporary files"} {
		if strings.Contains(floorOn, shown) {
			t.Fatalf("with the sandbox on the floor does not name %q:\n%s", shown, floorOn)
		}
	}
	for _, f := range targets {
		if res := g.call(t, "read", map[string]any{"file_path": f}); !res.Refused || res.Error != "access denied: outside sandbox" {
			t.Fatalf("with the sandbox on %s is refused as outside, in the words it always was: %+v", f, res)
		}
		if res := g.shell(t, "cat "+quoted(f), nil); !res.Refused {
			t.Fatalf("with the sandbox on the shell refuses %s: %+v", f, res)
		}
	}

	if err := a.setSandboxOff(true, true); err != nil {
		t.Fatalf("the operator's switch: %v", err)
	}
	if res := g.call(t, "read", map[string]any{"file_path": g.open}); !strings.Contains(res.Output, "open MARK") {
		t.Fatalf("the sandbox is off: a file outside every root is read: %+v", res)
	}

	taken := filepath.Join(g.ext, "elsewhere", "taken")
	for _, f := range targets {
		for name, c := range map[string]struct {
			tool string
			args map[string]any
		}{
			"read":      {"read", map[string]any{"file_path": f}},
			"write":     {"write", map[string]any{"file_path": f, "content": "SPRAY"}},
			"delete":    {"write", map[string]any{"file_path": f, "action": "delete"}},
			"move away": {"write", map[string]any{"file_path": f, "action": "move", "to": taken}},
			"copy away": {"write", map[string]any{"file_path": f, "action": "copy", "to": taken}},
			"move onto": {"write", map[string]any{"file_path": g.open, "action": "move", "to": f}},
			"copy onto": {"write", map[string]any{"file_path": g.open, "action": "copy", "to": f}},
			"edit":      {"edit", map[string]any{"file_path": f, "old_string": "KEPT", "new_string": "SPRAY"}},
			"grep":      {"grep", map[string]any{"pattern": "KEPT", "path": f}},
			"ls":        {"ls", map[string]any{"path": f}},
		} {
			res := g.call(t, c.tool, c.args)
			if !res.Refused || res.ReasonCode != tools.ReasonPathRefused || !strings.Contains(res.Error, "protected path (sub.") || strings.Contains(res.Output, "KEPT") {
				t.Errorf("%s of %s must be refused as a kept place with the sandbox off: %+v", name, f, res)
			}
		}
		if res := g.shell(t, "cat "+quoted(f), nil); !res.Refused || strings.Contains(res.Output, "KEPT") {
			t.Errorf("the shell must be refused %s as an argument: %+v", f, res)
		}
		if res := g.shell(t, "cp "+quoted(g.open)+" "+quoted(f), nil); !res.Refused {
			t.Errorf("the shell must be refused a write onto %s: %+v", f, res)
		}
	}
	for _, f := range g.made {
		if b, err := os.ReadFile(f); err != nil || string(b) != "KEPT MARK\n" {
			t.Fatalf("%s was changed: %q %v", f, b, err)
		}
	}
	if _, err := os.Lstat(taken); !os.IsNotExist(err) {
		t.Fatal("a refused move or copy took something")
	}

	// The directories: listed, searched, stood in, changed into, reached
	// by a glob, by a relative walk, through a link.
	link := filepath.Join(g.ext, "elsewhere", "into")
	for i, dir := range g.dirs {
		for _, tool := range []string{"ls", "grep"} {
			if res := g.call(t, tool, map[string]any{"path": dir, "pattern": "KEPT"}); !res.Refused || !strings.Contains(res.Error, "protected path") {
				t.Errorf("%s of the kept directory %s: %+v", tool, dir, res)
			}
		}
		os.Remove(link)
		if err := os.Symlink(dir, link); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
		star := filepath.Join(filepath.Dir(dir), filepath.Base(dir)[:len(filepath.Base(dir))-1]+"*")
		for why, c := range map[string]struct {
			command string
			cwd     any
		}{
			"as the working directory":  {"ls", dir},
			"after a cd":                {"cd " + quoted(dir) + " && ls", nil},
			"through a glob":            {"ls " + quoted(star), nil},
			"through a link":            {"ls " + quoted(link), nil},
			"a file through the link":   {"cat " + quoted(filepath.Join(link, "index.html")), nil},
			"the folder that holds it":  {"grep -r KEPT " + quoted(filepath.Dir(dir)), nil},
			"a walk up from beside it":  {"cat ../" + filepath.Base(dir) + "/index.html", filepath.Join(filepath.Dir(dir), "rig-beside-"+string(rune('a'+i)))},
			"a cd beside and a walk up": {"cd " + quoted(filepath.Dir(dir)) + " && ls " + filepath.Base(dir), nil},
		} {
			if cwd, ok := c.cwd.(string); ok && strings.Contains(cwd, "rig-beside-") {
				if err := os.MkdirAll(cwd, 0o700); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(cwd) })
			}
			if res := g.shell(t, c.command, c.cwd); !res.Refused || strings.Contains(res.Output, "KEPT") {
				t.Errorf("%s, %s (%q, cwd %v) must be refused: %+v", dir, why, c.command, c.cwd, res)
			}
		}
		if res := g.call(t, "read", map[string]any{"file_path": filepath.Join(link, "index.html")}); !res.Refused || !strings.Contains(res.Error, "protected path") {
			t.Errorf("a file of %s read through a link in an allowed folder: %+v", dir, res)
		}
	}
	os.Remove(link)

	// The logs: the reading tools open them, every other door is refused.
	for _, f := range g.logs {
		if res := g.call(t, "read", map[string]any{"file_path": f}); res.Error != "" || !strings.Contains(res.Output, "LOG LINE MARK") {
			t.Errorf("read of the log %s stays open: %+v", f, res)
		}
		if res := g.call(t, "grep", map[string]any{"pattern": "LOG LINE", "path": filepath.Dir(f)}); res.Error != "" || !strings.Contains(res.Output, "LOG LINE MARK") {
			t.Errorf("grep of the logs in %s stays open: %+v", filepath.Dir(f), res)
		}
		if res := g.call(t, "ls", map[string]any{"path": filepath.Dir(f)}); res.Error != "" || !strings.Contains(res.Output, filepath.Base(f)) {
			t.Errorf("ls of the logs in %s stays open: %+v", filepath.Dir(f), res)
		}
		for name, args := range map[string]map[string]any{
			"write":     {"file_path": f, "content": "SPRAY"},
			"delete":    {"file_path": f, "action": "delete"},
			"move away": {"file_path": f, "action": "move", "to": taken},
			"copy onto": {"file_path": g.open, "action": "copy", "to": f},
			"a new one": {"file_path": filepath.Join(filepath.Dir(f), "forged.log"), "content": "SPRAY"},
		} {
			if res := g.call(t, "write", args); !res.Refused || !strings.Contains(res.Error, "protected path (sub.logs:") {
				t.Errorf("%s of the log %s must be refused: %+v", name, f, res)
			}
		}
		if res := g.call(t, "edit", map[string]any{"file_path": f, "old_string": "LOG", "new_string": "SPRAY"}); !res.Refused {
			t.Errorf("edit of the log %s must be refused: %+v", f, res)
		}
		if res := g.shell(t, "echo SPRAY >> "+quoted(f), nil); !res.Refused {
			t.Errorf("the shell, which cannot be told a read from a write, is refused the log %s: %+v", f, res)
		}
		if b, _ := os.ReadFile(f); string(b) != "LOG LINE MARK\n" {
			t.Fatalf("the log %s was changed: %q", f, b)
		}
	}

	// What the operator had put within reach is as it was.
	if res := g.call(t, "read", map[string]any{"file_path": g.inHome}); !strings.Contains(res.Output, "in the home") {
		t.Errorf("a trust root kept in the home is as it was with the sandbox on: %+v", res)
	}
	if res := g.call(t, "read", map[string]any{"file_path": g.inRoot}); !strings.Contains(res.Output, "in a granted folder") {
		t.Errorf("a trust root kept in a granted folder is as it was with the sandbox on: %+v", res)
	}
	if res := g.shell(t, "cat "+quoted(g.open), nil); res.Refused || res.Output != "open MARK\n" {
		t.Fatalf("the shell still runs outside every root: %+v", res)
	}

	// What the identity is told.
	floorOff := a.rings.GetContent(ring.Ring5)
	for _, shown := range []string{"your program's update files", "the files that start your program", "your operator's trust roots and worker program",
		"credentials your host borrows", "the machine service's link and stores", "your host's temporary files", "your host's logs"} {
		if strings.Count(floorOff, "**"+shown+"**") != 1 {
			t.Errorf("the floor names %q once with the sandbox off:\n%s", shown, floorOff)
		}
	}
	if strings.Count(floorOff, machineryReason) != 1 || strings.Count(floorOff, logsReason) != 1 || strings.Count(floorOff, "config.json") != 1 {
		t.Errorf("each reason is said once, and config.json's copies are kept under its own line:\n%s", floorOff)
	}
	if strings.Contains(floorOff, g.person) {
		t.Errorf("the floor names what is kept, not where the person keeps it:\n%s", floorOff)
	}

	if err := a.setSandboxOff(false, true); err != nil {
		t.Fatal(err)
	}
	if got := a.rings.GetContent(ring.Ring5); got != floorOn {
		t.Fatalf("turned back on, the floor is the bytes it was: %q, want %q", got, floorOn)
	}
	for _, f := range targets[:3] {
		if res := g.call(t, "read", map[string]any{"file_path": f}); res.Error != "access denied: outside sandbox" {
			t.Fatalf("turned back on, %s is outside again, in the words it always was: %+v", f, res)
		}
	}
}

// A SWITCH THAT CANNOT NAME THE HOST'S MACHINERY IS REFUSED. Which
// credentials the host borrows is read from the provider table. When that
// does not read, what must stay closed cannot be named, and the switch is
// refused with a typed reason that says what it requires. Nothing is saved
// and nothing moves.
func TestASwitchThatCannotNameTheHostsMachineryIsRefused(t *testing.T) {
	g := newMachineryRig(t)
	a := g.a
	providers := providerFilePath(a.cfg.SourcePath)
	if err := os.WriteFile(providers, []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	var held *SandboxHeldError
	err := a.setSandboxOff(true, true)
	if !errors.As(err, &held) || held.Why != sandboxHeldHostFiles || !strings.Contains(held.Error(), "requires the credentials the provider table has the host borrow to be named") {
		t.Fatalf("the switch must be refused, typed, naming what it requires: %v", err)
	}
	if a.toolReg.SandboxOff() || a.configSnapshot().Tools.SandboxOff {
		t.Fatal("the refused switch moved something")
	}
	if _, err := os.Stat(a.cfg.SourcePath); !os.IsNotExist(err) {
		t.Fatalf("the refused switch saved something: %v", err)
	}
	if out := toolRead(a, g.open); !strings.Contains(out, "access denied: outside sandbox") {
		t.Fatalf("the boundary holds after a refused switch: %q", out)
	}
	// The table reads again: the switch takes.
	if err := os.Remove(providers); err != nil {
		t.Fatal(err)
	}
	if err := a.setSandboxOff(true, true); err != nil || !a.toolReg.SandboxOff() {
		t.Fatalf("with the provider table readable the switch takes: %v", err)
	}
}

// A START WITH THE SANDBOX SAVED OFF NAMES EVERY KEPT PLACE ON THE FLOOR
// THE FIRST TURN READS. The start's own load of Ring 5 runs before the
// places are named; with the sandbox off the floor is loaded again after
// them, so the first prompt composed (appRingSource.Ring5) carries the
// floor's own places, the places kept only while the sandbox is off, and
// the sentence that says the boundary is off.
func TestAStartWithTheSandboxSavedOffNamesEveryKeptPlaceOnTheFloor(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, sibling := filepath.Join(root, "identity-0"), filepath.Join(root, "identity-1")
	data := filepath.Join(home, "data")
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "StartOff",
		KeyPath:    filepath.Join(data, "identity.sec"),
		LedgerPath: filepath.Join(data, "ledger.jsonl"),
		DBPath:     filepath.Join(data, "aii.db"),
	}).Ledger.Close()
	if err := os.MkdirAll(filepath.Join(sibling, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "open.txt")
	if err := os.WriteFile(outside, []byte("reachable"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	withProcessTable(t, "")
	cfg := defaultConfig()
	cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = "data/identity.sec", "data/ledger.jsonl", "data/aii.db"
	cfg.LLM = withTestProvider(t, home, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.SourcePath = filepath.Join(home, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Dashboard.RequireToken = true // the sandbox is off only while the dashboard asks for its token
	cfg.Tools.CWD = "."
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
	cfg.Tools.SandboxOff = true
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)

	if !app.toolReg.SandboxOff() || !strings.Contains(toolRead(app, outside), "reachable") {
		t.Fatal("the start did not apply the saved setting")
	}
	floor := appRingSource{rm: app.rings}.Ring5()
	policy := app.ensureRing5Policy()
	if floor == "" || !strings.HasSuffix(floor, policy.LocalFloor()) {
		t.Fatalf("the floor the first turn reads is the floor as it stands now, every place named:\n%s\nwant it to end with:\n%s", floor, policy.LocalFloor())
	}
	for _, want := range []string{firewall.BoundaryLifted, "- **data** — ", "**other identities' slots**", "**your program's update files**", "**the files that start your program**", "**your host's temporary files**"} {
		if !strings.Contains(floor, want) {
			t.Errorf("the floor the first turn reads lacks %q:\n%s", want, floor)
		}
	}
	if strings.Contains(floor, "paths outside fail") {
		t.Errorf("the floor the first turn reads still says paths outside fail:\n%s", floor)
	}
	if len(policy.Kept()) <= len(policy.Places()) {
		t.Fatalf("the places kept only while the sandbox is off are named at the start: kept %d, the identity's own %d", len(policy.Kept()), len(policy.Places()))
	}
}
