package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// unreadableConfig is a file a newer or older version wrote: a section
// key this version does not have, a section it has never heard of, and a
// tool it does not know among those switched off. The strict loader
// refuses it whole.
const unreadableConfig = `{
  "identity": {"ledger_path": "vault/ledger.jsonl", "db_path": "vault/aii.db", "key_path": "vault/identity.sec"},
  "llm": {"endpoint": "https://api.example.test/v1", "model": "kept-model"},
  "tools": {"disabled": ["web_fetch", "no_such_tool", "shell"]},
  "plugins": {"autoload": "none"},
  "speech": {"speakers": {"mode": "ignore", "uids": ["spk-1"], "from_a_newer_version": true}},
  "dashboard": {"port": 9123},
  "a_section_from_elsewhere": {"x": 1}
}`

// WHAT READS IS KEPT, AND THE NEXT START READS IT WHOLE. Every value the
// strict loader takes on its own survives: the identity's locations, the
// tools switched off that this version has, the autoload level, the model,
// the port. What it does not take is named, the restrictions first, and
// a section of which nothing is kept by the section's name; a speaker
// policy is kept whole or not at all, because a part of one (an "ignore"
// without its list) would deliver every voice.
func TestAnUnreadableConfigKeepsWhatReadsRestrictionsFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := decodeConfig(path, []byte(unreadableConfig)); err == nil {
		t.Fatal("inconclusive: the strict loader read the file")
	}
	cfg, lost, err := SalvageConfig(path, []byte(unreadableConfig))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Identity.LedgerPath != "vault/ledger.jsonl" || cfg.Identity.DBPath != "vault/aii.db" || cfg.Identity.KeyPath != "vault/identity.sec" {
		t.Errorf("the identity's locations were not kept: %+v", cfg.Identity)
	}
	if !reflect.DeepEqual(cfg.Tools.Disabled, []string{"web_fetch", "shell"}) {
		t.Errorf("A TOOL SWITCHED OFF CAME BACK ON: tools.disabled is %q", cfg.Tools.Disabled)
	}
	if cfg.Plugins.Autoload != "none" || cfg.LLM.Model != "kept-model" || cfg.Dashboard.Port != 9123 {
		t.Errorf("a setting that reads was not kept: autoload %q, model %q, port %d", cfg.Plugins.Autoload, cfg.LLM.Model, cfg.Dashboard.Port)
	}
	if cfg.Speech.Speakers.Mode != "" || len(cfg.Speech.Speakers.UIDs) != 0 {
		t.Errorf("a part of a speaker policy was kept: %+v", cfg.Speech.Speakers)
	}
	want := []string{"tools.disabled (1 of its 3 entries)", "speech", "a_section_from_elsewhere", "llm.endpoint"}
	if !reflect.DeepEqual(lost, want) {
		t.Errorf("what was not kept, restrictions first:\n got %q\nwant %q", lost, want)
	}
	// Written through the one owner, and read whole by the strict loader.
	again, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("the file written from what was kept does not read: %v", err)
	}
	if !reflect.DeepEqual(again.Tools.Disabled, cfg.Tools.Disabled) || again.Plugins.Autoload != "none" || again.Identity != cfg.Identity {
		t.Errorf("the file on disk is not what the start runs on: %+v %+v", again.Tools, again.Identity)
	}
}

