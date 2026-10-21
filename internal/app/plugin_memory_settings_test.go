package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// A PLUGIN'S MEMORY CEILING AND THE WINDOW FOR ITS REPLACED MEMORIES ARE THE
// OPERATOR'S. Both were numbers in the code: 10,000 memories in the broker,
// 90 days in the store. A file that names neither gets both, so nothing
// changes for it; a file that names them is obeyed; a negative number of
// either, and a window past a hundred years, are refused at load by their
// keys. The window reaches the daily pass in the settings it reads at each
// run and the ceiling reaches the broker when the plugin facility starts,
// and neither is a limit an activation reads, so a change of either has no
// plugin judged again.
func TestAPluginsMemoryCeilingAndWindowAreTheOperators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	load := func(runtime string) (*Config, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"plugins":{"runtime":{`+runtime+`}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(path)
	}
	day := 24 * time.Hour
	for _, runtime := range []string{``, `"max_memories":0,"replaced_memory_days":0`} {
		cfg, err := load(runtime)
		if err != nil {
			t.Fatalf("{%s}: %v", runtime, err)
		}
		if r := cfg.Plugins.Runtime; r.MaxMemories != 0 || r.replacedMemoryWindow() != 90*day {
			t.Errorf("{%s}: no number takes the broker's ceiling and 90 days: max_memories %d, window %s", runtime, r.MaxMemories, r.replacedMemoryWindow())
		}
		if got := continuitySettingsOf(*cfg).ReplacedPluginMemories; got != 90*day {
			t.Errorf("{%s}: the daily pass is handed %s, want the 90 days it always had", runtime, got)
		}
	}
	if broker.DefaultMaxMemories != 10000 {
		t.Errorf("the broker's ceiling where none is named is %d; it was 10000 in the code", broker.DefaultMaxMemories)
	}

	cfg, err := load(`"max_memories":2,"replaced_memory_days":30`)
	if err != nil {
		t.Fatal(err)
	}
	if r := cfg.Plugins.Runtime; r.MaxMemories != 2 || r.ReplacedMemoryDays != 30 || continuitySettingsOf(*cfg).ReplacedPluginMemories != 30*day {
		t.Errorf("the file's numbers are not the ones in force: %+v, the pass handed %s", r, continuitySettingsOf(*cfg).ReplacedPluginMemories)
	}
	for _, days := range []string{"1", "36500"} {
		if _, err := load(`"replaced_memory_days":` + days); err != nil {
			t.Errorf("a window of %s days is in the range and was refused: %v", days, err)
		}
	}
	for runtime, key := range map[string]string{
		`"max_memories":-1`:            "plugins.runtime.max_memories",
		`"replaced_memory_days":-1`:    "plugins.runtime.replaced_memory_days",
		`"replaced_memory_days":36501`: "plugins.runtime.replaced_memory_days",
	} {
		if _, err := load(runtime); err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "take the default") {
			t.Errorf("{%s} must be refused by its key, with the default named: %v", runtime, err)
		}
	}

	// Neither is a limit an activation reads: a change judges no plugin
	// again. A reload calls the ceiling restart-only (the broker is built
	// once) and the window live (the pass reads it when it runs).
	before, after := defaultConfig(), defaultConfig()
	after.Plugins.Runtime.MaxMemories, after.Plugins.Runtime.ReplacedMemoryDays = 2, 30
	if pluginLimitsMoved(before, after) {
		t.Error("a change of the memory ceiling or the window had every plugin judged again")
	}
	blankLiveAppliable(after)
	if r := after.Plugins.Runtime; r.MaxMemories != 2 || r.ReplacedMemoryDays != 0 {
		t.Errorf("a reload must call the ceiling restart-only and the window live: after blanking what applies live, %+v", r)
	}

	// The ceiling in force, through the broker the plugin facility is
	// given: with max_memories 3, two memories and a correction of one
	// are three rows, and a fourth is refused. The refusal says the
	// window the file set, for the replaced memory that is held, and the
	// ways out that exist, with both settings named.
	live := defaultConfig()
	live.SourcePath = path
	live.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
	live.Plugins.Runtime.MaxMemories, live.Plugins.Runtime.ReplacedMemoryDays = 3, 30
	live.Plugins.Grants = map[string]broker.Grant{"org.example.notes": {Memory: true}}
	if _, err := saveConfig(live); err != nil {
		t.Fatal(err)
	}
	a := New(live)
	opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	b := opts.Broker.Bind("org.example.notes", packagefmt.TierT1, []string{"ring4.memory"})
	remember := func(arguments string) string {
		t.Helper()
		reply, err := b.Dispatch(t.Context(), "invoke-call", []byte(`{"operation":"memory.remember","arguments":`+arguments+`}`))
		if err != nil {
			t.Fatalf("remember %s: %v", arguments, err)
		}
		if strings.Contains(string(reply), "MEMORY_QUOTA_EXCEEDED") != strings.Contains(arguments, "refused") {
			t.Fatalf("remember %s under a ceiling of three was answered: %s", arguments, reply)
		}
		return string(reply)
	}
	var first struct {
		Result struct {
			ID string `json:"id"`
		} `json:"operation_result"`
	}
	if err := json.Unmarshal([]byte(remember(`{"text":"one distinct thing"}`)), &first); err != nil || first.Result.ID == "" {
		t.Fatalf("the first memory's id: %+v %v", first, err)
	}
	remember(`{"text":"a second, unrelated"}`)
	remember(`{"text":"one thing corrected","supersedes":"` + first.Result.ID + `"}`)
	reply := remember(`{"text":"a fourth, refused"}`)
	for _, want := range []string{
		"3 memories held at the 3-memory ceiling, 2 current and 1 replaced",
		"30 days after it was replaced (plugins.runtime.replaced_memory_days)",
		"plugins.runtime.max_memories in the config file",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("a fourth row under max_memories 3 and a window of 30 days: the reply lacks %q: %s", want, reply)
		}
	}

	// The refusal says the window as the operator has it when it is
	// made, not as it was when the facility started: the window is read
	// by the daily pass at each run, and a change of it acts without a
	// restart.
	a.cfgMu.Lock()
	a.cfg.Plugins.Runtime.ReplacedMemoryDays = 7
	a.cfgMu.Unlock()
	if reply := remember(`{"text":"a fifth, refused"}`); !strings.Contains(reply, "7 days after it was replaced (plugins.runtime.replaced_memory_days)") {
		t.Errorf("the window changed to 7 days while the facility ran, and the refusal says: %s", reply)
	}
}

