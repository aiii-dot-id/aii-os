package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	configdir "github.com/aiii-dot-id/aii-os/config"
)

// wholeIdentity copies the files 0.1.22 writes for a first install into a
// fresh directory, with edit applied to each as JSON, and starts an App on
// them as far as the strip.
func wholeIdentity(t *testing.T, editConfig, editProviders func(map[string]any)) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	for name, edit := range map[string]func(map[string]any){"config.json": editConfig, "providers.json": editProviders} {
		raw, err := os.ReadFile(filepath.Join("testdata", "identity-by-v0.1.22", name))
		if err != nil {
			t.Fatal(err)
		}
		if edit != nil {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			edit(m)
			if raw, err = json.MarshalIndent(m, "", "  "); err != nil {
				t.Fatal(err)
			}
		}
		writeFile(t, filepath.Join(dir, name), string(raw))
	}
	cfg, err := ReadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg), dir
}

func section(m map[string]any, name string) map[string]any { return m[name].(map[string]any) }

func setPath(m map[string]any, path []string, v any) {
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
}

// A CORRECTED DEFAULT REACHES AN IDENTITY AN EARLIER RELEASE WROTE, and the
// operator's own values stay. 0.1.22's first start wrote the backup count
// 8, as every release did; a later release that corrects it to 12 (the
// defaults are swapped here for one that does) reaches the identity only
// once the 8 is out of its file. The strip runs once: a file it stripped is
// not one an earlier release wrote whole.
func TestACorrectedDefaultReachesAFileAnEarlierReleaseWrote(t *testing.T) {
	a, dir := wholeIdentity(t, func(m map[string]any) {
		m["timezone"] = "Europe/Oslo"                // the operator's
		section(m, "agency")["max_tool_rounds"] = 77 // the operator's: no release shipped it
	}, nil)
	corrected := strings.Replace(string(configdir.Config), `"backup_keep": 8`, `"backup_keep": 12`, 1)
	if corrected == string(configdir.Config) {
		t.Fatal("the defaults name no backup_keep of 8: the correction this test makes is not there to make")
	}
	shipped := configdir.Config
	configdir.Config = []byte(corrected)
	defer func() { configdir.Config = shipped }()
	if kept, err := ReadConfig(filepath.Join(dir, "config.json")); err != nil || kept.Maintenance.BackupKeep != 8 {
		t.Fatalf("the premise: the whole file holds the old default against the corrected one: %+v %v", kept.Maintenance, err)
	}

	a.stripShipped()
	file := readFile(t, filepath.Join(dir, "config.json"))
	var stripped map[string]any
	if err := json.Unmarshal([]byte(file), &stripped); err != nil {
		t.Fatal(err)
	}
	if stripped["timezone"] != "Europe/Oslo" || section(stripped, "agency")["max_tool_rounds"] != float64(77) {
		t.Fatalf("a value of the operator's was taken out:\n%s", file)
	}
	for _, gone := range [][]string{{"identity"}, {"maintenance"}, {"agency", "subagent_max_tool_rounds"}, {"logs"}, {"witness_server"}} {
		d, _ := parseJSONDoc([]byte(file))
		for _, k := range gone[:len(gone)-1] {
			d = d.member(k)
		}
		if d.member(gone[len(gone)-1]) != nil {
			t.Errorf("%s, a value a release shipped, is still in the file:\n%s", configPathName(gone), file)
		}
	}
	now := a.configSnapshot()
	if now.Maintenance.BackupKeep != 12 {
		t.Fatalf("the corrected default did not reach the identity: backup_keep %d in force, want 12", now.Maintenance.BackupKeep)
	}
	if now.Agency.MaxToolRounds.N != 77 || now.Timezone != "Europe/Oslo" {
		t.Fatalf("the operator's values are not in force: %+v %q", now.Agency.MaxToolRounds, now.Timezone)
	}
	a.stripShipped()
	if again := readFile(t, filepath.Join(dir, "config.json")); again != file {
		t.Fatalf("a second start stripped again:\n%s", again)
	}
}

