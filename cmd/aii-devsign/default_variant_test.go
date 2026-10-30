package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoVariants stages a spec with two variants on this host's coordinates
// (so the ephemeral path verifies it) plus the extra top-level fields.
func twoVariants(t *testing.T, extra map[string]any) string {
	t.Helper()
	dir := stage(t)
	entry := filepath.Join(dir, "install-root", "variants", "native-b", "memoryd")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("\x7fELFfake-native-memoryd-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "devsign.json")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	first := spec["variants"].([]any)[0].(map[string]any)
	second := map[string]any{}
	for k, v := range first {
		second[k] = v
	}
	second["id"], second["entrypoint"] = "native-b", "variants/native-b/memoryd"
	spec["variants"] = []any{first, second}
	for k, v := range extra {
		spec[k] = v
	}
	edited, _ := json.Marshal(spec)
	if err := os.WriteFile(specPath, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func payloadOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(phase1(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	var pair map[string]string
	if err := json.Unmarshal(raw, &pair); err != nil {
		t.Fatal(err)
	}
	return pair
}

// The SDK's package signs its default_variant; the platform-signed bundle
// carries the same one, a non-first one included, inside the signed hash.
func TestDevsignSignsTheDefaultVariant(t *testing.T) {
	dir := twoVariants(t, map[string]any{"default_variant": "native-b"})
	out := filepath.Join(t.TempDir(), "memoryd.aiiospkg")
	var stdout, stderr bytes.Buffer
	if rc := runDevsign([]string{"-staging", dir, "-o", out, "-root-out", filepath.Join(t.TempDir(), "root.pub.json")}, &stdout, &stderr); rc != 0 {
		t.Fatalf("devsign rc=%d stderr=%s", rc, stderr.String())
	}
	if got := manifestOf(t, out)["default_variant"]; got != "native-b" {
		t.Fatalf("signed manifest default_variant = %v, want native-b", got)
	}
	with, without := payloadOf(t, dir), payloadOf(t, twoVariants(t, nil))
	if with["package_hash"] != without["package_hash"] || with["manifest_hash"] == without["manifest_hash"] {
		t.Fatalf("default_variant must change the manifest hash and nothing else: with %v, without %v", with, without)
	}
}

// A spec without the field signs the manifest it signed before the field
// existed: the pair below is what ed352cb6's devsign produced for it.
func TestDevsignWithoutADefaultSignsTodaysManifest(t *testing.T) {
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"interfaces/memory.core.v1.schema.json": `{"interface":"memory.core","v":1}`,
		"variants/native/memoryd":               "\x7fELFfake-native-memoryd",
		"variants/native-b/memoryd":             "\x7fELFfake-native-memoryd-b",
	} {
		p := filepath.Join(dir, "install-root", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	variant := func(id, entry string) map[string]any {
		return map[string]any{"id": id, "platform": "linux", "arch": "x86_64", "topology": "full_identity_host",
			"runtime": "native_t3_component", "profile": "platform_reserved", "entrypoint": entry}
	}
	raw, _ := json.Marshal(map[string]any{
		"id": "org.example.memoryd", "version": "0.1.0",
		"interfaces": []map[string]any{{"id": "memory.core", "version": 1,
			"schema_file": "interfaces/memory.core.v1.schema.json", "methods": []string{"focus.set"}}},
		"variants": []any{variant("native", "variants/native/memoryd"), variant("native-b", "variants/native-b/memoryd")},
	})
	if err := os.WriteFile(filepath.Join(dir, "devsign.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"package_hash":  "sha256:37ae42e5550c8c6bb6722ed4227719c1b2e2e4628c16fdf3e14d21adba5e5aac",
		"manifest_hash": "sha256:88cb6cb5c0a745b3f56529f64305e3cedd6df5adebb13df07823161c92ca97a2",
	}
	if got := payloadOf(t, dir); got["package_hash"] != want["package_hash"] || got["manifest_hash"] != want["manifest_hash"] {
		t.Fatalf("a spec without default_variant changed its signed pair: got %v, want %v", got, want)
	}
}

// A default naming no declared variant is refused by name before anything
// is written: signed, it would be a publisher choice no host can honour.
func TestDevsignRefusesAnUndeclaredDefault(t *testing.T) {
	dir := twoVariants(t, map[string]any{"default_variant": "native-c"})
	pair := filepath.Join(t.TempDir(), "pair.json")
	var stdout, stderr bytes.Buffer
	rc := runDevsign([]string{"-staging", dir, "-payload-out", pair}, &stdout, &stderr)
	if rc == 0 || !strings.Contains(stderr.String(), `default_variant "native-c" is not a declared variant`) {
		t.Fatalf("an undeclared default must be refused by name: rc=%d stderr=%s", rc, stderr.String())
	}
	if _, err := os.Stat(pair); !os.IsNotExist(err) {
		t.Fatalf("a refused spec wrote a payload: %v", err)
	}
}