// TWO PIECES THAT READ APART AND NOT TOGETHER: the one taken first is kept.
// A roll size and a total that each read beside the defaults, and not
// beside each other, in a section the loader refuses for a third value.
func TestPiecesThatCannotStandTogetherKeepTheFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"logs": {"max_bytes": 20971520, "max_total_bytes": 20971520, "max_backups": "nine"}}`
	cfg, lost, err := SalvageConfig(path, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logs.MaxBytes != 20971520 || cfg.Logs.MaxTotalBytes != 0 {
		t.Errorf("logs: max_bytes %d, max_total_bytes %d; want the first kept and the second not", cfg.Logs.MaxBytes, cfg.Logs.MaxTotalBytes)
	}
	if !reflect.DeepEqual(lost, []string{"logs.max_backups", "logs.max_total_bytes"}) {
		t.Errorf("not kept: %q", lost)
	}
}

// Bytes that are not a JSON object keep nothing, and the defaults are
// written: the identity's locations are the standard layout, which the
// FIRSTBOOT evidence scan guards (chooseBoot).
func TestUnreadableBytesKeepNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, lost, err := SalvageConfig(path, []byte("{{{ not json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Identity.LedgerPath != filepath.Join("data", "ledger.jsonl") || !reflect.DeepEqual(lost, []string{notAnObject}) {
		t.Fatalf("corrupt bytes: ledger %q, lost %q", cfg.Identity.LedgerPath, lost)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the defaults were not written: %v", err)
	}
}

// configSetAsideNotices are the operator's notices of files set aside.
func configSetAsideNotices(t *testing.T, a *App) []string {
	t.Helper()
	all, err := a.store.PageNotices()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range all {
		if strings.HasPrefix(n.ID, configSetAsideNoticePrefix) {
			out = append(out, n.Content)
		}
	}
	return out
}

// THE OPERATOR IS TOLD ONCE, ON THE PAGE, WHICH SETTINGS ARE NOT IN FORCE.
// The account is read at the live start from the file set aside and the
// file in use, so it is true whatever the operator has set since: a
// setting set again in Settings is in force and not named. A file already
// told is not told again; a second one set aside is its own notice.
func TestASetAsideConfigIsToldOnceWithWhatIsNotInForce(t *testing.T) {
	a := liveApp(t)
	cfgPath := a.configSnapshot().SourcePath
	cfg, _, err := SalvageConfig(cfgPath, []byte(unreadableConfig))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)
	aside := SetAsideName(cfgPath, at)
	if err := os.WriteFile(aside, []byte(unreadableConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	a.sayConfigsSetAside(*cfg)
	said := configSetAsideNotices(t, a)
	if len(said) != 1 {
		t.Fatalf("one notice for one file set aside, got %d: %q", len(said), said)
	}
	const whole = "[settings] Your settings file could not be read by this version of the platform when it started on 2026-10-20 at 09:30 UTC. " +
		"It is kept, unchanged, as config.json.unreadable-20261020T093000Z, and the settings in use were written from what could be read of it. " +
		"These settings in it are not in force now: tools.disabled (1 of its 3 entries), speech, a_section_from_elsewhere, llm.endpoint. Set again in Settings those you still want."
	if said[0] != whole {
		t.Errorf("the notice:\n got %s\nwant %s", said[0], whole)
	}

	// The next start: nothing more.
	a.sayConfigsSetAside(*cfg)
	if n := len(configSetAsideNotices(t, a)); n != 1 {
		t.Fatalf("told again at the next start: %d notices", n)
	}

	// A second file, set aside later, after the operator set the speaker
	// policy again: it is not named, because it is in force.
	cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"spk-1"}}
	second := SetAsideName(cfgPath, at.Add(time.Hour))
	if err := os.WriteFile(second, []byte(`{"speech": {"speakers": {"mode": "ignore", "uids": ["spk-1"]}}, "llm": {"model": "kept-model"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.sayConfigsSetAside(*cfg)
	said = configSetAsideNotices(t, a)
	if len(said) != 2 || !strings.HasSuffix(said[1], "Every setting in it is in force now.") {
		t.Fatalf("the second file: %q", said)
	}

	// Bytes that are not JSON: nothing of it is in force, and it says so.
	if err := os.WriteFile(SetAsideName(cfgPath, at.Add(2*time.Hour)), []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.sayConfigsSetAside(*cfg)
	said = configSetAsideNotices(t, a)
	if len(said) != 3 || !strings.HasSuffix(said[2], "Nothing in it could be read, so none of it is in force. Set again in Settings what you still want.") {
		t.Fatalf("the unreadable file: %q", said)
	}
}
