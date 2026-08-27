package bbb

// The two audited spellings of the eight BBB methods a plugin can
// originate, and the mapping between them:
//
//   - WIRE method ids — the JSON-RPC "method" strings
//     (sev_method_ids.h:16-25; BBB_V2_AUDIT §6);
//   - WIT import names — the kebab function names of the aiii:bbb/bbb
//     component surface (wit/deps/aiii-bbb/bbb.wit; the C host table
//     wasm_host.c:1138-1155; internal/pluginworker/abi.go pins the
//     same list in declaration order).
//
// The mapping is a fixed table, not a string transform: kebab→wire is
// not mechanical ("plugin-register-interface" carries both a '.' and a
// '_' on the wire). It lives HERE, in the wire layer, because both
// sides of the stdio forwarding channel need it: the worker serializes
// a guest's WIT hostcall as a wire-method REQUEST frame, and the
// supervisor's bridge maps the wire method back to the kebab name the
// broker's HostDispatcher contract speaks (DELTA_D1 D1-1; the ADR-033
// import surface).

// methodByImport maps WIT kebab import name → wire method id.
var methodByImport = map[string]string{
	"rpc-connect":               "rpc.connect",
	"plugin-register-interface": "plugin.register_interface",
	"invoke-call":               "invoke.call",
	"rpc-cancel":                "rpc.cancel",
	"observe-subscribe":         "observe.subscribe",
	"heartbeat-signal":          "heartbeat.signal",
	"heartbeat-tempo-request":   "heartbeat.tempo_request",
	"heartbeat-config":          "heartbeat.config",
}

// importByMethod is the exact inverse, built once at init so the two
// directions can never drift.
var importByMethod = func() map[string]string {
	inv := make(map[string]string, len(methodByImport))
	for imp, method := range methodByImport {
		inv[method] = imp
	}
	return inv
}()

// MethodForImport maps an aiii:bbb/bbb WIT import name to its wire
// method id. ok=false for anything outside the eight-function surface.
func MethodForImport(importName string) (string, bool) {
	m, ok := methodByImport[importName]
	return m, ok
}

// ImportForMethod maps a wire method id to its aiii:bbb/bbb WIT import
// name. ok=false for wire methods that are not plugin-originated BBB
// calls (plugin.invoke, notifications, unknown methods).
func ImportForMethod(method string) (string, bool) {
	imp, ok := importByMethod[method]
	return imp, ok
}
