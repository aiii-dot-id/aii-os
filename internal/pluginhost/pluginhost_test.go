package pluginhost

// The quarantine-harness battery (threat model §8): a verified T0
// package's guest runs behind the wazero wall with zero capabilities,
// its signed operations are identity-callable tools, and every
// containment promise is proven against real packages built around the
// real pluginworker wasm fixtures.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// fixtureWasm reads a pluginworker guest fixture — the same artifacts
// the worker suite pins to their wasmgen source.
func fixtureWasm(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v (run `go generate ./internal/pluginworker`)", name, err)
	}
	return b
}

// pkgSpec builds the canonical spec for a T0 plugin package wrapping
// one wasm guest, declaring one interface with the given methods. The
// variant is built FOR THE RUNNING HOST (selection is exact-match; a
// hardcoded platform would only activate on that platform's CI).
func pkgSpec(id, version string, methods []string, wasm []byte) packagetest.PackageSpec {
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm":     wasm,
	}
	manifest := packagetest.BuildManifestJSON(id, version,
		[]packagetest.InterfaceSpec{{
			ID: "quarantine.probe", Version: 1,
			SchemaFile: "interfaces/quarantine.probe.v1.schema.json",
			Methods:    methods,
		}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	return packagetest.PackageSpec{Root: id + "-" + version, Manifest: manifest, InstallFiles: files}
}

func writePkg(t *testing.T, spec packagetest.PackageSpec) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), spec.Root+".aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func buildPkg(t *testing.T, id, version string, methods []string, wasm string) string {
	t.Helper()
	return writePkg(t, pkgSpec(id, version, methods, fixtureWasm(t, wasm)))
}

func newRegistry(t *testing.T) *tools.Registry {
	t.Helper()
	return tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
}

func activate(t *testing.T, pkg string, reg *tools.Registry) *ActivePlugin {
	t.Helper()
	ap, err := Activate(context.Background(), pkg, reg, nil)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	return ap
}

// The milestone path end to end: identity → registry → BBB → worker →
// result. A T0 responder package activates against a REAL registry,
// its operation appears as a tool with the plugin's origin, and
// calling it through the registry returns the operation_result.
func TestActivateResponderRegistersAndInvokes(t *testing.T) {
	reg := newRegistry(t)
	before := len(reg.Names())
	ap := activate(t, buildPkg(t, "org.example.responder", "0.1.0", []string{"ping"}, "responder.wasm"), reg)

	if ap.Tier != packagefmt.TierT0 {
		t.Fatalf("unsigned harness package must be T0, got %s", ap.Tier)
	}
	wantTool := "pl_org_example_responder_ping"
	if len(ap.ToolNames) != 1 || ap.ToolNames[0] != wantTool {
		t.Fatalf("tool naming scheme drifted: %v, want [%s]", ap.ToolNames, wantTool)
	}
	if _, ok := reg.Get(wantTool); !ok {
		t.Fatalf("activated operation must be registered, names: %v", reg.Names())
	}

	res, err := reg.Execute(context.Background(), wantTool, map[string]interface{}{"probe": true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("tool must return the guest's operation_result, got %+v", res)
	}

	// Deactivate removes the tool and only the tool.
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(wantTool); ok {
		t.Fatal("Deactivate must deregister the plugin's tools")
	}
	if len(reg.Names()) != before {
		t.Fatalf("builtins must be untouched: %v", reg.Names())
	}
	if _, err := reg.Execute(context.Background(), wantTool, nil); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("a deactivated tool must be honestly unknown, got %v", err)
	}
}

// The response contract: the echo guest returns the REQUEST — a frame
// with a method member and no result|error — which is not a response.
// The handler refuses it typed, naming the violation.
func TestEchoGuestViolatesResponseContract(t *testing.T) {
	reg := newRegistry(t)
	ap := activate(t, buildPkg(t, "org.example.echo", "0.1.0", []string{"ping"}, "echo.wasm"), reg)

	_, err := reg.Execute(context.Background(), ap.ToolNames[0], nil)
	var rce *ResponseContractError
	if !errors.As(err, &rce) {
		t.Fatalf("an echoed request must be a typed ResponseContractError, got %v", err)
	}
	if !strings.Contains(rce.Requirement, "method") {
		t.Fatalf("the violation must name the method member, got %q", rce.Requirement)
	}
	if !strings.Contains(rce.Got, "invoke.call") {
		t.Fatalf("the error must carry the evidence, got %q", rce.Got)
	}
}