// WHERE THE WINDOW IS SAID, IT IS THE ONE IN FORCE, AND A RELOAD SAYS WHEN
// EACH NUMBER ACTS. The Plugins page's card says how many days a replaced
// memory is kept: the file's number, or 90 where it names none. A reload
// that changes the window says the daily pass takes it live; one that
// changes the ceiling says it is saved for the next start, since the
// broker reads it once.
func TestTheWindowOnThePageIsTheFilesAndAReloadSaysWhenEachNumberActs(t *testing.T) {
	cfg := defaultConfig()
	// The page's state reads the providers beside the configuration: a
	// configuration with no path would write them into the working
	// directory.
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	if got := (&App{cfg: cfg}).pluginsState(cfg).StoredReplacedDays; got != 90 {
		t.Errorf("with no window named the page is told %d days, want 90", got)
	}
	cfg.Plugins.Runtime.ReplacedMemoryDays = 30
	if got := (&App{cfg: cfg}).pluginsState(cfg).StoredReplacedDays; got != 30 {
		t.Errorf("with a window of 30 days in the file the page is told %d days", got)
	}

	out := reloadLog(t, func(c *Config) { c.Plugins.Runtime.ReplacedMemoryDays = 30 })
	if !strings.Contains(out, "maintenance and database housekeeping applied live") || strings.Contains(out, "SAVED FOR NEXT BOOT") {
		t.Errorf("a reload that changed plugins.runtime.replaced_memory_days must say the daily pass takes it live:\n%s", out)
	}
	out = reloadLog(t, func(c *Config) { c.Plugins.Runtime.MaxMemories = 2 })
	if !strings.Contains(out, "SAVED FOR NEXT BOOT") {
		t.Errorf("a reload that changed plugins.runtime.max_memories must say it waits for a restart:\n%s", out)
	}
}

