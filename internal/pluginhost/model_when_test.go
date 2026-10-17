package pluginhost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

type whenVectors struct {
	Floor       string          `json:"floor"`
	Settings    []SettingDecl   `json:"settings"`
	Base        json.RawMessage `json:"base"`
	Declaration []struct {
		Name   string `json:"name"`
		When   string `json:"when"`
		Min    string `json:"min"`
		Host   bool   `json:"host"`
		Author bool   `json:"author"`
	} `json:"declaration"`
}

// THE HOST ACCEPTS WHAT THE VECTORS SAY IT ACCEPTS, and the vectors' floor
// is the host's constant: the kit reads the same file, so the two cannot
// drift apart on what a package may declare.
func TestTheHostHoldsAConditionalModelToTheVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "model_when.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v whenVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Floor != packagefmt.ModelWhenMinHost {
		t.Fatalf("the vectors' floor is %s, the host's %s: they change in one landing", v.Floor, packagefmt.ModelWhenMinHost)
	}
	base := strings.TrimSpace(string(v.Base))
	for _, row := range v.Declaration {
		decls, err := ParseModels([]byte(`[` + base[:len(base)-1] + row.When + `}]`))
		if err == nil {
			err = checkModelConditions(decls, v.Settings, row.Min)
		}
		if (err == nil) != row.Host {
			t.Errorf("%s: the host accepts=%v, the vectors say %v (%v)", row.Name, err == nil, row.Host, err)
		}
	}
}

// whenPackage signs a native package for this host with models, settings
// and a floor, and no accelerator profile.
func whenPackage(t *testing.T, id string, role *packagetest.Role, models []ModelDecl, settings []SettingDecl, minHost string) string {
	t.Helper()
	vid := hostVariantID()
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/" + vid + "/child":                 []byte("#!/bin/sh\nexit 0\n"),
	}
	var err error
	if files[ModelsFile], err = json.Marshal(models); err != nil {
		t.Fatal(err)
	}
	if settings != nil {
		if files[SettingsFile], err = json.Marshal(settings); err != nil {
			t.Fatal(err)
		}
	}
	extra := map[string]any{}
	if minHost != "" {
		extra["aiios_min_version"] = minHost
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{ID: vid, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + vid + "/child"}},
		files, extra)
	spec := packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := role.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	return writePkg(t, spec)
}

func whenModel(name string, when *ModelWhen) ModelDecl {
	return ModelDecl{Name: name, URL: "https://example.com/" + name, SHA256: strings.Repeat("a", 64), Size: 1024, When: when}
}

func whenNames(models []ModelDecl) string {
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name)
	}
	return strings.Join(names, " ")
}

// THE SELECTION FOLLOWS THE VALUE THE PLUGIN WILL READ: the operator's
// stored value where it holds to the declaration, else the declared
// default, else nothing.
func TestAConditionalModelIsTakenWhileItsSettingHoldsAValueItNames(t *testing.T) {
	roots, role := platformRootsForTest(t)
	language := SettingDecl{Key: "tts_language", Type: SettingEnum, Title: "Speaking language", Default: "en", Values: []string{"en", "fr", "de", "nl"}}
	models := []ModelDecl{
		whenModel("base", nil),
		whenModel("fr-pack", &ModelWhen{Setting: "tts_language", Values: []string{"fr"}}),
		whenModel("lowlands-pack", &ModelWhen{Setting: "tts_language", Values: []string{"de", "nl"}}),
	}
	selected := func(id string, settings []SettingDecl, stored map[string]any) string {
		t.Helper()
		pkg := whenPackage(t, id, role, models, settings, packagefmt.ModelWhenMinHost)
		opts := supervisedOpts(Options{Roots: roots, StoreDir: t.TempDir(), HostVersion: packagefmt.ModelWhenMinHost})
		if stored != nil {
			opts.Settings = func(plugin string) map[string]any {
				if plugin != id {
					t.Errorf("the stored settings of %q were asked for %q", id, plugin)
				}
				return stored
			}
		}
		res, err := Verify(pkg, opts)
		if err != nil {
			t.Fatal(err)
		}
		sel, err := Select(pkg, res, opts)
		if err != nil {
			t.Fatalf("selection: %v", err)
		}
		return whenNames(sel.Models())
	}
	for _, c := range []struct {
		name     string
		settings []SettingDecl
		stored   map[string]any
		want     string
	}{
		{"nothing stored, the default names no pack", []SettingDecl{language}, nil, "base"},
		{"French stored", []SettingDecl{language}, map[string]any{"tts_language": "fr"}, "base fr-pack"},
		{"Dutch stored, a pack for two values", []SettingDecl{language}, map[string]any{"tts_language": "nl"}, "base lowlands-pack"},
		{"a stored value the declaration no longer has falls to the default", []SettingDecl{language}, map[string]any{"tts_language": "xx"}, "base"},
		{"another setting stored", []SettingDecl{language}, map[string]any{"speed": 2}, "base"},
		{"nothing stored and the default is German", []SettingDecl{{Key: "tts_language", Type: SettingEnum, Title: "Speaking language", Default: "de", Values: []string{"en", "fr", "de", "nl"}}}, nil, "base lowlands-pack"},
		{"no default and nothing stored", []SettingDecl{{Key: "tts_language", Type: SettingEnum, Title: "Speaking language", Values: []string{"en", "fr", "de", "nl"}}}, nil, "base"},
	} {
		if got := selected("org.example.when"+string(rune('a'+len(c.name)%26)), c.settings, c.stored); got != c.want {
			t.Errorf("%s: selected %q, want %q", c.name, got, c.want)
		}
	}
}

