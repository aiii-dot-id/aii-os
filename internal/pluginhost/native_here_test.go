package pluginhost

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// WHETHER A PACKAGE RUNS AS A NATIVE PROGRAM HERE IS READ BEFORE ANY
// VARIANT IS CHOSEN, from the variants built for this host alone: native
// when one of them is, whichever selection would choose; not native when
// none is, whatever a variant for another system is. What it is signed for
// is those variants' capabilities, each inside the release's envelope.
func TestWhetherAPackageRunsNativelyHereIsReadFromThisHostsVariants(t *testing.T) {
	variant := func(id, platform, runtime, profile, caps string) packagefmt.Variant {
		return packagefmt.Variant{VariantID: id, Platform: platform, Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			ExecutionRuntime: runtime, AdmissionProfile: profile, VariantCapabilities: json.RawMessage(caps)}
	}
	here := packagefmt.HostPlatform()
	module := variant("module", here, "wasm_component", "wasm_sandbox", `["fs.private"]`)
	engine := variant("engine", here, "native_t3_component", "platform_reserved", `["fs.private","net.outbound:api.example.org:443"]`)
	elsewhere := variant("elsewhere", "plan9", "native_t3_component", "platform_reserved", `["net.local"]`)
	for _, c := range []struct {
		name     string
		variants []packagefmt.Variant
		native   bool
		signed   string
	}{
		{"a module", []packagefmt.Variant{module}, false, "fs.private"},
		{"a native engine", []packagefmt.Variant{engine}, true, "fs.private,net.outbound:api.example.org:443"},
		{"a module and an engine for this host", []packagefmt.Variant{module, engine}, true, "fs.private,net.outbound:api.example.org:443"},
		{"an engine for another system only", []packagefmt.Variant{module, elsewhere}, false, "fs.private"},
		{"nothing that runs", nil, false, ""},
	} {
		res := &packagefmt.Result{Manifest: &packagefmt.Manifest{
			CapabilityEnvelope: json.RawMessage(`["fs.private","net.outbound:api.example.org:443","net.local"]`), Variants: c.variants}}
		native, signed := NativeHere(res)
		if native != c.native || strings.Join(signed, ",") != c.signed {
			t.Errorf("%s: native %v signed for %v; want %v and %s", c.name, native, signed, c.native, c.signed)
		}
	}
}