// THE BOUND ON ANSWER-FIRST RECEIPTS IS THE OPERATOR'S
// (plugins.runtime.max_inflight_receipts). Absent or 0 is the broker's
// default; a number is obeyed; a negative one is refused at load by its
// key. It is the broker's, read when the plugin facility starts, so a
// reload that changes it says it waits for the next start and has no
// plugin judged again.
func TestTheBoundOnReceiptsHeldUnwrittenIsTheOperators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	load := func(runtime string) (*Config, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"plugins":{"runtime":{`+runtime+`}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(path)
	}
	if cfg, err := load(`"max_inflight_receipts":64`); err != nil || cfg.Plugins.Runtime.MaxInflightReceipts != 64 {
		t.Fatalf("a bound of 64 in the file: %v", err)
	}
	if _, err := load(`"max_inflight_receipts":-1`); err == nil || !strings.Contains(err.Error(), "plugins.runtime.max_inflight_receipts") {
		t.Fatalf("a negative bound must be refused at load by its key: %v", err)
	}
	before, after := defaultConfig(), defaultConfig()
	after.Plugins.Runtime.MaxInflightReceipts = 64
	if pluginLimitsMoved(before, after) {
		t.Error("the bound is no activation's limit: a change of it must not have a plugin judged again")
	}
	out := reloadLog(t, func(c *Config) { c.Plugins.Runtime.MaxInflightReceipts = 64 })
	if !strings.Contains(out, "SAVED FOR NEXT BOOT") {
		t.Errorf("a reload that changed plugins.runtime.max_inflight_receipts must say it waits for a restart:\n%s", out)
	}
}

// THE ARGUMENT-SCHEMA BOUND IS THE OPERATOR'S
// (plugins.runtime.max_input_schema_bytes). Absent or 0 hands
// the plugin host zero, its default; a number is handed as it is; a
// negative one is refused at load by its key. It is a limit an activation
// reads as it starts, so a change of it is one the plugins are judged
// again on.
func TestTheArgumentSchemaBoundIsHandedToThePluginHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	load := func(runtime string) (*Config, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"plugins":{"runtime":{`+runtime+`}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(path)
	}
	for runtime, want := range map[string]int{``: 0, `"max_input_schema_bytes":65536`: 65536} {
		cfg, err := load(runtime)
		if err != nil {
			t.Fatalf("{%s}: %v", runtime, err)
		}
		var opts pluginhost.Options
		if err := pluginLimits(&opts, *cfg); err != nil || opts.MaxInputSchemaBytes != want {
			t.Fatalf("{%s}: the plugin host is handed %d (%v), want %d", runtime, opts.MaxInputSchemaBytes, err, want)
		}
	}
	if _, err := load(`"max_input_schema_bytes":-1`); err == nil || !strings.Contains(err.Error(), "plugins.runtime.max_input_schema_bytes") {
		t.Fatalf("a negative bound must be refused at load by its key: %v", err)
	}
	before, after := defaultConfig(), defaultConfig()
	after.Plugins.Runtime.MaxInputSchemaBytes = 65536
	if !pluginLimitsMoved(before, after) {
		t.Error("the bound is an activation's limit: a change of it must have the plugins judged again")
	}
}