// A CONDITION THE PACKAGE'S OWN SETTINGS DO NOT BEAR OUT REFUSES THE
// ACTIVATION, as does one in a package that does not ask for a host that
// reads it; and a package with no conditional model reads no settings
// here at all.
func TestAConditionalModelIsHeldToThePackagesSettingsAndFloor(t *testing.T) {
	roots, role := platformRootsForTest(t)
	language := SettingDecl{Key: "tts_language", Type: SettingEnum, Title: "Speaking language", Default: "en", Values: []string{"en", "fr"}}
	speed := SettingDecl{Key: "speed", Type: SettingNumber, Title: "Speed"}
	pack := func(setting string, values ...string) []ModelDecl {
		return []ModelDecl{whenModel("base", nil), whenModel("pack", &ModelWhen{Setting: setting, Values: values})}
	}
	for i, c := range []struct {
		name     string
		models   []ModelDecl
		settings []SettingDecl
		min      string
		refusal  string
	}{
		{"a setting the package does not declare", pack("voice_accent", "fr"), []SettingDecl{language}, packagefmt.ModelWhenMinHost, "not a setting this package declares"},
		{"no settings at all", pack("tts_language", "fr"), nil, packagefmt.ModelWhenMinHost, "not a setting this package declares"},
		{"a setting that is not an enum", pack("speed", "fr"), []SettingDecl{language, speed}, packagefmt.ModelWhenMinHost, "an enum alone"},
		{"a value the setting does not have", pack("tts_language", "de"), []SettingDecl{language}, packagefmt.ModelWhenMinHost, "not a value of setting"},
		{"a floor below the member's", pack("tts_language", "fr"), []SettingDecl{language}, "0.1.14", "requires aiios_min_version >= " + packagefmt.ModelWhenMinHost},
		{"no floor", pack("tts_language", "fr"), []SettingDecl{language}, "", "requires aiios_min_version >= " + packagefmt.ModelWhenMinHost},
	} {
		pkg := whenPackage(t, "org.example.refused"+string(rune('a'+i)), role, c.models, c.settings, c.min)
		opts := supervisedOpts(Options{Roots: roots, StoreDir: t.TempDir(), HostVersion: packagefmt.ModelWhenMinHost})
		res, err := Verify(pkg, opts)
		if err != nil {
			t.Fatalf("%s: verification: %v", c.name, err)
		}
		_, err = Select(pkg, res, opts)
		var me *ModelsError
		if !errors.As(err, &me) || !strings.Contains(err.Error(), c.refusal) {
			t.Errorf("%s: want a models refusal saying %q, got: %v", c.name, c.refusal, err)
		}
	}

	// No conditional model: an unreadable settings member is not this
	// stage's to refuse, as before.
	pkg := whenPackage(t, "org.example.plain", role, []ModelDecl{whenModel("base", nil)}, nil, "")
	opts := supervisedOpts(Options{Roots: roots, StoreDir: t.TempDir()})
	res, err := Verify(pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sel, err := Select(pkg, res, opts); err != nil || whenNames(sel.Models()) != "base" {
		t.Fatalf("a package with no conditional model: %q, %v", whenNames(sel.Models()), err)
	}
}

// A PROFILE NAMES WHAT ITS PLATFORM ALWAYS NEEDS, AND NO CONDITIONAL MODEL.
// With a profile the selection is the models it names and the conditional
// ones that hold, in the declaration's order; a conditional model is the
// same on every platform, so a profile that names one is refused.
func TestAProfileNamesNoConditionalModel(t *testing.T) {
	decls := []ModelDecl{
		whenModel("stt-metal", nil),
		whenModel("fr-pack", &ModelWhen{Setting: "tts_language", Values: []string{"fr"}}),
		whenModel("stt-cuda", nil),
		whenModel("tts", nil),
	}
	french := map[string]any{"tts_language": "fr"}
	got, err := selectModels(decls, &AcceleratorProfile{Models: []string{"tts", "stt-metal"}}, french)
	if err != nil || whenNames(got) != "stt-metal fr-pack tts" {
		t.Fatalf("a profile's models with the pack that holds: %q, %v", whenNames(got), err)
	}
	got, err = selectModels(decls, &AcceleratorProfile{Models: []string{"tts", "stt-metal"}}, map[string]any{"tts_language": "en"})
	if err != nil || whenNames(got) != "stt-metal tts" {
		t.Fatalf("a profile's models when no pack holds: %q, %v", whenNames(got), err)
	}
	if _, err := selectModels(decls, &AcceleratorProfile{Models: []string{"tts", "fr-pack"}}, french); err == nil || !strings.Contains(err.Error(), "needed only for some values of a setting") {
		t.Fatalf("a profile naming a conditional model must be refused: %v", err)
	}
	// With no profile every unconditional model is taken, and a
	// conditional one still only while it holds.
	if got, err := selectModels(decls, nil, nil); err != nil || whenNames(got) != "stt-metal stt-cuda tts" {
		t.Fatalf("no profile, nothing held: %q, %v", whenNames(got), err)
	}
	if got, err := selectModels(decls, nil, french); err != nil || whenNames(got) != "stt-metal fr-pack stt-cuda tts" {
		t.Fatalf("no profile, French: %q, %v", whenNames(got), err)
	}
	// A value that is not a string meets no condition.
	if got, _ := selectModels(decls, nil, map[string]any{"tts_language": 3}); whenNames(got) != "stt-metal stt-cuda tts" {
		t.Fatalf("a number met a condition: %q", whenNames(got))
	}
}

// THE FORM OF A CONDITION IS HELD WHERE THE DECLARATION IS READ, before any
// setting is in hand: a key that is a setting's key, one to sixty-four
// values, none empty and none twice. An explicit null is no condition.
func TestTheFormOfAModelsCondition(t *testing.T) {
	decl := func(when string) []byte {
		return []byte(`[{"name":"pack","url":"https://example.com/pack","sha256":"` + strings.Repeat("a", 64) + `","size":1024` + when + `}]`)
	}
	many := make([]string, MaxModelWhenValues+1)
	for i := range many {
		many[i] = `"v` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `"`
	}
	for name, c := range map[string]struct{ when, refusal string }{
		"a key that is not a setting's key": {`,"when":{"setting":"TTS Language","values":["fr"]}`, "is not a setting key"},
		"no values":                         {`,"when":{"setting":"tts_language","values":[]}`, "must name 1.."},
		"too many values":                   {`,"when":{"setting":"tts_language","values":[` + strings.Join(many, ",") + `]}`, "must name 1.."},
		"a value twice":                     {`,"when":{"setting":"tts_language","values":["fr","fr"]}`, "twice or names nothing"},
		"an empty value":                    {`,"when":{"setting":"tts_language","values":[""]}`, "twice or names nothing"},
		"a member the condition lacks":      {`,"when":{"setting":"tts_language","values":["fr"],"platform":"linux"}`, "platform"},
	} {
		if _, err := ParseModels(decl(c.when)); err == nil || !strings.Contains(err.Error(), c.refusal) {
			t.Errorf("%s: want a refusal saying %q, got: %v", name, c.refusal, err)
		}
	}
	got, err := ParseModels(decl(`,"when":{"setting":"tts_language","values":["fr","de"]}`))
	if err != nil || got[0].When == nil || got[0].When.Setting != "tts_language" || len(got[0].When.Values) != 2 {
		t.Fatalf("a well-formed condition: %+v, %v", got, err)
	}
	if got, err := ParseModels(decl(`,"when":null`)); err != nil || got[0].When != nil {
		t.Fatalf("an explicit null is no condition: %+v, %v", got, err)
	}
}
