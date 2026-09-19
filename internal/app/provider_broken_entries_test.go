package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
// .
// .

func writeProvidersFor(t *testing.T, a *App, body string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// .
func fileEntries(t *testing.T, path string) []json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Providers []json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	for i := range file.Providers {
		file.Providers[i] = compactEntry(file.Providers[i])
	}
	return file.Providers
}

func joined(entries []json.RawMessage) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = string(e)
	}
	return strings.Join(parts, "\n")
}

func loadedOrFatal(t *testing.T, a *App) *providerRegistry {
	t.Helper()
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatalf("providers.json refused whole: %v", err)
	}
	return reg
}

func names(reg *providerRegistry) []string {
	out := []string{}
	for _, e := range reg.Providers {
		out = append(out, e.Name)
	}
	return out
}

const threeEntries = `{"providers":[
  {"name":"A","url":"https://a.test","models":["m"],"default":true},
  {"name":"B","url":"https://b.test","models":["m"],"api_kye":"a key typed under a misspelt field"},
  {"name":"C","url":"https://c.test","models":["m"]}
]}`

func TestOneBrokenProviderLeavesTheRestInUse(t *testing.T) {
	a := newVoiceApp(t)
	writeProvidersFor(t, a, threeEntries)
	reg := loadedOrFatal(t, a)
	if got := strings.Join(names(reg), ","); got != "A,C" {
		t.Fatalf("admitted %q, want A,C", got)
	}
	if len(reg.broken) != 1 {
		t.Fatalf("broken entries: %+v", reg.broken)
	}
	b := reg.broken[0]
	if b.position != 1 || b.name != "B" || !strings.Contains(b.reason, `unknown field "api_kye"`) || b.repair != nil {
		t.Fatalf("the broken entry is not named, placed and explained: %+v", b)
	}
	if _, _, err := a.resolveLLMConfig(LLMConfig{Provider: "C", Model: "m"}, reg); err != nil {
		t.Fatalf("a good entry beside a broken one does not resolve: %v", err)
	}
	if _, _, err := a.resolveLLMConfig(LLMConfig{Provider: "B"}, reg); err == nil ||
		!strings.Contains(err.Error(), `"B" is a broken entry in providers.json`) || !strings.Contains(err.Error(), "Settings → Providers") {
		t.Fatalf("pointing at the broken entry does not say so, with the way out: %v", err)
	}

	dir := a.providerDirectory()
	if len(dir.Providers) != 2 || len(dir.Broken) != 1 {
		t.Fatalf("directory: %+v", dir)
	}
	if got := dir.Broken[0]; got.Position != 1 || got.Name != "B" || got.SHA256 != entryDigest(b.raw) || got.Repair != "" {
		t.Fatalf("the page is not told which entry is broken: %+v", got)
	}
	wire, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, []byte("a key typed under a misspelt field")) {
		t.Fatalf("a broken entry's content reached the page: %s", wire)
	}
}

func TestABrokenDefaultEntrySaysSoWhenNothingNamesTheProvider(t *testing.T) {
	a := newVoiceApp(t)
	writeProvidersFor(t, a, `{"providers":[
	  {"name":"A","url":"https://a.test","models":["m"],"default":true,"api_type":"anthropicc"},
	  {"name":"C","url":"https://c.test","models":["m"]}
	]}`)
	reg := loadedOrFatal(t, a)
	_, _, err := a.resolveLLMConfig(LLMConfig{}, reg)
	if err == nil || !strings.Contains(err.Error(), `default-flagged entry "A" in providers.json is broken`) {
		t.Fatalf("an empty pointer at a broken default does not say so: %v", err)
	}
}

