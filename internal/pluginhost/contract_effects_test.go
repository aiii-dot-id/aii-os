package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// .
// .
// .
func effectsPkg(t *testing.T, id string, descriptor []byte, guest []byte, caps []string) string {
	t.Helper()
	files := map[string][]byte{
		"interfaces/broker.probe.v1.schema.json": descriptor,
		"variants/linux-x86_64-wasm/plugin.wasm": guest,
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "broker.probe", Version: 1, SchemaFile: "interfaces/broker.probe.v1.schema.json", Methods: []string{"roundtrip"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm", Capabilities: caps,
		}},
		files, map[string]interface{}{"capability_envelope": caps})
	return writePkg(t, packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files})
}

func kvPutGuest() []byte {
	return wasmgen.CannedCaller([]byte(`{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`))
}

func runRoundtrip(t *testing.T, id string, descriptor []byte) string {
	t.Helper()
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
	if err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t)
	pkg := effectsPkg(t, id, descriptor, kvPutGuest(), []string{"ring4.kv"})
	ap, err := Activate(context.Background(), pkg, reg, &Options{Broker: h})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res.Output + res.Error
}

// .
// .
// .
func TestAReadOnlyOperationCannotWrite(t *testing.T) {
	out := runRoundtrip(t, "org.example.readonly",
		[]byte(`[{"id":"roundtrip","effects":"read.internal","capabilities":["ring4.kv"]}]`))
	if !strings.Contains(out, "declares effects read.internal") || !strings.Contains(out, "POLICY_DENY") {
		t.Fatalf("the write must be refused by the effect class, got %s", out)
	}
}

// .
// .
// .
func TestAnOperationCannotUseACapabilityItDidNotDeclare(t *testing.T) {
	out := runRoundtrip(t, "org.example.undeclaredcap",
		[]byte(`[{"id":"roundtrip","effects":"write.local","capabilities":[]}]`))
	if !strings.Contains(out, "did not declare ring4.kv") {
		t.Fatalf("a capability outside the operation's declared list must be refused, got %s", out)
	}
}

// .
// .
// .
func TestAnUndeclaredOperationIsBoundByTheEnvelopeAlone(t *testing.T) {
	out := runRoundtrip(t, "org.example.undeclared", []byte(`[{"id":"roundtrip"}]`))
	if !strings.Contains(out, `"stored":true`) {
		t.Fatalf("with nothing declared the envelope and grant decide, got %s", out)
	}
}

// .
func TestAWriteClassOperationMayWrite(t *testing.T) {
	out := runRoundtrip(t, "org.example.writer",
		[]byte(`[{"id":"roundtrip","effects":"write.local","capabilities":["ring4.kv"]}]`))
	if !strings.Contains(out, `"stored":true`) {
		t.Fatalf("a write within the declared class must succeed, got %s", out)
	}
}

// .
func TestAnUnknownEffectClassRefusesActivation(t *testing.T) {
	reg := newRegistry(t)
	pkg := effectsPkg(t, "org.example.badeffects", []byte(`[{"id":"roundtrip","effects":"sideways"}]`), kvPutGuest(), []string{"ring4.kv"})
	_, err := Activate(context.Background(), pkg, reg, nil)
	var se *SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "sideways") {
		t.Fatalf("want a SchemaError naming the class, got %v", err)
	}
}
