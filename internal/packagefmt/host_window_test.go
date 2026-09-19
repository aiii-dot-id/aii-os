package packagefmt

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
func windowManifest(min, maxExclusive string) []byte {
	extra := ""
	if min != "" {
		extra += fmt.Sprintf(`,"aiios_min_version":%q`, min)
	}
	if maxExclusive != "" {
		extra += fmt.Sprintf(`,"aiios_max_exclusive_version":%q`, maxExclusive)
	}
	return []byte(fmt.Sprintf(`{
		"kind":"plugin","id":"org.example.window","version":"1.0.0",
		"package_hash":"sha256:%s",
		"plugin_family":"tool_bridge","bbb_protocol_version":2,
		"interfaces":{"core":[{"id":"probe.iface","version":1,"schema_hash":"sha256:%s","methods":["probe.ping"]}]},
		"capability_envelope":[]%s,
		"variants":[{
			"variant_id":"v1","platform":"linux","arch":"x86_64",
			"topology":"full_identity_host","execution_runtime":"wasm_component",
			"admission_profile":"wasm_sandbox","entrypoint":"variants/v1/plugin.wasm",
			"artifact_hash":"sha256:%s",
			"implements":{"core":["probe.iface@1"]},
			"variant_capabilities":[]
		}]
	}`, strings.Repeat("0", 64), strings.Repeat("1", 64), extra, strings.Repeat("2", 64)))
}

func TestTheHostWindowIsRetainedAndItsBoundsAreHeld(t *testing.T) {
	m, verr := parseManifest(windowManifest("0.1.6", "0.3.0"))
	if verr != nil {
		t.Fatalf("a declared window must parse: %v", verr)
	}
	if m.AiiosMinVersion != "0.1.6" || m.AiiosMaxExclusiveVersion != "0.3.0" {
		t.Fatalf("the window was dropped on the floor again: %q..%q", m.AiiosMinVersion, m.AiiosMaxExclusiveVersion)
	}
	// .
	// .
	open, verr := parseManifest(windowManifest("", ""))
	if verr != nil || open.AiiosMinVersion != "" || open.AiiosMaxExclusiveVersion != "" {
		t.Fatalf("an undeclared window must stay undeclared: %v", verr)
	}
	for _, tc := range []struct{ name, min, max string }{
		{"a minimum that is not a version", "one.two.three", ""},
		{"a maximum that is not a version", "", "0.2"},
		{"a window with no versions in it", "0.3.0", "0.3.0"},
		{"a window that runs backwards", "0.4.0", "0.2.0"},
	} {
		if _, verr := parseManifest(windowManifest(tc.min, tc.max)); verr == nil {
			t.Fatalf("%s was accepted", tc.name)
		}
	}
}

// .
// .
// .
// .
func windowManifestRaw(minRaw, maxRaw string) []byte {
	m := string(windowManifest("", ""))
	extra := ""
	if minRaw != "" {
		extra += `,"aiios_min_version":` + minRaw
	}
	if maxRaw != "" {
		extra += `,"aiios_max_exclusive_version":` + maxRaw
	}
	return []byte(strings.Replace(m, `"capability_envelope":[]`, `"capability_envelope":[]`+extra, 1))
}

// .
type hostWindowVectors struct {
	Grammar []struct {
		Name, Min, Max string
		OK             bool   `json:"ok"`
		RefusesNaming  string `json:"refuses_naming"`
	} `json:"grammar"`
	Order []struct {
		Name, A, B string
		Cmp        int `json:"cmp"`
	} `json:"order"`
	Presence []struct {
		Name          string
		MinRaw        string `json:"min_raw"`
		MaxRaw        string `json:"max_raw"`
		OK            bool   `json:"ok"`
		RefusesNaming string `json:"refuses_naming"`
	} `json:"presence"`
}

func loadHostWindowVectors(t *testing.T) hostWindowVectors {
	t.Helper()
	raw, err := os.ReadFile("../pluginhost/testdata/host_window.json")
	if err != nil {
		t.Fatal(err)
	}
	var file hostWindowVectors
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Grammar) < 20 || len(file.Order) < 8 || len(file.Presence) < 8 {
		t.Fatalf("the vectors are thin: grammar %d, order %d, presence %d", len(file.Grammar), len(file.Order), len(file.Presence))
	}
	return file
}

// .
// .
// .
// .
// .
// .
// .
func TestHostWindowVectors(t *testing.T) {
	file := loadHostWindowVectors(t)
	judge := func(t *testing.T, name string, verr *Error, ok bool, naming, what string) {
		t.Helper()
		switch {
		case ok && verr != nil:
			t.Errorf("%s: refused: %v", name, verr)
		case !ok && verr == nil:
			t.Errorf("%s: accepted %s, want a refusal naming %q", name, what, naming)
		case !ok && naming != "" && !strings.Contains(verr.Error(), naming):
			t.Errorf("%s: refusal %q does not name %q", name, verr, naming)
		}
	}
	t.Run("grammar", func(t *testing.T) {
		for _, c := range file.Grammar {
			_, verr := parseManifest(windowManifest(c.Min, c.Max))
			judge(t, c.Name, verr, c.OK, c.RefusesNaming, fmt.Sprintf("min=%q max=%q", c.Min, c.Max))
		}
	})
	t.Run("presence: a bound is judged as it was written", func(t *testing.T) {
		for _, c := range file.Presence {
			_, verr := parseManifest(windowManifestRaw(c.MinRaw, c.MaxRaw))
			judge(t, c.Name, verr, c.OK, c.RefusesNaming, fmt.Sprintf("min=%s max=%s", c.MinRaw, c.MaxRaw))
		}
	})
	t.Run("order", func(t *testing.T) {
		for _, c := range file.Order {
			if got := CompareHostBounds(c.A, c.B); got != c.Cmp {
				t.Errorf("%s: compare(%q, %q) = %d, want %d", c.Name, c.A, c.B, got, c.Cmp)
			}
		}
	})
}
