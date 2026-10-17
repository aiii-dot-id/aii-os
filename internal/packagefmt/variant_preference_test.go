package packagefmt

import (
	"encoding/json"
	"os"
	"testing"
)

func TestVariantPreferenceSharedVectors(t *testing.T) {
	raw, err := os.ReadFile("../pluginhost/testdata/variant_selection.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Preference []struct {
			Name, Raw, Min string
			OK             bool
		}
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Preference) < 14 {
		t.Fatal("thin vectors")
	}
	for _, v := range vectors.Preference {
		t.Run(v.Name, func(t *testing.T) {
			var base map[string]json.RawMessage
			json.Unmarshal(windowManifest(v.Min, ""), &base)
			var variants []map[string]any
			json.Unmarshal(base["variants"], &variants)
			first := variants[0]
			second := map[string]any{}
			for k, value := range first {
				second[k] = value
			}
			first["variant_id"], first["entrypoint"] = "full", "variants/full/plugin.wasm"
			second["variant_id"], second["entrypoint"] = "small", "variants/small/plugin.wasm"
			base["variants"], _ = json.Marshal([]any{first, second})
			if v.Raw != "" {
				base["variant_preference"] = json.RawMessage(v.Raw)
			}
			written, _ := json.Marshal(base)
			got, err := parseManifest(written)
			if (err == nil) != v.OK {
				t.Fatalf("accepted=%v want=%v: %v", err == nil, v.OK, err)
			}
			if err == nil && (got.VariantPreference != nil) != (v.Raw != "") {
				t.Fatal("presence lost")
			}
		})
	}
	if err := ValidateVariantPreference(json.RawMessage(`[]`), VariantPreferenceMinHost, map[string]bool{}); err == nil {
		t.Fatal("empty order admitted")
	}
}
