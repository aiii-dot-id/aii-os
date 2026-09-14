package pluginhost

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
// .

// .
// .
func schemaPackage(t *testing.T, id string, inputSchema []byte) string {
	t.Helper()
	wasm := fixtureWasm(t, "responder.wasm")
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`[{"id":"ping","input":"schemas/ping_in.json","output":"schemas/ping_out.json"}]`),
		"schemas/ping_in.json":                       inputSchema,
		"schemas/ping_out.json":                      []byte(`{"type":"object"}`),
		"variants/linux-x86_64-wasm/plugin.wasm":     wasm,
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
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
	return writePkg(t, packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files})
}

// .
// .
// .
func TestAPackagedRequiredArgumentIsEnforcedBeforeTheGuest(t *testing.T) {
	reg := newRegistry(t)
	pkg := schemaPackage(t, "org.example.required",
		[]byte(`{"type":"object","properties":{"message":{"type":"string"},"count":{"type":"integer"}},"required":["message","count"]}`))
	ap := activate(t, pkg, reg)
	tool, ok := reg.Get(ap.ToolNames[0])
	if !ok {
		t.Fatalf("tool %s not registered", ap.ToolNames[0])
	}
	if _, canonical := tool.Parameters()["required"].([]string); !canonical {
		t.Fatalf("the packaged required list must reach the tool in the canonical shape, got %T", tool.Parameters()["required"])
	}

	before := reg.MalformedCallCount()
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "missing required count, message") {
		t.Fatalf("a call missing the declared arguments must be refused naming them, sorted, before the guest runs: %+v", res)
	}
	if reg.MalformedCallCount() != before+1 {
		t.Fatalf("the rejected call must count exactly once, got %d", reg.MalformedCallCount()-before)
	}

	res, err = reg.Execute(context.Background(), ap.ToolNames[0], map[string]interface{}{"message": "hi", "count": 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Error, "missing required") || strings.Contains(res.Error, "malformed") {
		t.Fatalf("a complete call must reach the guest: %+v", res)
	}
	if reg.MalformedCallCount() != before+1 {
		t.Fatalf("a complete call must not count as malformed, got %d", reg.MalformedCallCount()-before)
	}
}

// .
// .
// .
func TestAMalformedPackagedRequiredRefusesActivation(t *testing.T) {
	for _, c := range []struct{ name, schema, want string }{
		{"scalar", `{"type":"object","required":"message"}`, "is string"},
		{"null", `{"type":"object","required":null}`, "is <nil>"},
		{"object", `{"type":"object","required":{"message":true}}`, "want an array"},
		{"numeric member", `{"type":"object","required":["message",3]}`, "[1] is float64"},
		{"mixed", `{"type":"object","required":[true,"message"]}`, "[0] is bool"},
		{"empty member", `{"type":"object","required":["message",""]}`, "[1] is empty"},
		{"not json", `{"type":"object","required":["message"`, "not valid JSON"},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := newRegistry(t)
			names := len(reg.Names())
			pkg := schemaPackage(t, "org.example.malformed", []byte(c.schema))
			_, err := Activate(context.Background(), pkg, reg, nil)
			if err == nil {
				t.Fatal("a malformed present schema must refuse activation, not open the operation")
			}
			msg := err.Error()
			if !strings.Contains(msg, c.want) || !strings.Contains(msg, `"ping"`) || !strings.Contains(msg, "schemas/ping_in.json") {
				t.Fatalf("the refusal must name the operation, the schema and the fault (%q): %s", c.want, msg)
			}
			if len(reg.Names()) != names {
				t.Fatalf("a refused activation must register nothing: %v", reg.Names())
			}
		})
	}
}

// .
// .
func TestADeclaredEmptyRequiredListActivatesAndProceeds(t *testing.T) {
	reg := newRegistry(t)
	pkg := schemaPackage(t, "org.example.emptyreq", []byte(`{"type":"object","required":[]}`))
	ap := activate(t, pkg, reg)
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Error, "missing required") || strings.Contains(res.Error, "malformed") {
		t.Fatalf("nothing is required; the call must proceed: %+v", res)
	}
	_ = tools.Timeouts{}
}
