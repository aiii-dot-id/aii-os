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

// effectsPkg builds a T0 package around a host-calling guest whose one
// operation "roundtrip" carries the given descriptor entry, with the
// capability envelope caps (top-level and variant, as the kit emits).
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
		files, map[string]any{"capability_envelope": caps})
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

// A read-only operation cannot write: the guest's kv.put during an
// operation declared read.internal is refused by the operation's own
// word, before the grant that would have allowed it.
func TestAReadOnlyOperationCannotWrite(t *testing.T) {
	out := runRoundtrip(t, "org.example.readonly",
		[]byte(`[{"id":"roundtrip","effects":"read.internal","capabilities":["ring4.kv"]}]`))
	if !strings.Contains(out, "declares effects read.internal") || !strings.Contains(out, "POLICY_DENY") {
		t.Fatalf("the write must be refused by the effect class, got %s", out)
	}
}

// An operation that declared no capabilities requested nothing: a host
// call outside its declared list is refused even though the envelope
// and the grant would allow it.
func TestAnOperationCannotUseACapabilityItDidNotDeclare(t *testing.T) {
	out := runRoundtrip(t, "org.example.undeclaredcap",
		[]byte(`[{"id":"roundtrip","effects":"write.local","capabilities":[]}]`))
	if !strings.Contains(out, "did not declare ring4.kv") {
		t.Fatalf("a capability outside the operation's declared list must be refused, got %s", out)
	}
}

// An operation whose descriptor names neither effects nor capabilities
// keeps the envelope and the grant as its only bounds: the write goes
// through.
func TestAnUndeclaredOperationIsBoundByTheEnvelopeAlone(t *testing.T) {
	out := runRoundtrip(t, "org.example.undeclared", []byte(`[{"id":"roundtrip"}]`))
	if !strings.Contains(out, `"stored":true`) {
		t.Fatalf("with nothing declared the envelope and grant decide, got %s", out)
	}
}

// A declared class within which the call falls lets it through.
func TestAWriteClassOperationMayWrite(t *testing.T) {
	out := runRoundtrip(t, "org.example.writer",
		[]byte(`[{"id":"roundtrip","effects":"write.local","capabilities":["ring4.kv"]}]`))
	if !strings.Contains(out, `"stored":true`) {
		t.Fatalf("a write within the declared class must succeed, got %s", out)
	}
}

// An effect class the contract does not know refuses the activation.
func TestAnUnknownEffectClassRefusesActivation(t *testing.T) {
	reg := newRegistry(t)
	pkg := effectsPkg(t, "org.example.badeffects", []byte(`[{"id":"roundtrip","effects":"sideways"}]`), kvPutGuest(), []string{"ring4.kv"})
	_, err := Activate(context.Background(), pkg, reg, nil)
	var se *SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "sideways") {
		t.Fatalf("want a SchemaError naming the class, got %v", err)
	}
}
