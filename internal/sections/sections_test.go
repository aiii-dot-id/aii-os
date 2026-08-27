package sections

// The section lane's own battery: declaration strictness, registry
// discipline, activation from a real packagetest bundle, and the
// supply-chain wall — a member swapped between the verify pass and the
// extraction pass refuses typed (threat row S4).

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

// assetManifest builds a minimal kind=asset manifest around files
// (static_template: the section asset type — static files the frame
// serves, never host-run code).
func assetManifest(t *testing.T, id string, files map[string][]byte) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{
		"kind": "asset", "id": id, "version": "0.1.0",
		"package_hash": packagetest.ReferencePackageHash(files),
		"asset_type":   "static_template",
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writePkg(t *testing.T, id string, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{
		Root: id + "-0.1.0", Manifest: assetManifest(t, id, files), InstallFiles: files,
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// helloFiles is the four-file authoring shape (UI_FRAME.md §6).
func helloFiles() map[string][]byte {
	return map[string][]byte{
		"section.json": []byte(`{"id":"hello","title":"Hello","slot":"panel",` +
			`"commands":["project.select"],"topics":["status"],"entry":"index.html"}`),
		"index.html": []byte("<!DOCTYPE html><html><head><link rel=\"stylesheet\" href=\"./styles.css\"></head>" +
			"<body><div id=\"status\">…</div><script type=\"module\" src=\"./module.js\"></script></body></html>\n"),
		"module.js":  []byte("import { ready } from '/section-api.js';\nconst api = await ready();\napi.tokens(() => {});\nawait api.data.subscribe('status', s => { document.getElementById('status').textContent = s && s.name ? s.name : '—'; api.resize(document.body.scrollHeight); });\n"),
		"styles.css": []byte("#status { font-family: var(--font, sans-serif); }\n"),
	}
}

func TestParseDeclStrictness(t *testing.T) {
	cases := []struct {
		name, raw, wantIn string
	}{
		{"unknown slot", `{"id":"x","slot":"sidebar"}`, "slot"},
		{"missing id", `{"slot":"panel"}`, "id"},
		{"uppercase id", `{"id":"Hello","slot":"panel"}`, "id"},
		{"id leading dot", `{"id":".x","slot":"panel"}`, "id"},
		{"dotdot entry", `{"id":"x","slot":"panel","entry":"../index.html"}`, "entry"},
		{"absolute entry", `{"id":"x","slot":"panel","entry":"/index.html"}`, "entry"},
		{"non-html entry", `{"id":"x","slot":"panel","entry":"module.js"}`, "entry"},
		{"bad command", `{"id":"x","slot":"panel","commands":["Do It"]}`, "command"},
		{"dup topic", `{"id":"x","slot":"panel","topics":["status","status"]}`, "twice"},
		{"not json", `nope`, "json"},
	}
	for _, c := range cases {
		if _, err := ParseDecl([]byte(c.raw)); err == nil || !strings.Contains(err.Error(), c.wantIn) {
			t.Errorf("%s: want refusal naming %q, got %v", c.name, c.wantIn, err)
		}
	}

	// Defaults fill: title ← id, entry ← index.html; unknown fields
	// tolerated (schema-tolerant, §5).
	d, err := ParseDecl([]byte(`{"id":"pm","slot":"main-tabs","future_field":42}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "pm" || d.Entry != "index.html" {
		t.Fatalf("defaults not applied: %+v", d)
	}
}

func TestRegistryDisciplineAndSafe(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&Section{Decl: Decl{ID: "a", Slot: "rail"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&Section{Decl: Decl{ID: "a", Slot: "dock"}}); err == nil {
		t.Fatal("duplicate id must refuse, never silently replace")
	}
	if err := r.Register(&Section{Decl: Decl{ID: "b", Slot: "dock"}}); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, s := range r.List() {
		ids = append(ids, s.Decl.ID)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("List must be sorted and complete, got %v", ids)
	}
	r.Remove("a")
	if _, ok := r.Get("a"); ok {
		t.Fatal("removed section still resolvable")
	}
	if reason, safe := r.Safe(); safe || reason != "" {
		t.Fatal("nil safe source must read as not-SAFE")
	}
	r.SetSafeSource(func() (string, bool) { return "chain broken", true })
	if reason, safe := r.Safe(); !safe || reason != "chain broken" {
		t.Fatal("safe source not consulted")
	}
}

func TestActivateFromPackageEndToEnd(t *testing.T) {
	pkg := writePkg(t, "org.example.hello", helloFiles())
	sec, err := ActivateFromPackage(pkg, packagefmt.TrustRoots{})
	if err != nil {
		t.Fatal(err)
	}
	defer sec.Close()
	if sec.Decl.ID != "hello" || sec.Decl.Slot != "panel" || sec.PackageID != "org.example.hello" || sec.Dev {
		t.Fatalf("section wrong: %+v", sec)
	}
	// Every install-root file landed in the cache, byte-identical.
	for rel, want := range helloFiles() {
		got, err := os.ReadFile(filepath.Join(sec.Dir, filepath.FromSlash(rel)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("cache member %s wrong: %v", rel, err)
		}
	}
	if err := sec.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sec.Dir); !os.IsNotExist(err) {
		t.Fatal("Close must remove the extraction cache")
	}
}

func TestActivateLaneSignals(t *testing.T) {
	// kind=asset without section.json → skip signal, never an error path
	// that kills the package list (assets have other futures).
	files := map[string][]byte{"prompts/hi.md": []byte("# hi\n")}
	if _, err := ActivateFromPackage(writePkg(t, "org.example.pack", files), packagefmt.TrustRoots{}); !errors.Is(err, ErrAssetNotSection) {
		t.Fatalf("want ErrAssetNotSection, got %v", err)
	}

	// kind=plugin → the plugin-lane signal.
	pfiles := map[string][]byte{
		"interfaces/lane.probe.v1.schema.json": []byte(`{"interface":"lane.probe","v":1}`),
		"variants/v/plugin.wasm":               []byte("\x00asm"),
	}
	manifest := packagetest.BuildManifestJSON("org.example.plug", "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "lane.probe", Version: 1, SchemaFile: "interfaces/lane.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{ID: "v", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/v/plugin.wasm"}},
		pfiles, nil)
	ppath := filepath.Join(t.TempDir(), "plug.aiiospkg")
	if err := os.WriteFile(ppath, packagetest.Build(packagetest.PackageSpec{
		Root: "org.example.plug-0.1.0", Manifest: manifest, InstallFiles: pfiles,
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivateFromPackage(ppath, packagefmt.TrustRoots{}); !errors.Is(err, ErrNotAsset) {
		t.Fatalf("want ErrNotAsset, got %v", err)
	}

	// A declared entry that is not in the package refuses.
	bad := helloFiles()
	delete(bad, "index.html")
	bad["section.json"] = []byte(`{"id":"hello","slot":"panel","entry":"index.html"}`)
	if _, err := ActivateFromPackage(writePkg(t, "org.example.noentry", bad), packagefmt.TrustRoots{}); err == nil || !strings.Contains(err.Error(), "entry") {
		t.Fatalf("missing entry must refuse typed, got %v", err)
	}
}

// TestExtractRefusesTamperedMember is the supply-chain wall (threat row
// S4): the package file changes between the verify pass and the
// extraction pass — same paths, different bytes — and extraction must
// refuse typed on the digest, exactly the loadVerifiedMember
// discipline pluginhost enforces for entrypoints.
func TestExtractRefusesTamperedMember(t *testing.T) {
	files := helloFiles()
	pkg := writePkg(t, "org.example.hello", files)
	res, err := packagefmt.VerifyFile(pkg, packagefmt.TrustRoots{})
	if err != nil {
		t.Fatal(err)
	}

	// Swap the file on disk for a package with identical paths but a
	// modified module.js (independently valid — the attack is the swap,
	// not a malformed archive).
	swapped := helloFiles()
	swapped["module.js"] = []byte("// evil\n" + string(files["module.js"]))
	if err := os.WriteFile(pkg, packagetest.Build(packagetest.PackageSpec{
		Root: "org.example.hello-0.1.0", Manifest: assetManifest(t, "org.example.hello", swapped), InstallFiles: swapped,
	}), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	err = extractVerified(pkg, res, dir)
	var terr *TamperError
	if !errors.As(err, &terr) || terr.Member != "module.js" {
		t.Fatalf("want TamperError on module.js, got %v", err)
	}
}

func TestActivateDev(t *testing.T) {
	dir := t.TempDir()
	for rel, b := range helloFiles() {
		if err := os.WriteFile(filepath.Join(dir, rel), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sec, err := ActivateDev("hello", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !sec.Dev || sec.Dir != dir || sec.Decl.ID != "hello" {
		t.Fatalf("dev section wrong: %+v", sec)
	}
	// Close never touches the operator's directory.
	if err := sec.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "section.json")); err != nil {
		t.Fatal("dev Close must not remove the source directory")
	}
	// The configured id must match the declaration.
	if _, err := ActivateDev("other", dir); err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("id mismatch must refuse naming both ids, got %v", err)
	}
}
