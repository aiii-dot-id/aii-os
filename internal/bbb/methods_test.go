package bbb

import "testing"

// .
// .
// .
func TestMethodImportMappingPinned(t *testing.T) {
	pairs := map[string]string{
		"rpc-connect":               "rpc.connect",
		"plugin-register-interface": "plugin.register_interface",
		"invoke-call":               "invoke.call",
		"rpc-cancel":                "rpc.cancel",
		"observe-subscribe":         "observe.subscribe",
		"heartbeat-signal":          "heartbeat.signal",
		"heartbeat-tempo-request":   "heartbeat.tempo_request",
		"heartbeat-config":          "heartbeat.config",
	}
	if len(methodByImport) != len(pairs) {
		t.Fatalf("import surface has %d entries, want %d — the eight-function surface is closed", len(methodByImport), len(pairs))
	}
	for imp, method := range pairs {
		if got, ok := MethodForImport(imp); !ok || got != method {
			t.Fatalf("MethodForImport(%q) = %q %v, want %q", imp, got, ok, method)
		}
		if got, ok := ImportForMethod(method); !ok || got != imp {
			t.Fatalf("ImportForMethod(%q) = %q %v, want %q", method, got, ok, imp)
		}
	}
	// .
	// .
	for _, notImport := range []string{"plugin.invoke", "observe.event", "rpc.disconnect", "nope"} {
		if _, ok := ImportForMethod(notImport); ok {
			t.Fatalf("%q must not map to an import", notImport)
		}
	}
	if _, ok := MethodForImport("plugin-invoke"); ok {
		t.Fatal("plugin-invoke is the guest EXPORT, not an import")
	}
}