// EVERY RELEASE THAT WROTE THE FILES WHOLE IS IN THE HISTORY, and none of
// the values it holds for where the identity's files are names other files
// than today's default: the strip never moves them. The history holds them
// as the releases were run to make it (data/aii.db); a release wrote them
// as this system spells a path (filepath.FromSlash: data\aii.db on
// Windows), and that is today's default here, which the strip takes for
// the shipped value, so a file this system's release wrote whole is one.
func TestTheHistoryHoldsEveryReleaseThatWroteWholeFiles(t *testing.T) {
	var h shippedHistory
	if err := json.Unmarshal(shippedValuesFile, &h); err != nil {
		t.Fatal(err)
	}
	var have []string
	for _, r := range h.Releases {
		v, _, _ := strings.Cut(r, " ")
		have = append(have, v)
	}
	for minor := 0; minor <= 23; minor++ {
		v := "0.1." + strconv.Itoa(minor)
		if !slices.Contains(have, v) {
			t.Errorf("release %s wrote whole files and is not in shipped_values.json (%v)", v, have)
		}
	}
	defaults := defaultConfig().Identity
	want := map[string]string{"ledger_path": defaults.LedgerPath, "db_path": defaults.DBPath, "key_path": defaults.KeyPath}
	config, _ := shippedSets()
	for _, p := range h.Config {
		if p.Path[0] != "identity" {
			continue
		}
		for _, v := range p.Values {
			var s string
			if json.Unmarshal(v, &s) != nil || filepath.FromSlash(s) != want[p.Path[1]] {
				t.Errorf("a release wrote identity.%s %s, which this system spells %q, and today's default is %q: the strip would move the identity's files", p.Path[1], v, filepath.FromSlash(s), want[p.Path[1]])
			}
		}
		raw, _ := json.Marshal(want[p.Path[1]])
		if d, err := docOf(raw); err != nil || !config.shipped(p.Path, d) {
			t.Errorf("identity.%s %s, as a release on this system wrote it, is not taken for a shipped value: a file this system's release wrote whole is never stripped", p.Path[1], raw)
		}
	}
}

// A FILE AN EARLIER RELEASE WROTE WHOLE ON THIS SYSTEM IS STRIPPED, AND THE
// IDENTITY'S FILES STAY WHERE THEY ARE, however its three paths are spelled.
// Spelled as a release here wrote them (filepath.Join), the file was written
// whole and gives them back; spelled with the other separator, they name
// the same files where both are separators (Windows), and are taken out
// there, and other files where \ is a letter of a name (Linux, macOS), and
// stay. Either way the files the configuration in force names are the files
// it named before.
func TestTheStripNeverMovesTheIdentitysFiles(t *testing.T) {
	for _, sep := range []string{"/", `\`} {
		spelled := func(name string) string { return "data" + sep + name }
		a, dir := wholeIdentity(t, func(m map[string]any) {
			id := section(m, "identity")
			id["ledger_path"], id["db_path"], id["key_path"] = spelled("ledger.jsonl"), spelled("aii.db"), spelled("identity.sec")
		}, nil)
		files := func() []string {
			id := a.configSnapshot().Identity
			var out []string
			for _, p := range []string{id.LedgerPath, id.DBPath, id.KeyPath} {
				abs, err := filepath.Abs(p)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, abs)
			}
			return out
		}
		before := files()
		a.stripShipped()
		if after := files(); !slices.Equal(after, before) {
			t.Errorf("data%sNAME: the strip moved the identity's files from %q to %q", sep, before, after)
		}
		d, err := parseJSONDoc([]byte(readFile(t, filepath.Join(dir, "config.json"))))
		if err != nil {
			t.Fatal(err)
		}
		// The spelling names here the files a release here wrote, or
		// others.
		same := filepath.Clean(spelled("aii.db")) == filepath.Join("data", "aii.db")
		if stripped := d.member("identity") == nil; stripped != same {
			t.Errorf("data%sNAME names here the files a release here wrote: %v; the file was stripped: %v", sep, same, stripped)
		}
	}
}

// A FILE NOT WRITTEN WHOLE IS LEFT AS IT IS: one that holds only what was
// named, and one whose identity paths are the operator's own.
func TestAFileNotWrittenWholeIsNotStripped(t *testing.T) {
	for name, content := range map[string]string{
		"named only":     `{"agency": {"max_tool_rounds": 30}}`,
		"own data paths": `{"identity": {"ledger_path": "/srv/me/ledger.jsonl", "db_path": "data/aii.db", "key_path": "data/identity.sec"}, "agency": {"max_tool_rounds": 30}}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		writeFile(t, path, content)
		cfg, err := ReadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		New(cfg).stripShipped()
		if got := readFile(t, path); got != content {
			t.Errorf("%s: the strip wrote %s", name, got)
		}
	}
}
