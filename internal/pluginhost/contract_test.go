package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// .
// .
// .
func contractPkg(t *testing.T, id string, descriptor, input, output []byte) string {
	t.Helper()
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": descriptor,
		"variants/linux-x86_64-wasm/plugin.wasm":     fixtureWasm(t, "responder.wasm"),
	}
	if input != nil {
		files["schemas/in.json"] = input
	}
	if output != nil {
		files["schemas/out.json"] = output
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
func TestArgumentsOutsideTheInputSchemaAreRefusedBeforeTheCall(t *testing.T) {
	reg := newRegistry(t)
	pkg := contractPkg(t, "org.example.args",
		[]byte(`[{"id":"ping","input":"schemas/in.json"}]`),
		[]byte(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`), nil)
	ap := activate(t, pkg, reg)
	name := ap.ToolNames[0]
	for _, bad := range []map[string]interface{}{{"id": "x"}, {"id": 0}, {"id": 2, "extra": true}} {
		res, err := reg.Execute(context.Background(), name, bad)
		if err != nil || !strings.Contains(res.Error, "arguments refused before the call") {
			t.Fatalf("%v must be refused before the call, got %v %+v", bad, err, res)
		}
	}
	res, err := reg.Execute(context.Background(), name, map[string]interface{}{"id": 2})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("arguments inside the schema must reach the guest: %v %+v", err, res)
	}
}

// .
// .
// .
func TestAResultOutsideTheOutputSchemaIsRefusedAndReceipted(t *testing.T) {
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t)
	pkg := contractPkg(t, "org.example.outschema",
		[]byte(`[{"id":"ping","output":"schemas/out.json"}]`), nil,
		[]byte(`{"type":"object","properties":{"echoed":{"type":"string"}},"required":["echoed"]}`))
	ap, err := Activate(context.Background(), pkg, reg, &Options{Broker: h})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]interface{}{})
	if err != nil || !strings.Contains(res.Error, "result refused") || !strings.Contains(res.Error, "$.echoed") {
		t.Fatalf("the boolean the guest echoed is not the string the schema declared; want a refusal naming the path, got %v %+v", err, res)
	}
	if strings.Contains(res.Output, "echoed") {
		t.Fatal("a refused result must not reach the identity as output")
	}
	recs, err := st.PluginReceipts("org.example.outschema")
	if err != nil || len(recs) != 1 || recs[0].Operation != "invoke.result" || recs[0].Success {
		t.Fatalf("the refusal must be one host-authored receipt: %v %+v", err, recs)
	}
	if !strings.Contains(string(recs[0].ReceiptJSON), `"plugin_operation":"ping"`) || !strings.Contains(string(recs[0].ReceiptJSON), `"host_authored":true`) {
		t.Fatalf("the receipt names the operation and is host-authored: %s", recs[0].ReceiptJSON)
	}
}

// .
func TestAResultOverTheDeclaredBoundIsRefused(t *testing.T) {
	reg := newRegistry(t)
	pkg := contractPkg(t, "org.example.bound", []byte(`[{"id":"ping","max_result_bytes":4}]`), nil, nil)
	ap := activate(t, pkg, reg)
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]interface{}{})
	if err != nil || !strings.Contains(res.Error, "exceeds the operation's declared bound of 4") {
		t.Fatalf("a 15-byte result over a 4-byte bound must be refused, got %v %+v", err, res)
	}
}

// .
// .
func TestASchemaOutsideTheClosedSubsetRefusesActivation(t *testing.T) {
	reg := newRegistry(t)
	pkg := contractPkg(t, "org.example.subset",
		[]byte(`[{"id":"ping","input":"schemas/in.json"}]`),
		[]byte(`{"type":"object","properties":{"id":{"$ref":"#/elsewhere"}}}`), nil)
	_, err := Activate(context.Background(), pkg, reg, nil)
	var se *SchemaError
	if !errors.As(err, &se) || se.Operation != "ping" || !strings.Contains(err.Error(), "$ref") {
		t.Fatalf("want a SchemaError naming the operation and the keyword, got %v", err)
	}
}
