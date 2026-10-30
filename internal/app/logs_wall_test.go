package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// logWallApp is a live identity in the installed layout — their
// configuration in config/, their record, key and store in data/, their
// logs in log/ — with a rolled log, a capture and their switch already written
// in log/, each marked. It returns the app and the identity's home.
func logWallApp(t *testing.T) (*App, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "identity-0")
	data, configDir := filepath.Join(home, "data"), filepath.Join(home, "config")
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "LogWallTest",
		KeyPath:    filepath.Join(data, "identity.sec"),
		LedgerPath: filepath.Join(data, "ledger.jsonl"),
		DBPath:     filepath.Join(data, "aii.db"),
	}).Ledger.Close()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	cfg := defaultConfig()
	cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = "data/identity.sec", "data/ledger.jsonl", "data/aii.db"
	cfg.LLM = withTestProvider(t, configDir, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.SourcePath = filepath.Join(configDir, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = "."
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })
	for rel, body := range map[string]string{
		"log/aii-rolled.log":        "LOG LINE MARK\n",
		"log/llm/a-capture.json":    "CAPTURE MARK\n",
		"log/" + LogControlFileName: "{}\n",
	} {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return app, home
}

func TestTheLogIsTheIdentitysToReadAndNotToChangeButForItsSwitch(t *testing.T) {
	app, home := logWallApp(t)
	call := func(tool string, args map[string]any) tools.Result {
		t.Helper()
		res, err := app.toolReg.Execute(context.Background(), tool, args)
		if err != nil {
			t.Fatalf("%s %v: %v", tool, args, err)
		}
		return res
	}
	for _, rel := range []string{"log/aii-rolled.log", "log/llm/a-capture.json"} {
		if res := call("read", map[string]any{"file_path": rel}); res.Error != "" || !strings.Contains(res.Output, "MARK") {
			t.Errorf("read of %s stays open: %+v", rel, res)
		}
		if res := call("grep", map[string]any{"pattern": "MARK", "path": filepath.Dir(rel)}); res.Error != "" || !strings.Contains(res.Output, filepath.Base(rel)) {
			t.Errorf("grep of %s stays open: %+v", filepath.Dir(rel), res)
		}
		if res := call("ls", map[string]any{"path": filepath.Dir(rel)}); res.Error != "" || !strings.Contains(res.Output, filepath.Base(rel)) {
			t.Errorf("ls of %s stays open: %+v", filepath.Dir(rel), res)
		}
		for name, args := range map[string]map[string]any{
			"write":     {"file_path": rel, "content": "SPRAY"},
			"delete":    {"file_path": rel, "action": "delete"},
			"move away": {"file_path": rel, "action": "move", "to": "taken.txt"},
			"copy onto": {"file_path": "notes.md", "action": "copy", "to": rel},
			"a new one": {"file_path": filepath.Join(filepath.Dir(rel), "forged.log"), "content": "SPRAY"},
		} {
			if name == "copy onto" {
				if err := os.WriteFile(filepath.Join(home, "notes.md"), []byte("a note\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if res := call("write", args); !res.Refused || !strings.Contains(res.Error, "protected path (sub.logs:") {
				t.Errorf("%s of %s must be refused as the log: %+v", name, rel, res)
			}
		}
		if res := call("edit", map[string]any{"file_path": rel, "old_string": "MARK", "new_string": "SPRAY"}); !res.Refused {
			t.Errorf("edit of %s must be refused: %+v", rel, res)
		}
		if res := call("shell", map[string]any{"command": "echo SPRAY >> " + rel}); !res.Refused {
			t.Errorf("the shell, which cannot be told a read from a write, is refused %s: %+v", rel, res)
		}
		if b, err := os.ReadFile(filepath.Join(home, rel)); err != nil || !strings.HasSuffix(string(b), "MARK\n") || strings.Contains(string(b), "SPRAY") {
			t.Fatalf("%s was changed: %q %v", rel, b, err)
		}
	}
	if res := call("shell", map[string]any{"command": "rm -r log"}); !res.Refused {
		t.Errorf("the shell is refused the log directory whole: %+v", res)
	}

	// The switch is the identity's: written by their write tool, and by their
	// own command, whose word log is not read as the directory. Only that
	// command alone: with a second one the word is the directory again.
	control := filepath.Join("log", LogControlFileName)
	if res := call("write", map[string]any{"file_path": control, "content": `{"level":"debug"}` + "\n"}); res.Error != "" || !strings.Contains(res.Output, "Wrote") {
		t.Fatalf("the identity writes their switch %s: %+v", control, res)
	}
	if ctl, err := ReadLogControl(filepath.Join(home, control)); err != nil || ctl.Level != "debug" {
		t.Fatalf("the switch the identity wrote is read as it wrote it: %+v %v", ctl, err)
	}
	if res := call("shell", map[string]any{"command": "aii log level debug"}); res.Refused {
		t.Errorf("the identity's own log command is not refused for its word log: %+v", res)
	}
	if res := call("shell", map[string]any{"command": "aii log level debug; rm -r log"}); !res.Refused {
		t.Errorf("a second command after it names the log directory, and is refused: %+v", res)
	}
}

func TestAPluginNeverReachesTheLog(t *testing.T) {
	app, home := logWallApp(t)
	cfg := app.configSnapshot()
	door := newPluginSandbox(app.toolReg, cfg)
	control := filepath.Join("log", LogControlFileName)
	for _, rel := range []string{"log", "log/aii-rolled.log", "log/llm/a-capture.json", control, "LOG/" + LogControlFileName} {
		for _, op := range []string{"fs.read", "fs.write", "fs.list"} {
			_, _, err := door.Admit("org.example.probe "+op, rel)
			var denied *tools.SubstrateError
			if !errors.As(err, &denied) || denied.Rule.ID != "sub.logs" {
				t.Errorf("a plugin's %s of %s must be refused as the log, got %v", op, rel, err)
			}
		}
	}
	if _, _, err := app.toolReg.Admit("write", control); err != nil {
		t.Fatalf("the identity's own door admits their switch: %v", err)
	}
	// A file the plugin's operation opened is asked what it is: the switch,
	// opened by another name, is the switch.
	f, err := os.Open(filepath.Join(home, control))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var denied *tools.SubstrateError
	if err := door.Held(info, f); !errors.As(err, &denied) || denied.Rule.ID != "sub.logs" {
		t.Errorf("a plugin holding the switch open is refused it: %v", err)
	}

	// The native wall's inventory: the log directory, kept, with the switch
	// its one exception.
	logDir := realPath(filepath.Join(home, "log"))
	found := false
	for _, o := range substrateObjects(app.activeConfig(cfg), cfg.Tools.CWD) {
		if o.ID == "sub.logs" && o.Path == logDir {
			found = o.ReadOpen && len(o.Except) == 1 && o.Except[0] == logControlFile(cfg)
		}
	}
	if !found {
		t.Errorf("the native wall's inventory keeps %s, read-open, with only the switch handed back", logDir)
	}
}

// THE LOG DIRECTORIES ARE WHERE THE LOGS ARE, AND NEVER THE HOME. The
// switch's directory beside the identity's configuration, and the live
// log's when logs.dir names another; one when they are the same; and a
// logs.dir that is the home or holds it is not kept, or the identity's
// whole home would be read-only.
func TestTheLogDirectoriesAreWhereTheLogsAreAndNeverTheHome(t *testing.T) {
	home := realPath(t.TempDir())
	elsewhere := realPath(t.TempDir())
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(home, ConfigDirName, "config.json")
	for _, c := range []struct {
		dir  string
		want []string
	}{
		{filepath.Join(home, "log"), []string{filepath.Join(home, "log")}},
		{"", []string{filepath.Join(home, "log")}},
		{elsewhere, []string{filepath.Join(home, "log"), elsewhere}},
		{home, []string{filepath.Join(home, "log")}},
		{filepath.Dir(home), []string{filepath.Join(home, "log")}},
	} {
		cfg.Logs.Dir = c.dir
		got := logDirs(*cfg, home)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("logs.dir %q: the log directories are %q, want %q", c.dir, got, c.want)
		}
	}
	if got, want := logControlFile(*cfg), filepath.Join(home, "log", LogControlFileName); got != want {
		t.Errorf("the switch is %s, want %s", got, want)
	}
}

// THE FLOOR NAMES THE PLACES IT KEEPS FROM THE START. Ring 5 was loaded
// before the boot named the places, and loaded again only for a granted
// root: an identity with none was told of none of them — the data
// directory, the log — until a reload moved one, while their tools
// refused each. Now the boot's naming of the places loads the floor again.
func TestTheFloorNamesThePlacesItKeepsFromTheStart(t *testing.T) {
	app, _ := logWallApp(t)
	if roots := app.configSnapshot().Tools.ExtraRoots; len(roots) != 0 {
		t.Fatalf("the case under test has no granted root: %v", roots)
	}
	floor := app.rings.GetContent(ring.Ring5)
	for _, said := range []string{"**log** — " + logsReason, "**data** — Your data directory is the host's"} {
		if strings.Count(floor, said) != 1 {
			t.Errorf("the floor says %q once from the start:\n%s", said, floor)
		}
	}
}