func TestASaveKeepsEachBrokenEntryWhereItStood(t *testing.T) {
	a := newVoiceApp(t)
	path := writeProvidersFor(t, a, threeEntries)
	original := fileEntries(t, path)[1]

	if err := a.setProviderInfo(dashboard.ProviderInfo{Name: "C", Endpoint: "https://c.test", DefaultModel: "m2", ConfiguredModels: []string{"m", "m2"}}); err != nil {
		t.Fatal(err)
	}
	entries := fileEntries(t, path)
	if len(entries) != 3 || !bytes.Equal(entries[1], original) {
		t.Fatalf("an edit to another entry lost or moved the broken one:\n%s", joined(entries))
	}
	if reg := loadedOrFatal(t, a); reg.Providers[1].DefaultModel != "m2" || reg.broken[0].position != 1 {
		t.Fatalf("the edit or the broken entry's place did not survive: %+v %+v", reg.Providers, reg.broken)
	}

	if err := a.deleteProvider("A"); err != nil {
		t.Fatal(err)
	}
	entries = fileEntries(t, path)
	if len(entries) != 2 || !bytes.Equal(entries[0], original) {
		t.Fatalf("removing the entry before it did not keep the broken one ahead of C:\n%s", joined(entries))
	}

	if err := a.setProviderInfo(dashboard.ProviderInfo{Name: "D", Endpoint: "https://d.test", ConfiguredModels: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	entries = fileEntries(t, path)
	reg := loadedOrFatal(t, a)
	if len(entries) != 3 || !bytes.Equal(entries[0], original) || strings.Join(names(reg), ",") != "C,D" {
		t.Fatalf("adding an entry moved or lost the broken one: %v\n%s", names(reg), joined(entries))
	}
}

func TestRepairAdmitsABrokenEntryWithOneSmallChange(t *testing.T) {
	first := `{"name":"A","url":"https://a.test","models":["m"],"default":true}`
	for _, c := range []struct {
		name, bad, what string
		check           func(*providerRegistry, providerEntry, []json.RawMessage) string
	}{
		{"the later of two defaults gives up its flag",
			`{"name":"X","url":"https://x.test","models":["m"],"default":true}`, "clear its default flag",
			func(reg *providerRegistry, e providerEntry, _ []json.RawMessage) string {
				if e.Name != "X" || e.Default || !reg.Providers[0].Default {
					return "X is still a default, or A lost its flag"
				}
				return ""
			}},
		{"a second entry with a name in use is kept under a free name",
			`{"name":"A","url":"https://again.test","models":["m"]}`, `rename it to "A (2)"; the first entry named "A" stays in use`,
			func(reg *providerRegistry, e providerEntry, _ []json.RawMessage) string {
				if e.Name != "A (2)" || e.URL != "https://again.test" || reg.Providers[0].URL != "https://a.test" {
					return "the second entry was not kept whole under the free name"
				}
				return ""
			}},
		{"an OAuth contract name no release ships is dropped",
			`{"name":"X","url":"https://x.test","models":["m"],"oauth":"no-such-contract"}`,
			"remove the OAuth contract name this release does not ship, so its credential's own contract applies",
			func(_ *providerRegistry, e providerEntry, _ []json.RawMessage) string {
				if e.Name != "X" || e.OAuth != "" || e.URL != "https://x.test" {
					return "the contract name was not the only thing removed"
				}
				return ""
			}},
		{"speech settings that fail are dropped where the release ships the vendor's own",
			`{"name":"Deepgram","url":"https://api.deepgram.com","chat":false,"speech":{"tts":{"query":{"x":"{secret}"}}}}`,
			"remove its speech settings, so the ones this release ships for Deepgram apply",
			func(_ *providerRegistry, e providerEntry, entries []json.RawMessage) string {
				if e.Name != "Deepgram" || e.Speech == nil {
					return "the shipped speech settings are not lent to the repaired entry"
				}
				if bytes.Contains(entries[1], []byte(`"speech"`)) {
					return "the failing speech settings were written back"
				}
				return ""
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newVoiceApp(t)
			path := writeProvidersFor(t, a, `{"providers":[`+first+`,`+c.bad+`,{"name":"Z","url":"https://z.test","models":["m"]}]}`)
			reg := loadedOrFatal(t, a)
			if len(reg.broken) != 1 || reg.broken[0].repair == nil || reg.broken[0].repair.what != c.what {
				t.Fatalf("repair offered: %+v", reg.broken)
			}
			b := reg.broken[0]
			if err := a.repairBrokenProvider(b.position, entryDigest(b.raw)); err != nil {
				t.Fatal(err)
			}
			after := loadedOrFatal(t, a)
			entries := fileEntries(t, path)
			if len(after.broken) != 0 || len(after.Providers) != 3 || after.Providers[2].Name != "Z" {
				t.Fatalf("the repaired entry did not take its own place: %v %+v", names(after), after.broken)
			}
			if problem := c.check(after, after.Providers[1], entries); problem != "" {
				t.Fatalf("%s: %+v", problem, after.Providers[1])
			}
		})
	}
}

// .
// .
func TestARepairKeepsALaterBrokenEntryInItsPlace(t *testing.T) {
	a := newVoiceApp(t)
	path := writeProvidersFor(t, a, `{"providers":[
	  {"name":"A","url":"https://a.test","models":["m"],"default":true},
	  {"name":"X","url":"https://x.test","models":["m"],"default":true},
	  {"name":"Z","url":"https://z.test","models":["m"]},
	  {"name":"Y","url":"https://y.test","models":["m"],"api_kye":"k"}
	]}`)
	later := fileEntries(t, path)[3]
	reg := loadedOrFatal(t, a)
	x := reg.broken[0]
	if err := a.repairBrokenProvider(x.position, entryDigest(x.raw)); err != nil {
		t.Fatal(err)
	}
	entries := fileEntries(t, path)
	after := loadedOrFatal(t, a)
	if len(entries) != 4 || !bytes.Equal(entries[3], later) || strings.Join(names(after), ",") != "A,X,Z" ||
		len(after.broken) != 1 || after.broken[0].position != 3 {
		t.Fatalf("the repair moved the later broken entry:\n%s", joined(entries))
	}
}

func TestNoRepairIsOfferedWhereOneWouldChangeTheEntry(t *testing.T) {
	for _, bad := range []string{
		`{"name":"X","url":"https://x.test","models":["m"],"api_type":"anthropicc"}`,
		`{"url":"https://x.test","models":["m"]}`,
		`{"name":"X","url":"https://x.test","models":"m"}`,
		`{"name":"X","url":"https://x.test","chat":false}`,
		`{"name":"X","url":"https://x.test","speech":{"tts":{"query":{"x":"{secret}"}}}}`,
		`42`,
		// .
		// .
		`{"name":"X","url":"https://x.test","models":["m"],"api_type":"anthropicc","default":true}`,
		`{"name":"A","url":"https://again.test","models":["m"],"api_type":"anthropicc"}`,
	} {
		a := newVoiceApp(t)
		writeProvidersFor(t, a, `{"providers":[{"name":"A","url":"https://a.test","models":["m"]},`+bad+`]}`)
		reg := loadedOrFatal(t, a)
		if len(reg.broken) != 1 || reg.broken[0].repair != nil {
			t.Fatalf("%s: broken %+v", bad, reg.broken)
		}
		b := reg.broken[0]
		if err := a.repairBrokenProvider(b.position, entryDigest(b.raw)); err == nil || !strings.Contains(err.Error(), "no repair is known") {
			t.Fatalf("%s: a repair ran where none is known: %v", bad, err)
		}
	}
}

func TestRepairAndRemoveActOnlyOnTheEntryShown(t *testing.T) {
	a := newVoiceApp(t)
	path := writeProvidersFor(t, a, threeEntries)
	reg := loadedOrFatal(t, a)
	b := reg.broken[0]
	digest := entryDigest(b.raw)
	before, _ := os.ReadFile(path)

	for _, attempt := range []struct {
		position int
		digest   string
	}{{b.position, strings.Repeat("0", 64)}, {b.position + 1, digest}} {
		if err := a.removeBrokenProvider(attempt.position, attempt.digest); err == nil || !strings.Contains(err.Error(), "no longer holds that broken entry") {
			t.Fatalf("a removal naming another entry was not refused: %v", err)
		}
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("a refused removal changed providers.json")
	}

	// .
	writeProvidersFor(t, a, strings.Replace(threeEntries, "misspelt", "mistyped", 1))
	if err := a.removeBrokenProvider(b.position, digest); err == nil {
		t.Fatal("a removal acted on an entry edited since it was shown")
	}

	current := loadedOrFatal(t, a).broken[0]
	if err := a.removeBrokenProvider(current.position, entryDigest(current.raw)); err != nil {
		t.Fatal(err)
	}
	after := loadedOrFatal(t, a)
	if len(after.broken) != 0 || strings.Join(names(after), ",") != "A,C" || len(fileEntries(t, path)) != 2 {
		t.Fatalf("removal took more or less than the broken entry: %v %+v", names(after), after.broken)
	}
}

// .
// .
// .
// .
// .
// .
func TestAnEditIsNotHeldHostageToABrokenSubstrateEntry(t *testing.T) {
	f := newEffortFixture(t, "own", "")
	path := filepath.Join(f.dir, "providers.json")
	good, err := json.Marshal(f.entry)
	if err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(good, &entry); err != nil {
		t.Fatal(err)
	}
	entry["oauth"] = "no-such-contract"
	broken, _ := json.Marshal(entry)
	if err := os.WriteFile(path, []byte(`{"providers":[`+string(broken)+`,{"name":"Other","url":"https://o.test","api_type":"anthropicc"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := loadedOrFatal(t, f.a)
	if len(reg.broken) != 2 {
		t.Fatalf("broken: %+v", reg.broken)
	}
	other := reg.broken[1]
	if err := f.a.removeBrokenProvider(other.position, entryDigest(other.raw)); err != nil {
		t.Fatalf("removing another broken entry was refused because the substrate entry is broken: %v", err)
	}
	reg = loadedOrFatal(t, f.a)
	if len(reg.broken) != 1 || reg.broken[0].name != f.entry.Name {
		t.Fatalf("after removal: %+v", reg.broken)
	}
	probes := f.probes.Load()
	sub := reg.broken[0]
	if err := f.a.repairBrokenProvider(sub.position, entryDigest(sub.raw)); err != nil {
		t.Fatalf("repairing the substrate entry: %v", err)
	}
	if f.probes.Load() == probes {
		t.Fatal("the repaired substrate entry was activated without being proved")
	}
	if reg = loadedOrFatal(t, f.a); len(reg.broken) != 0 || len(reg.Providers) != 1 || reg.Providers[0].OAuth != "" {
		t.Fatalf("after repair: %v %+v", names(reg), reg.broken)
	}
}

// .
// .
// .
func TestSkipSignInWithValidTokenIsOnUnlessTurnedOff(t *testing.T) {
	if got := defaultConfig().Dashboard.SkipSignInWithValidToken; got == nil || !*got {
		t.Fatalf("the shipped config does not set the parameter on: %v", got)
	}
	if !(DashboardConfig{}).skipSignInWithValidToken() {
		t.Fatal("an absent parameter is not on")
	}
	dir := t.TempDir()
	for _, c := range []struct {
		body string
		want bool
	}{
		{`{}`, true},
		{`{"dashboard":{"port":8180}}`, true},
		{`{"dashboard":{"skip_signin_with_valid_token":false}}`, false},
		{`{"dashboard":{"skip_signin_with_valid_token":true}}`, true},
	} {
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.Dashboard.skipSignInWithValidToken(); got != c.want {
			t.Fatalf("%s: skip=%v, want %v", c.body, got, c.want)
		}
	}
	a := newVoiceApp(t)
	if !a.providerDirectory().SkipSignInWithValidToken {
		t.Fatal("the listing does not carry the default")
	}
	off := false
	a.cfg.Dashboard.SkipSignInWithValidToken = &off
	if a.providerDirectory().SkipSignInWithValidToken {
		t.Fatal("the listing does not carry the operator's choice")
	}
}
