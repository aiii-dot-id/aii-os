package app

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// EVERY LINE OF AN ENTRY IS A CHOICE OR A VENDOR FACT, and each fact is
// lent by exactly one rule: a field added to providerEntry must say which.
func TestEachEntryLineIsAChoiceOrALentFact(t *testing.T) {
	seen := map[string]string{}
	for class, names := range map[string][]string{"choice": choiceFields, "lent by its rule": lentByRule, "lent by name": lentFactFields} {
		for _, n := range names {
			if other, dup := seen[n]; dup {
				t.Errorf("%s is both %s and %s", n, other, class)
			}
			seen[n] = class
		}
	}
	for name := range entryFieldIndex {
		if _, ok := seen[name]; !ok {
			t.Errorf("providerEntry's %q is neither a choice nor a lent fact: say which in providers_lent.go", name)
		}
	}
}

// A FIRST LOAD WRITES EACH SHIPPED ENTRY'S CHOICES AND NOTHING ELSE, and the
// registry it reads back is the shipped one: every fact is lent.
func TestAFirstLoadWritesOnlyChoicesAndLendsEveryFact(t *testing.T) {
	path := filepath.Join(t.TempDir(), ProvidersFileName)
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, path)), &file); err != nil {
		t.Fatal(err)
	}
	if len(file) != 1 || file["providers"] == nil {
		t.Fatalf("a first load wrote top-level lines other than the list: %v", slices.Collect(mapKeys(file)))
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(file["providers"], &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(embeddedRegistry().Providers) {
		t.Fatalf("%d entries written, the registry ships %d", len(entries), len(embeddedRegistry().Providers))
	}
	for _, e := range entries {
		for k := range e {
			if !isChoiceField(k) {
				t.Errorf("a first load wrote %s's %s, a vendor fact", e["name"], k)
			}
		}
	}
	for _, shipped := range embeddedRegistry().Providers {
		got := entryNamed(reg, shipped.Name)
		if got == nil {
			t.Fatalf("%s is not in the registry read back", shipped.Name)
		}
		for _, name := range lentFactFields {
			want, _ := json.Marshal(entryField(&shipped, name).Interface())
			if have, _ := json.Marshal(entryField(got, name).Interface()); string(have) != string(want) {
				t.Errorf("%s's %s read back as %s, the shipped registry says %s", shipped.Name, name, have, want)
			}
		}
	}
}

// A SAVE WRITES THE LINE IT CHANGED and lends, never writes, the rest.
func TestAProvidersSaveWritesOnlyWhatItChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), ProvidersFileName)
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := readFile(t, path)
	e := entryNamed(reg, "Anthropic")
	e.DefaultModel = "claude-sonnet-5"
	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)
	want := strings.Replace(before, `"name": "Anthropic",
      "api_type": "anthropic",
      "url": "https://api.anthropic.com",
      "default_model": "claude-opus-5"`, `"name": "Anthropic",
      "api_type": "anthropic",
      "url": "https://api.anthropic.com",
      "default_model": "claude-sonnet-5"`, 1)
	if want == before {
		t.Fatalf("the scaffold's Anthropic entry is not as this test expects:\n%s", before)
	}
	if after != want {
		t.Fatalf("a save of one model wrote more than that line:\n%s", after)
	}
}