// The zero-capability proof: the caller guest forwards the request out
// through the aiii:bbb/bbb invoke-call import. The deny-all worker
// answers the audited POLICY_DENY error object; the guest relays it as
// its response body, and the harness surfaces exactly that denial —
// nothing was performed, nothing else came back.
func TestCallerGuestSurfacesQuarantineDenial(t *testing.T) {
	reg := newRegistry(t)
	ap := activate(t, buildPkg(t, "org.example.caller", "0.1.0", []string{"ping"}, "caller.wasm"), reg)

	_, err := reg.Execute(context.Background(), ap.ToolNames[0], nil)
	var rce *ResponseContractError
	if !errors.As(err, &rce) {
		t.Fatalf("the relayed denial object is not a response envelope; want typed contract error, got %v", err)
	}
	for _, evidence := range []string{"POLICY_DENY", "-32000"} {
		if !strings.Contains(rce.Got, evidence) {
			t.Fatalf("the surfaced denial must carry %q, got %q", evidence, rce.Got)
		}
	}
}

// Tamper after manifest: content that no longer matches the signed-in
// package_hash refuses activation before any guest byte runs, and the
// registry gains nothing.
func TestTamperedPackageRefusesActivation(t *testing.T) {
	reg := newRegistry(t)
	before := reg.Names()

	spec := pkgSpec("org.example.tampered", "0.1.0", []string{"ping"}, fixtureWasm(t, "responder.wasm"))
	// The manifest above was computed over the honest bytes; now the
	// payload changes underneath it.
	spec.InstallFiles["variants/linux-x86_64-wasm/plugin.wasm"] =
		append([]byte{0xff}, spec.InstallFiles["variants/linux-x86_64-wasm/plugin.wasm"][1:]...)

	_, err := Activate(context.Background(), writePkg(t, spec), reg, nil)
	var perr *packagefmt.Error
	if !errors.As(err, &perr) || perr.Reason != packagefmt.ReasonPackageHashMismatch {
		t.Fatalf("tampered content must refuse as PACKAGE_HASH_MISMATCH, got %v", err)
	}
	if got := reg.Names(); len(got) != len(before) {
		t.Fatalf("a refused package must register nothing: %v", got)
	}
}

// The verified-bytes-are-loaded-bytes invariant, both refusal shapes:
// a member the verified digest map does not name, and a digest map
// that disagrees with the extracted bytes (the file-swapped-between-
// passes window). White-box on loadVerifiedMember — the public path
// cannot reach these states because Verify and ReadMember read the
// same honest file, which is exactly why the invariant needs its own
// proof.
func TestEntrypointDigestMismatchRefused(t *testing.T) {
	spec := pkgSpec("org.example.responder", "0.1.0", []string{"ping"}, fixtureWasm(t, "responder.wasm"))
	path := writePkg(t, spec)
	res, err := packagefmt.VerifyFile(path, packagefmt.TrustRoots{})
	if err != nil {
		t.Fatal(err)
	}
	const entry = "variants/linux-x86_64-wasm/plugin.wasm"

	// Not in the digest map: refuse before reading anything.
	_, err = loadVerifiedMember(path, &packagefmt.Result{FileDigests: map[string]string{}}, entry)
	var derr *EntrypointDigestError
	if !errors.As(err, &derr) || derr.Want != "" {
		t.Fatalf("a member outside the verified digest map must refuse named, got %v", err)
	}

	// Doctored digest: extracted bytes no longer hash to the record.
	doctored := &packagefmt.Result{FileDigests: map[string]string{
		entry: "sha256:" + strings.Repeat("0", 64),
	}}
	_, err = loadVerifiedMember(path, doctored, entry)
	if !errors.As(err, &derr) || derr.Want == "" || derr.Got == "" {
		t.Fatalf("a digest mismatch must refuse naming both digests, got %v", err)
	}

	// The honest result loads.
	if _, err := loadVerifiedMember(path, res, entry); err != nil {
		t.Fatalf("honest bytes must load: %v", err)
	}
}

