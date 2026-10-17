package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// emptyEveryNone gives every map and list of v that holds none an empty
// one, and says how many it gave. The two are one thing to an operator and
// to the file, and two to reflect.DeepEqual.
func emptyEveryNone(v reflect.Value) int {
	given := 0
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanSet() {
			continue
		}
		switch f.Kind() {
		case reflect.Struct:
			given += emptyEveryNone(f)
		case reflect.Map:
			if f.IsNil() {
				f.Set(reflect.MakeMap(f.Type()))
				given++
			}
		case reflect.Slice:
			if f.IsNil() {
				f.Set(reflect.MakeSlice(f.Type(), 0, 0))
				given++
			}
		}
	}
	return given
}

func TestAReloadOfTheHostsOwnSaveChangesNothing(t *testing.T) {
	dir := t.TempDir()
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "OwnSaveTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}).Ledger.Close()
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")}
	cfg.LLM, cfg.SourcePath, cfg.Dashboard.Port, cfg.Tools.CWD = withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"), filepath.Join(dir, "config.json"), 0, dir
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.SourcePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })

	// The host saves a configuration whose every empty map and list is
	// empty and not none, through the one place a save becomes live.
	app.cfgMu.Lock()
	candidate := *app.cfg
	given := emptyEveryNone(reflect.ValueOf(&candidate).Elem())
	published, err := app.persistLocked(&candidate, saveConfig)
	app.cfgMu.Unlock()
	if err != nil || !published {
		t.Fatalf("the save: published=%v err=%v", published, err)
	}
	if given < 3 {
		t.Fatalf("only %d maps and lists held none: this configuration no longer tries the reload", given)
	}
	saved := app.configSnapshot()
	read, err := ReadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(*read, saved) {
		t.Fatal("the file reads back as the very value that was saved: nothing here tries the reload")
	}
	if !sameWritten(saved, *read) {
		t.Fatal("the file does not say what was saved")
	}

	app.reloadConfig()
	if after := app.configSnapshot(); !reflect.DeepEqual(after, saved) {
		t.Fatal("the reload of the host's own save replaced the live configuration: a change checked across it is refused as moved")
	}

	// A change of the file is still a change: an edit by hand is applied.
	read.Dashboard.Expert = !saved.Dashboard.Expert
	edited, err := json.MarshalIndent(read, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.SourcePath, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	app.reloadConfig()
	if after := app.configSnapshot(); after.Dashboard.Expert == saved.Dashboard.Expert {
		t.Fatal("an edit of the file by hand was not applied")
	}
}
