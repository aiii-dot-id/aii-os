package packagefmt

// The requires-predicate grammar (manifest.schema.json
// $defs/requirement, adopted verbatim: sev_manifest.h:293-298 owns the
// class names) and the host-coordinate vocabulary. Hostile-first: the
// closed class set is the load-bearing rule — an invented predicate
// class must kill the manifest, never be skipped into a permanently
// unselectable-but-installed package.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// requiresManifest builds a minimal valid plugin manifest and splices
// requirements JSON into the top level and/or the sole variant.
func requiresManifest(t *testing.T, topRequirements, variantRequirements string) []byte {
	t.Helper()
	variantExtra := ""
	if variantRequirements != "" {
		variantExtra = fmt.Sprintf(`,"requirements":%s`, variantRequirements)
	}
	topExtra := ""
	if topRequirements != "" {
		topExtra = fmt.Sprintf(`,"requirements":%s`, topRequirements)
	}
	raw := fmt.Sprintf(`{
		"kind":"plugin","id":"org.example.req","version":"1.0.0",
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
			"variant_capabilities":[]%s
		}]
	}`, strings.Repeat("0", 64), strings.Repeat("1", 64), topExtra, strings.Repeat("2", 64), variantExtra)
	if !json.Valid([]byte(raw)) {
		t.Fatalf("fixture manifest is not JSON: %s", raw)
	}
	return []byte(raw)
}

func TestRequirementsParseAndExposePerVariant(t *testing.T) {
	m, verr := parseManifest(requiresManifest(t,
		`{"required":["facility:sev_transport.local"],"optional":["backend:cpu"]}`,
		`{"required":["facility:sev_audio.raw_pcm","permission:microphone"],"optional":["backend:metal.mlx"]}`))
	if verr != nil {
		t.Fatalf("valid requirements must parse: %v", verr)
	}
	v := &m.Variants[0]
	got := m.RequiredPredicates(v)
	want := []string{"facility:sev_transport.local", "facility:sev_audio.raw_pcm", "permission:microphone"}
	if len(got) != len(want) {
		t.Fatalf("effective required predicates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("effective required predicates = %v, want %v (release-level first, then variant — the C admission validates both, plugin_host_install.c:1102-1121)", got, want)
		}
	}
	backends := m.BackendDeclarations(v)
	if len(backends) != 2 || backends[0] != "backend:cpu" || backends[1] != "backend:metal.mlx" {
		t.Fatalf("backend declarations must collect required+optional from both levels, got %v", backends)
	}
}

func TestRequirementsRejectUnknownClassFailClosed(t *testing.T) {
	// "silicon:" is exactly the class the seam register refuses to
	// invent (PLATFORM_SEAMS §5: no silicon predicate exists) — a
	// manifest smuggling one dies at the grammar, top level or variant.
	for _, place := range []struct{ top, variant string }{
		{top: `{"required":["silicon:npu"]}`},
		{variant: `{"required":["silicon:npu"]}`},
		{variant: `{"optional":["silicon:npu"]}`},
	} {
		_, verr := parseManifest(requiresManifest(t, place.top, place.variant))
		if verr == nil || verr.Reason != ReasonManifestInvalid {
			t.Fatalf("unknown predicate class must refuse MANIFEST_INVALID, got %v", verr)
		}
		if !strings.Contains(verr.Error(), "unknown predicate class") || !strings.Contains(verr.Error(), "silicon") {
			t.Fatalf("the refusal must name the unknown class, got %v", verr)
		}
	}
}

