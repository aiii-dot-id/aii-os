package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// channelPkg builds a channel adapter; confirms names the operation, if
// any, whose descriptor declares operator_confirms.
func channelPkg(t *testing.T, id, confirms string) string {
	t.Helper()
	const descriptor = "interfaces/aii.channel.v1.schema.json"
	op := func(name, effects string) string {
		if name == confirms {
			return fmt.Sprintf(`{"id":%q,"effects":%q,"capabilities":[],"operator_confirms":true}`, name, effects)
		}
		return fmt.Sprintf(`{"id":%q,"effects":%q,"capabilities":[]}`, name, effects)
	}
	files := map[string][]byte{
		descriptor:                               []byte(`[` + op(MethodSend, "write.external") + `,` + op(MethodReceive, "read.external") + `,` + op(MethodDescribe, "read.internal") + `]`),
		"variants/linux-x86_64-wasm/plugin.wasm": fixtureWasm(t, "responder.wasm"),
	}
	raw := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: ChannelInterfaceID, Version: ChannelInterfaceVersion, SchemaFile: descriptor, Methods: []string{MethodSend, MethodReceive, MethodDescribe}}},
		[]packagetest.VariantSpec{{ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox", Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm"}}, files, nil)
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["plugin_family"] = "channel_adapter"
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return writePkg(t, packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: raw, InstallFiles: files})
}

func TestAChannelOperationThatWaitsForConfirmationIsRefused(t *testing.T) {
	h, err := broker.New(broker.Config{Store: newBrokerStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	prop := &recordingProposer{}
	reg := newRegistry(t)
	for _, method := range []string{MethodSend, MethodReceive, MethodDescribe} {
		ap, err := Activate(t.Context(), channelPkg(t, "org.example.confirmed-"+method, method), reg, &Options{Broker: h, Acts: prop})
		var schema *SchemaError
		if !errors.As(err, &schema) || schema.Operation != method || !strings.Contains(err.Error(), "operator_confirms") {
			if ap != nil {
				_ = ap.Deactivate(context.Background())
			}
			t.Fatalf("%s: activation answered %v (%T), want a SchemaError naming %s and operator_confirms", method, err, err, method)
		}
		if !schema.WaitsOnPackage() {
			t.Fatalf("%s: the refusal is the package's until its bytes change", method)
		}
	}
	if len(prop.proposals) != 0 {
		t.Fatalf("a refused channel proposed %d act(s)", len(prop.proposals))
	}

	ok, err := Activate(t.Context(), channelPkg(t, "org.example.plain", ""), reg, &Options{Broker: h, Acts: prop})
	if err != nil {
		t.Fatalf("the control, a channel with no flag, did not activate: %v", err)
	}
	t.Cleanup(func() { _ = ok.Deactivate(context.Background()) })
	if ok.Channel == nil || ok.Channel.Send == "" {
		t.Fatalf("the control did not activate as a channel: %+v", ok.Channel)
	}
}
