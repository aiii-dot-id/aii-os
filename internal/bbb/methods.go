package bbb

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
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

// .
// .
var importByMethod = func() map[string]string {
	inv := make(map[string]string, len(methodByImport))
	for imp, method := range methodByImport {
		inv[method] = imp
	}
	return inv
}()

// .
// .
func MethodForImport(importName string) (string, bool) {
	m, ok := methodByImport[importName]
	return m, ok
}

// .
// .
// .
func ImportForMethod(method string) (string, bool) {
	imp, ok := importByMethod[method]
	return imp, ok
}
