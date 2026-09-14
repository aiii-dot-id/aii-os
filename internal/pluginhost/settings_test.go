package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// .
// .
func TestSettingsDeclarationVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/settings_decl.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name          string          `json:"name"`
			Decl          json.RawMessage `json:"decl"`
			OK            bool            `json:"ok"`
			ErrorContains string          `json:"error_contains"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) < 15 {
		t.Fatalf("the vectors are thin: %d cases", len(file.Cases))
	}
	for _, c := range file.Cases {
		_, err := ParseSettings(c.Decl)
		switch {
		case c.OK && err != nil:
			t.Errorf("%s: refused: %v", c.Name, err)
		case !c.OK && err == nil:
			t.Errorf("%s: accepted, want a refusal containing %q", c.Name, c.ErrorContains)
		case !c.OK && !strings.Contains(err.Error(), c.ErrorContains):
			t.Errorf("%s: refusal %q does not name %q", c.Name, err, c.ErrorContains)
		}
	}
}

// .
// .
// .
func TestSettingValuesAreHeldToTheDeclaration(t *testing.T) {
	decls, err := ParseSettings([]byte(`[
		{"key":"recall_limit","type":"number","title":"Recall limit","default":5,"minimum":1,"maximum":50},
		{"key":"mode","type":"enum","title":"Mode","values":["fast","exact"],"default":"fast"},
		{"key":"api_key","type":"secret","title":"API key"},
		{"key":"verbose","type":"boolean","title":"Verbose"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSettingValue(decls[0], 60.0); err == nil || !strings.Contains(err.Error(), "above the maximum") {
		t.Fatalf("a number over its maximum: %v", err)
	}
	if err := CheckSettingValue(decls[1], "slow"); err == nil {
		t.Fatal("an enum value outside its values")
	}
	if err := CheckSettingValue(decls[2], "not a handle!"); err == nil {
		t.Fatal("a secret must be a handle name")
	}
	if err := CheckSettingValue(decls[3], "yes"); err == nil {
		t.Fatal("a boolean must be a bool")
	}
	eff := EffectiveSettings(decls, map[string]interface{}{"recall_limit": 8.0, "mode": "slow", "api_key": "acme", "stray": 1, "verbose": nil})
	if eff["recall_limit"] != 8.0 || eff["mode"] != "fast" || eff["api_key"] != "acme" {
		t.Fatalf("effective values: %v", eff)
	}
	if _, ok := eff["stray"]; ok {
		t.Fatal("an undeclared key is never handed over")
	}
	if _, ok := eff["verbose"]; ok {
		t.Fatal("a boolean with no default and no value is absent")
	}
	if got := SettingKeys(decls); strings.Join(got, ",") != "recall_limit,mode,api_key,verbose" {
		t.Fatalf("keys: %v", got)
	}
}

// .
// .
func settingsPkg(t *testing.T, id string, settings string) string {
	t.Helper()
	files := map[string][]byte{
		"interfaces/settings.probe.v1.schema.json": []byte(`[{"id":"roundtrip","summary":"Read my settings","effects":"read.internal","capabilities":[]}]`),
		"variants/linux-x86_64-wasm/plugin.wasm":   wasmgen.CannedCaller([]byte(`{"operation":"settings.get"}`)),
	}
	if settings != "" {
		files[SettingsFile] = []byte(settings)
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "settings.probe", Version: 1, SchemaFile: "interfaces/settings.probe.v1.schema.json", Methods: []string{"roundtrip"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	return writePkg(t, packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files})
}

// .
// .
// .
// .
func TestSettingsReachTheGuestWithoutAGrant(t *testing.T) {
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]interface{}{}
	opts := &Options{Broker: h, Settings: func(id string) map[string]interface{} { return values }}
	reg := newRegistry(t)
	pkg := settingsPkg(t, "org.example.configured", `[{"key":"recall_limit","type":"number","title":"Recall limit","default":5,"minimum":1,"maximum":50},{"key":"api_key","type":"secret","title":"API key"}]`)
	ap, err := Activate(context.Background(), pkg, reg, opts)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	if ap.Version != "0.1.0" || len(ap.Settings) != 2 || ap.Settings[0].Key != "recall_limit" {
		t.Fatalf("the activation carries the declaration and the release: %+v %+v", ap.Version, ap.Settings)
	}

	res, err := reg.Execute(context.Background(), ap.ToolNames[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, `"values":{"recall_limit":5}`) {
		t.Fatalf("the default reaches the guest: %s %s", res.Output, res.Error)
	}
	values["recall_limit"], values["api_key"], values["stray"] = 8.0, "acme", true
	res, _ = reg.Execute(context.Background(), ap.ToolNames[0], nil)
	if !strings.Contains(res.Output, `"values":{"api_key":"acme","recall_limit":8}`) {
		t.Fatalf("the operator's values reach the guest live, undeclared keys never: %s", res.Output)
	}
	if !strings.Contains(res.Output, `"host_authored":true`) {
		t.Fatalf("a settings read is receipted like every host call: %s", res.Output)
	}

	bare := settingsPkg(t, "org.example.bare", "")
	ap2, err := Activate(context.Background(), bare, newRegistry(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ap2.Deactivate(context.Background()) })
	if len(ap2.Settings) != 0 {
		t.Fatal("no declaration, no settings")
	}
	res, _ = ap2.reg.Execute(context.Background(), ap2.ToolNames[0], nil)
	if !strings.Contains(res.Output, `"values":{}`) {
		t.Fatalf("a plugin without a declaration reads an empty object: %s", res.Output)
	}

	bad := settingsPkg(t, "org.example.badsettings", `[{"key":"Recall","type":"number","title":"R"}]`)
	if _, err := Activate(context.Background(), bad, newRegistry(t), opts); err == nil || !strings.Contains(err.Error(), "settings.json is not a settings declaration") {
		t.Fatalf("a declaration the host cannot honor refuses the activation: %v", err)
	}
}

// .
// .
// .
// .
func TestMaximalSettingsDeclarationStaysUnderTheMemberCeiling(t *testing.T) {
	values := make([]string, MaxSettingEnumValues)
	labels := map[string]string{}
	for i := range values {
		values[i] = strings.Repeat("v", MaxSettingTitleBytes-4) + string(rune('a'+i/26)) + string(rune('a'+i%26)) + "zz"
		labels[values[i]] = strings.Repeat("l", MaxSettingLabelBytes)
	}
	var decls []map[string]interface{}
	for i := 0; i < MaxSettings; i++ {
		decls = append(decls, map[string]interface{}{
			"key": "k" + strings.Repeat("x", MaxSettingKeyBytes-3) + string(rune('a'+i)), "type": SettingEnum,
			"title": strings.Repeat("t", MaxSettingTitleBytes), "description": strings.Repeat("d", MaxSettingDescBytes),
			"values": values, "labels": labels, "default": values[0], "required": true,
		})
	}
	raw, err := json.Marshal(decls)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= 1<<20 {
		t.Fatalf("a maximal declaration measures %d bytes, not under the 1 MiB member ceiling", len(raw))
	}
	parsed, err := ParseSettings(raw)
	if err != nil {
		t.Fatalf("a maximal declaration parses: %v", err)
	}
	if len(parsed) != MaxSettings || len(parsed[0].Values) != MaxSettingEnumValues || len(parsed[0].Labels) != MaxSettingEnumValues {
		t.Fatalf("parsed shape: %d settings, %d values, %d labels", len(parsed), len(parsed[0].Values), len(parsed[0].Labels))
	}
	t.Logf("maximal declaration: %d bytes", len(raw))
}

// .
// .
// .
func TestIntegersLabelsAndStaleStoredValues(t *testing.T) {
	decls, err := ParseSettings([]byte(`[
		{"key":"top_k","type":"integer","title":"Top-k","default":40,"minimum":1,"maximum":200},
		{"key":"voice","type":"enum","title":"Voice","values":["alba","ryan"],"labels":{"alba":"Alba"},"default":"alba"}]`))
	if err != nil {
		t.Fatal(err)
	}
	topK, voice := decls[0], decls[1]
	if err := CheckSettingValue(topK, 2.5); err == nil || !strings.Contains(err.Error(), "whole number") {
		t.Fatalf("a fraction for an integer: %v", err)
	}
	if err := CheckSettingValue(topK, "3"); err == nil || !strings.Contains(err.Error(), "must be a number") {
		t.Fatalf("a string for an integer: %v", err)
	}
	if err := CheckSettingValue(topK, math.Inf(1)); err == nil {
		t.Fatal("a non-finite number must be refused")
	}
	if err := CheckSettingValue(topK, json.Number("1e400")); err == nil {
		t.Fatal("a number the decoder cannot hold must be refused")
	}
	for _, ok := range []interface{}{3.0, 3, int64(3), json.Number("3")} {
		if err := CheckSettingValue(topK, ok); err != nil {
			t.Fatalf("%T %v is a whole number: %v", ok, ok, err)
		}
	}
	if err := CheckSettingValue(voice, "Alba"); err == nil {
		t.Fatal("a label is shown, never stored: the value is what holds")
	}
	stored := map[string]interface{}{"top_k": 2.5, "voice": "ryan"}
	eff := EffectiveSettings(decls, stored)
	if eff["top_k"] != 40.0 || eff["voice"] != "ryan" {
		t.Fatalf("effective: %v", eff)
	}
	if why := StoredInvalid(topK, stored); !strings.Contains(why, "whole number") {
		t.Fatalf("the stale value is named to the operator: %q", why)
	}
	if why := StoredInvalid(voice, stored); why != "" {
		t.Fatalf("a holding value is not flagged: %q", why)
	}
	if why := StoredInvalid(voice, map[string]interface{}{"voice": "retired"}); !strings.Contains(why, "declared 2 values") && !strings.Contains(why, "must be one of") {
		t.Fatalf("a retired choice is named: %q", why)
	}
	long := make([]string, 40)
	for i := range long {
		long[i] = fmt.Sprintf("loc-%02d", i)
	}
	if err := CheckSettingValue(SettingDecl{Key: "l", Type: SettingEnum, Values: long}, "xx"); err == nil || strings.Contains(err.Error(), "loc-39") {
		t.Fatalf("a long list is counted in the refusal, not spelled out: %v", err)
	}
}

// .
// .
// .
func TestASecretSettingMayCarryAnOAuthHint(t *testing.T) {
	good := `[{"key":"google","type":"secret","title":"Google account","oauth":{"provider":"google","services":["calendar","gmail"]}}]`
	decls, err := ParseSettings([]byte(good))
	if err != nil || len(decls) != 1 || decls[0].OAuth == nil || decls[0].OAuth.Provider != "google" || len(decls[0].OAuth.Services) != 2 {
		t.Fatalf("the hint is read: %+v %v", decls, err)
	}
	for _, bad := range []string{
		`[{"key":"repo","type":"string","title":"Repo","oauth":{"provider":"github"}}]`,
		`[{"key":"g","type":"secret","title":"G","oauth":{"provider":"not a name"}}]`,
		`[{"key":"g","type":"secret","title":"G","oauth":{"provider":"google","services":["Calendar!"]}}]`,
		`[{"key":"g","type":"secret","title":"G","oauth":{"provider":"google","colour":"blue"}}]`,
	} {
		if _, err := ParseSettings([]byte(bad)); err == nil {
			t.Fatalf("refused: %s", bad)
		}
	}
}