func TestRequirementsGrammarEdges(t *testing.T) {
	cases := []struct {
		name string
		top  string
		want string // substring of the refusal; "" = must parse
	}{
		{"no colon", `{"required":["facilitysev_transport"]}`, "requirement grammar"},
		{"empty name", `{"required":["facility:"]}`, "requirement grammar"},
		{"empty class", `{"required":[":sev_transport.local"]}`, "requirement grammar"},
		{"uppercase name", `{"required":["facility:SEV_TRANSPORT"]}`, "requirement grammar"},
		{"space in name", `{"required":["facility:a b"]}`, "requirement grammar"},
		{"duplicate entry", `{"required":["backend:cpu","backend:cpu"]}`, "duplicated"},
		{"mistyped list", `{"required":"backend:cpu"}`, "mistyped"},
		{"slash legal in name", `{"required":["distribution:store/eu"]}`, ""},
		{"second colon legal in name? no — colon outside name charset", `{"required":["facility:a:b"]}`, "requirement grammar"},
		{"empty lists legal", `{"required":[],"optional":[]}`, ""},
		{"empty object legal", `{}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, verr := parseManifest(requiresManifest(t, tc.top, ""))
			if tc.want == "" {
				if verr != nil {
					t.Fatalf("must parse: %v", verr)
				}
				_ = m
				return
			}
			if verr == nil || !strings.Contains(verr.Error(), tc.want) {
				t.Fatalf("want refusal containing %q, got %v", tc.want, verr)
			}
		})
	}

	// The schema ceiling: 17 entries refuse.
	entries := make([]string, 17)
	for i := range entries {
		entries[i] = fmt.Sprintf("backend:b%d", i)
	}
	raw, _ := json.Marshal(map[string][]string{"required": entries})
	_, verr := parseManifest(requiresManifest(t, string(raw), ""))
	if verr == nil || !strings.Contains(verr.Error(), "more than 16") {
		t.Fatalf("over-ceiling requirements must refuse, got %v", verr)
	}
}

func TestDefaultVariantGrammar(t *testing.T) {
	base := requiresManifest(t, "", "")
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	set := func(v string) []byte {
		m["default_variant"] = json.RawMessage(fmt.Sprintf("%q", v))
		out, _ := json.Marshal(m)
		return out
	}
	if _, verr := parseManifest(set("v1")); verr != nil {
		t.Fatalf("a grammatical default_variant must parse: %v", verr)
	}
	// A default naming NO declared variant still parses — C compares it
	// against variant ids at selection and a miss is simply inert
	// (manifest.c:1634-1636), adopted rather than second-guessed.
	if _, verr := parseManifest(set("not-declared")); verr != nil {
		t.Fatalf("an inert default_variant is schema-legal: %v", verr)
	}
	if _, verr := parseManifest(set("Bad Name")); verr == nil {
		t.Fatal("an ungrammatical default_variant must refuse")
	}
}

func TestHostCoordinatesAreManifestVocabulary(t *testing.T) {
	p, a, topo := HostPlatform(), HostArch(), HostTopology()
	// The suite runs on hosts inside the five-platform law; the mapping
	// must land inside the C enums, never echo GOOS/GOARCH spellings.
	okPlatform := map[string]bool{"linux": true, "macos": true, "windows": true, "android": true, "ios": true}
	okArch := map[string]bool{"x86_64": true, "arm64": true}
	if !okPlatform[p] || !okArch[a] {
		t.Fatalf("host coordinates %q/%q are outside the manifest vocabulary", p, a)
	}
	if p == "darwin" || a == "amd64" {
		t.Fatal("host coordinates must be manifest vocabulary, not GOOS/GOARCH spellings")
	}
	if topo != "full_identity_host" && topo != "mobile_app_host" {
		t.Fatalf("host topology %q outside the vocabulary", topo)
	}
}

func TestSplitPredicate(t *testing.T) {
	if c, n, ok := SplitPredicate("facility:sev_audio.raw_pcm"); !ok || c != "facility" || n != "sev_audio.raw_pcm" {
		t.Fatalf("split = %q %q %v", c, n, ok)
	}
	for _, bad := range []string{"", "noclass", ":name", "class:"} {
		if _, _, ok := SplitPredicate(bad); ok {
			t.Fatalf("%q must not split", bad)
		}
	}
}
