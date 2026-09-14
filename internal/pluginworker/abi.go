package pluginworker

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
// .
// .

// .
// .
// .
const BBBWITModule = "aiii:bbb/bbb"

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
var bbbImportNames = []string{
	"rpc-connect",
	"plugin-register-interface",
	"invoke-call",
	"rpc-cancel",
	"observe-subscribe",
	"heartbeat-signal",
	"heartbeat-tempo-request",
	"heartbeat-config",
}

// .
const (
	// .
	// .
	// .
	ExportProtocolVersion = "aiii-plugin-bbb-protocol-version"

	// .
	// .
	ExportSmoke = "aiii-plugin-smoke"

	// .
	// .
	// .
	// .
	ExportPluginInvoke = "plugin-invoke"

	// .
	// .
	ExportPostReturn = "cabi_post_" + ExportPluginInvoke

	// .
	ExportRealloc = "cabi_realloc"

	// .
	ExportMemory = "memory"

	// .
	// .
	ExportInitialize = "_initialize"

	// .
	// .
	// .
	// .
	// .
	ExportOnEvent = "on_event"

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	ExportDescribe = "aiii-plugin-describe"

	// .
	ExportPostDescribe = "cabi_post_" + ExportDescribe
)

// .
const (
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	RequiredProtocolVersion uint32 = 2

	// .
	// .
	RequiredSmokeCode uint32 = 1
)