// THE STRIP TAKES OUT OF A FILE AN EARLIER RELEASE WROTE THE VENDOR FACTS
// IT STILL HOLDS AS SHIPPED, so a corrected one reaches the identity;
// keeps every choice an older release needs to read an entry as the same
// entry; and keeps what differs from every shipped version.
func TestTheStripLendsWhatWasShippedAndKeepsWhatIsYours(t *testing.T) {
	path, old := correctedVendorFact(t)
	var oldValue any
	if err := json.Unmarshal(old, &oldValue); err != nil {
		t.Fatal(err)
	}
	a, dir := wholeIdentity(t, nil, func(m map[string]any) {
		for _, raw := range m["providers"].([]any) {
			e := raw.(map[string]any)
			switch e["name"] {
			case path[1]:
				setPath(e, path[2:], oldValue)
			}
			switch e["name"] {
			case "zAI":
				e["context_length"] = 123456 // the operator's: no release shipped it
			case "ElevenLabs":
				e["url"] = "https://speech.example.test" // moved: the vendor's key variable is not lent there
			}
		}
	})
	a.stripShipped()
	reg, err := loadProvidersFile(filepath.Join(dir, ProvidersFileName))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Providers []map[string]any `json:"providers"`
	}
	raw := readFile(t, filepath.Join(dir, ProvidersFileName))
	if err := json.Unmarshal([]byte(raw), &file); err != nil {
		t.Fatal(err)
	}
	shipped := map[string]providerEntry{}
	for _, e := range embeddedRegistry().Providers {
		shipped[e.Name] = e
	}
	for _, e := range file.Providers {
		name := e["name"].(string)
		s := shipped[name]
		for _, choice := range []string{"api_type", "url", "credential", "chat", "local"} {
			want, _ := json.Marshal(entryField(&s, choice).Interface())
			if name == "ElevenLabs" && choice == "url" {
				continue
			}
			if entryField(&s, choice).IsZero() {
				continue
			}
			if have, _ := json.Marshal(e[choice]); string(have) != string(want) {
				t.Errorf("%s's %s, which a release before this one reads the entry by, is %s after the strip, want %s", name, choice, have, want)
			}
		}
		if name == "zAI" && e["context_length"] != float64(123456) {
			t.Errorf("zAI's own window was taken out: %v", e)
		}
		if name == "ElevenLabs" && e["api_key_env"] != "ELEVENLABS_API_KEY" {
			t.Errorf("ElevenLabs, moved, lost its key variable, which this release lends only at the vendor's address: %v", e)
		}
		if name == "Cartesia" && e["api_key_env"] != nil {
			t.Errorf("Cartesia, at its vendor's address, keeps the key variable this release lends there: %v", e)
		}
	}
	// The corrected fact is in force: the shipped one of today.
	view, err := providersView([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	today, _ := providersView(embeddedProviders)
	if view[pathKey(path)] != today[pathKey(path)] {
		t.Fatalf("%s is %s after the strip, want today's %s", providersPathName(path), view[pathKey(path)], today[pathKey(path)])
	}
	if strings.Contains(raw, "\"model_capabilities\"") || strings.Contains(raw, "\"dialect_effort_floor\"") {
		t.Errorf("the shipped model rows or floors are still in the file:\n%s", raw)
	}
	if c := entryNamed(reg, "Cartesia"); c == nil || c.APIKeyEnv != shipped["Cartesia"].APIKeyEnv {
		t.Errorf("Cartesia's key variable is not lent at its address: %+v", c)
	}
}

// correctedVendorFact is a line of an entry of providers.json whose value
// a release shipped and that is not what this release lends there.
func correctedVendorFact(t *testing.T) (path []string, old json.RawMessage) {
	t.Helper()
	today, err := providersView(embeddedProviders)
	if err != nil {
		t.Fatal(err)
	}
	var h shippedHistory
	if err := json.Unmarshal(shippedValuesFile, &h); err != nil {
		t.Fatal(err)
	}
	for _, p := range h.Providers {
		if len(p.Path) < 3 || p.Path[0] != "providers" || isChoiceField(p.Path[2]) || p.Path[2] == "api_key_env" {
			continue
		}
		if p.Path[1] == "zAI" || p.Path[1] == "ElevenLabs" || entryNamed(embeddedRegistry(), p.Path[1]) == nil {
			continue
		}
		cur, ok := today[pathKey(p.Path)]
		if !ok {
			continue
		}
		for _, v := range p.Values {
			if string(canonicalJSON(v)) != cur {
				return p.Path, v
			}
		}
	}
	t.Fatal("no release shipped a vendor fact this release corrected: the history holds no such line, and this test proves nothing")
	return nil, nil
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
