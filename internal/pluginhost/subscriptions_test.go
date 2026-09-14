package pluginhost

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// .
// .
func TestSubscriptionDeclarationsAreHeldToThePackage(t *testing.T) {
	methods := []string{"log.event", "log.flush"}
	decls, err := ParseSubscriptions([]byte(`[{"topic":"tool.called","operation":"log.event","filter":{"tool":"read"}},{"topic":"turn.ended","operation":"log.flush"}]`), methods)
	if err != nil || len(decls) != 2 {
		t.Fatalf("a good declaration parses: %v %+v", err, decls)
	}
	if !decls[0].Matches(map[string]interface{}{"tool": "read", "failed": false}) || decls[0].Matches(map[string]interface{}{"tool": "write"}) || decls[0].Matches(map[string]interface{}{}) {
		t.Fatal("the filter is exact")
	}
	if !decls[1].Matches(map[string]interface{}{}) {
		t.Fatal("no filter matches everything")
	}
	for name, tc := range map[string]struct{ raw, want string }{
		"undeclared topic":     {`[{"topic":"disk.full","operation":"log.event"}]`, "not one the host emits"},
		"work topics wanted":   {`[{"topic":"work.delivered","operation":"log.event","filter":{"outcome":"served"}},{"topic":"subagent.spawned","operation":"log.everything"}]`, "not a method"},
		"undeclared operation": {`[{"topic":"tool.called","operation":"log.everything"}]`, "not a method"},
		"unknown member":       {`[{"topic":"tool.called","operation":"log.event","every":"5m"}]`, "unknown field"},
		"bad filter key":       {`[{"topic":"tool.called","operation":"log.event","filter":{"Tool Name":"x"}}]`, "filter"},
		"not a list":           {`{"topic":"tool.called"}`, "not a list"},
	} {
		_, err := ParseSubscriptions([]byte(tc.raw), methods)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}
}

// .
// .
func TestSubscribedOperationsAreHostOps(t *testing.T) {
	const id = "org.example.logger"
	files := map[string][]byte{
		"interfaces/log.sink.v1.schema.json":     []byte(`[{"id":"log.event","summary":"Record an event","effects":"write.local","capabilities":[]},{"id":"log.read","summary":"Read the log","effects":"read.internal","capabilities":[]}]`),
		"variants/linux-x86_64-wasm/plugin.wasm": wasmgen.Responder(),
		SubscriptionsFile:                        []byte(`[{"topic":"tool.called","operation":"log.event"}]`),
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "log.sink", Version: 1, SchemaFile: "interfaces/log.sink.v1.schema.json", Methods: []string{"log.event", "log.read"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	pkg := writePkg(t, packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files})
	reg := newRegistry(t)
	ap, err := Activate(context.Background(), pkg, reg, nil)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	if len(ap.Subscriptions) != 1 || ap.Subscriptions[0].Topic != TopicToolCalled {
		t.Fatalf("the activation carries the declaration: %+v", ap.Subscriptions)
	}
	sink := ToolNameFor(id, "log.event")
	for _, name := range reg.Names() {
		if name == sink {
			t.Fatal("a subscribed operation reached the identity's advertised set")
		}
	}
	if _, ok := reg.Get(sink); !ok {
		t.Fatal("the host can still execute it")
	}
	if _, _, ok := reg.State(ToolNameFor(id, "log.read")); !ok {
		t.Fatal("the plugin's other operation stays discoverable")
	}
	bad := map[string][]byte{}
	for k, v := range files {
		bad[k] = v
	}
	bad[SubscriptionsFile] = []byte(`[{"topic":"disk.full","operation":"log.event"}]`)
	badManifest := packagetest.BuildManifestJSON("org.example.badsub", "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "log.sink", Version: 1, SchemaFile: "interfaces/log.sink.v1.schema.json", Methods: []string{"log.event", "log.read"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		bad, nil)
	badPkg := writePkg(t, packagetest.PackageSpec{Root: "org.example.badsub-0.1.0", Manifest: badManifest, InstallFiles: bad})
	if _, err := Activate(context.Background(), badPkg, newRegistry(t), nil); err == nil || !strings.Contains(err.Error(), "not one the host emits") {
		t.Fatalf("an undeclared topic refuses the activation: %v", err)
	}
}