// Origin-gated SAFE for free: the plugin tool registered under the
// plugin id's origin is suspended wholesale in SAFE mode while builtin
// read-only tools continue (SAFE_MODE_PLUGIN_LIFECYCLE: total,
// exception-free) — using the Registry's existing mechanism, untouched.
func TestSafeModeSuspendsPluginTool(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, nil, tools.Timeouts{})
	safe := false
	reg.SetSafeSource(func() (string, bool) { return "test corruption", safe })

	ap := activate(t, buildPkg(t, "org.example.responder", "0.1.0", []string{"ping"}, "responder.wasm"), reg)

	// Live outside SAFE.
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], nil)
	if err != nil || res.Error != "" {
		t.Fatalf("outside SAFE the plugin tool must run: %v %+v", err, res)
	}

	safe = true
	res, err = reg.Execute(context.Background(), ap.ToolNames[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("SAFE must suspend the plugin tool wholesale, got %+v", res)
	}

	// The builtin read-only diagnostic surface continues.
	probe := filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(probe, []byte("alive"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = reg.Execute(context.Background(), "read", map[string]interface{}{"file_path": probe})
	if err != nil || !strings.Contains(res.Output, "alive") {
		t.Fatalf("SAFE must keep builtin read alive: %v %+v", err, res)
	}
}

// Name discipline fails activation closed — and totally. A cross-plugin
// collision aborts the second activation with the first untouched; a
// within-manifest collision and an over-ceiling name register NOTHING
// (no partial plugin ever remains).
func TestToolNameCollisionFailsClosed(t *testing.T) {
	reg := newRegistry(t)
	first := activate(t, buildPkg(t, "org.example.dup", "0.1.0", []string{"ping"}, "responder.wasm"), reg)

	// Same id, same method, different release: the same tool name.
	_, err := Activate(context.Background(),
		buildPkg(t, "org.example.dup", "0.2.0", []string{"ping"}, "responder.wasm"), reg, nil)
	var terr *ToolNameError
	if !errors.As(err, &terr) {
		t.Fatalf("a registry name collision must be a typed ToolNameError, got %v", err)
	}
	// The first plugin's tool still works.
	if res, err := reg.Execute(context.Background(), first.ToolNames[0], nil); err != nil || res.Error != "" {
		t.Fatalf("the incumbent tool must survive the refused activation: %v %+v", err, res)
	}

	// Within one manifest: "pi.ng" and "pi_ng" sanitize identically —
	// registered count must be ZERO after the abort, not one.
	before := len(reg.Names())
	_, err = Activate(context.Background(),
		buildPkg(t, "org.example.selfdup", "0.1.0", []string{"pi.ng", "pi_ng"}, "responder.wasm"), reg, nil)
	if !errors.As(err, &terr) {
		t.Fatalf("a sanitize collision must be a typed ToolNameError, got %v", err)
	}
	if len(reg.Names()) != before {
		t.Fatalf("a failed activation must roll back every registered tool: %v", reg.Names())
	}

	// Over the 64-byte provider ceiling: refused, never truncated.
	_, err = Activate(context.Background(),
		buildPkg(t, "org.example.long", "0.1.0", []string{strings.Repeat("m", 80)}, "responder.wasm"), reg, nil)
	if !errors.As(err, &terr) || !strings.Contains(terr.Reason, "ceiling") {
		t.Fatalf("an over-length name must refuse naming the ceiling, got %v", err)
	}
	if len(reg.Names()) != before {
		t.Fatalf("the over-length refusal must register nothing: %v", reg.Names())
	}
}

// Per-operation input schemas packaged in the .aiiospkg must reach the
// LLM-facing tool: a descriptor declaring "input":"schemas/ping_in.json"
// and a schema file declaring an integer parameter must make
// Parameters() return that schema — not the open-object fallback. This
// is the fix for the memory.get bug where "id" arrived as a string
// because the LLM never saw the integer type.
func TestInputSchemaForwardedToToolParameters(t *testing.T) {
	reg := newRegistry(t)
	wasm := fixtureWasm(t, "responder.wasm")

	// The descriptor is an array of operation descriptors (the real
	// plugin format), each carrying an "input" path.
	descriptor := []byte(`[{"id":"ping","input":"schemas/ping_in.json","output":"schemas/ping_out.json"}]`)
	// The input schema declares one integer parameter.
	inputSchema := []byte(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"]}`)
	outputSchema := []byte(`{"type":"object"}`)

	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": descriptor,
		"schemas/ping_in.json":                       inputSchema,
		"schemas/ping_out.json":                      outputSchema,
		"variants/linux-x86_64-wasm/plugin.wasm":     wasm,
	}
	manifest := packagetest.BuildManifestJSON("org.example.schematest", "0.1.0",
		[]packagetest.InterfaceSpec{{
			ID: "quarantine.probe", Version: 1,
			SchemaFile: "interfaces/quarantine.probe.v1.schema.json",
			Methods:    []string{"ping"},
		}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)

	pkgPath := writePkg(t, packagetest.PackageSpec{
		Root: "org.example.schematest-0.1.0", Manifest: manifest, InstallFiles: files,
	})
	ap := activate(t, pkgPath, reg)

	tool, ok := reg.Get(ap.ToolNames[0])
	if !ok {
		t.Fatalf("tool %s not registered", ap.ToolNames[0])
	}
	params := tool.Parameters()

	// The schema must declare "id" as an integer — not the open-object
	// fallback (which has empty properties and additionalProperties:true).
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties map, got %T: %+v", params["properties"], params)
	}
	idField, ok := props["id"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected id field in properties, got: %+v", props)
	}
	if idType, _ := idField["type"].(string); idType != "integer" {
		t.Fatalf("id parameter must be declared integer, got %q: %+v", idType, idField)
	}
}

// Without packaged schema files (the old shape: descriptor is just
// {"interface":"quarantine.probe","v":1}), Parameters() falls back to
// the open-object schema — the prior behavior, unchanged.
func TestNoSchemaFileFallsBackToOpenSchema(t *testing.T) {
	reg := newRegistry(t)
	ap := activate(t, buildPkg(t, "org.example.noschema", "0.1.0", []string{"ping"}, "responder.wasm"), reg)

	tool, ok := reg.Get(ap.ToolNames[0])
	if !ok {
		t.Fatalf("tool %s not registered", ap.ToolNames[0])
	}
	params := tool.Parameters()

	if params["type"] != "object" {
		t.Fatalf("fallback schema must be type object, got %v", params["type"])
	}
	if ap, ok := params["additionalProperties"].(bool); !ok || !ap {
		t.Fatalf("fallback schema must allow additionalProperties, got %v", params["additionalProperties"])
	}
}

// A package the harness cannot run refuses in the right layer: a
// wasm-less plugin dies at the tier contract (packagefmt's
// WASM_BASELINE_MISSING — never reaches the harness), and an AOT-only
// package — a valid baseline the pure-Go worker has no lane for — dies
// at selection with the missing runtime lane NAMED.
func TestNoRunnableVariantRefused(t *testing.T) {
	reg := newRegistry(t)
	buildWith := func(id, runtime, profile string) string {
		files := map[string][]byte{
			"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
			"variants/only/plugin.bin":                   []byte("\x00asm\x01\x00\x00\x00stub"),
		}
		manifest := packagetest.BuildManifestJSON(id, "0.1.0",
			[]packagetest.InterfaceSpec{{
				ID: "quarantine.probe", Version: 1,
				SchemaFile: "interfaces/quarantine.probe.v1.schema.json",
				Methods:    []string{"ping"},
			}},
			[]packagetest.VariantSpec{{
				ID: "only", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
				Topology: packagefmt.HostTopology(), Runtime: runtime, Profile: profile,
				Entrypoint: "variants/only/plugin.bin",
			}},
			files, nil)
		return writePkg(t, packagetest.PackageSpec{
			Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files,
		})
	}

	_, err := Activate(context.Background(), buildWith("org.example.native", "service_process", "standard"), reg, nil)
	var perr *packagefmt.Error
	if !errors.As(err, &perr) || perr.Reason != packagefmt.ReasonWASMBaselineMissing {
		t.Fatalf("a wasm-less T0 package must refuse at the tier contract, got %v", err)
	}

	_, err = Activate(context.Background(), buildWith("org.example.aot", "wasm_aot_component", "wasm_sandbox"), reg, nil)
	var serr *VariantSelectionError
	if !errors.As(err, &serr) || len(serr.Refusals) != 1 {
		t.Fatalf("an AOT-only package must refuse at selection naming the variant, got %v", err)
	}
	if !strings.Contains(err.Error(), "runtime:wasm_aot_component") {
		t.Fatalf("the refusal must name the missing runtime lane in the C requirement spelling, got %v", err)
	}

	for _, name := range []string{"pl_org_example_native_ping", "pl_org_example_aot_ping"} {
		if _, ok := reg.Get(name); ok {
			t.Fatalf("nothing may register from a refused package: %s", name)
		}
	}
}
