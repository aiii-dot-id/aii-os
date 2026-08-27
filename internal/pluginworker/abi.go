package pluginworker

// The lowered ABI — adopted, not invented.
//
// The ADR-033 world (aiii:plugin, wit/plugin.wit) is a Component Model
// contract; wazero runs core modules. This file pins the CORE lowering
// of that world exactly as the SDK's own toolchain emits it inside
// components — the Legacy canonical-ABI mangling of the vendored
// wit-component/wit-parser the SDK builds with:
//
//   - world-level function exports keep their bare kebab WIT name
//     (wit-parser lib.rs core_export_name, Mangling::Legacy: the
//     interface-less arm returns the function name verbatim);
//   - interface imports arrive as core imports whose MODULE is the
//     canonical interface id — "aiii:bbb/bbb" — and whose FIELD is the
//     kebab function name (wit-component validation.rs Legacy
//     module_to_interface; the C host binds the identical instance
//     name, sev_wasm_host.h:52 SEV_WASM_HOST_BBB_WIT_MODULE);
//   - flattening: MAX_FLAT_PARAMS=16, MAX_FLAT_RESULTS=1 (wit-parser
//     abi.rs:182-184). list<u8> flattens to (ptr:i32, len:i32); a
//     list<u8> RESULT overflows the single flat result, so a guest
//     EXPORT returns an i32 pointer to an 8-byte return area
//     {ptr:u32le@0, len:u32le@4}, and a guest IMPORT takes an extra
//     trailing i32 return-area pointer and returns nothing (abi.rs
//     wasm_signature, GuestExport/GuestImport retptr arms);
//   - the allocation bridge is the exported
//     cabi_realloc(old_ptr, old_len, align, new_len) -> ptr
//     (validation.rs Legacy export_realloc; the SDK's own bridge,
//     sdk/rust/aiii/src/lib.rs:42-77). The host lowers list arguments
//     into guest memory through it (align 1 for list<u8>);
//   - the optional post-return export is "cabi_post_" + export name
//     (validation.rs:2493-2494); the linear memory export is "memory"
//     and the optional reactor initializer is "_initialize"
//     (validation.rs Legacy export_memory/export_initialize).
//
// Component binaries are unwrapped at load: the walker in component.go
// extracts the single embedded core module exporting this surface, and
// that module — the raw wit-bindgen output, embedded unchanged by the
// vendored wit-component (encoding.rs:428-433) — is what runs here.
// WASI p2 capability mapping is still LATER work (delta-spec finding):
// a core module that imports WASI fails the import wall, wrapped in a
// component or not.

// BBBWITModule is the canonical import namespace, byte-identical to the
// component instance name the C host links (sev_wasm_host.h:52) and to
// the WIT package id (wit/deps/aiii-bbb/bbb.wit).
const BBBWITModule = "aiii:bbb/bbb"

// bbbImportNames is the complete host-import surface of the world — the
// eight aiii:bbb/bbb functions, kebab names, in WIT declaration order
// (wit/deps/aiii-bbb/bbb.wit; C host table wasm_host.c:1138-1155
// WASM_HOST_BBB_WIT_IMPORTS; ADR-033 Decision 6 lines 212-215). Every
// one lowers to core (param i32 i32 i32) -> () : params_ptr,
// params_len, return-area ptr.
//
// Divergence note (finding): ADR-033 Decision 3 prose (lines 125-132)
// also names rpc_connect-style CORE functions under module "aiii:bbb"
// with underscore names and (i32,i32)->i32 signatures, and only five of
// them. That surface exists in the C host solely as the provider
// conformance probe (wasm_host.c:1129-1136 WASM_HOST_BBB_IMPORTS,
// wasm_host_make_i32_result_type); SDK guests never import it. This
// package implements the WIT surface the SDK emits and the C component
// path links — the ABI as implemented, not the probe.
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

// Guest export names of the ADR-033 world under the Legacy lowering.
const (
	// ExportProtocolVersion is `aiii-plugin-bbb-protocol-version:
	// func() -> u32` (wit/plugin.wit; sev_wasm_host.h:62). Core:
	// () -> i32.
	ExportProtocolVersion = "aiii-plugin-bbb-protocol-version"

	// ExportSmoke is `aiii-plugin-smoke: func() -> u32`
	// (wit/plugin.wit; sev_wasm_host.h:63). Core: () -> i32.
	ExportSmoke = "aiii-plugin-smoke"

	// ExportPluginInvoke is the canonical daemon-to-plugin entrypoint
	// `plugin-invoke: func(request: list<u8>) -> list<u8>` (ADR-033
	// Decision 6 lines 203-204; sev_wasm_host.h:64). Core:
	// (i32,i32) -> i32 return-area pointer.
	ExportPluginInvoke = "plugin-invoke"

	// ExportPostReturn frees the plugin-invoke return list after the
	// host has copied it out (Legacy "cabi_post_" prefix). Optional.
	ExportPostReturn = "cabi_post_" + ExportPluginInvoke

	// ExportRealloc is the canonical allocation bridge.
	ExportRealloc = "cabi_realloc"

	// ExportMemory is the guest linear memory.
	ExportMemory = "memory"

	// ExportInitialize is the optional Legacy reactor initializer,
	// run once at instantiation when present.
	ExportInitialize = "_initialize"

	// ExportOnEvent is the ADR-033 push-notification export:
	// on_event(topic_ptr, topic_len, payload_ptr, payload_len) — a
	// core-style signature straight from the ADR (line 161), no
	// results. Optional at load; see the package doc's divergence
	// note (the C implementation never calls it yet).
	ExportOnEvent = "on_event"
)

// Admission constants.
const (
	// RequiredProtocolVersion is bbb_protocol_version — the
	// capability-contract number the aiii-plugin-bbb-protocol-version
	// export must return. BBB_V2_AUDIT.md §1 pins which of the two
	// audited version numbers governs this export: the manifest const
	// 2 (schema `"bbb_protocol_version": const 2`), NOT the RPC
	// envelope's protocol_version = 1. The C host and the Rust SDK
	// agree (sev_wasm_host.h:65-66 → sev_manifest.h:351;
	// sdk/rust/aiii/src/lib.rs BBB_PROTOCOL_VERSION = 2).
	RequiredProtocolVersion uint32 = 2

	// RequiredSmokeCode is the aiii-plugin-smoke conformance code
	// (sev_wasm_host.h:67 SEV_WASM_HOST_EXPECTED_SMOKE_CODE).
	RequiredSmokeCode uint32 = 1

	// retAreaBytes is the size of a canonical-ABI return area for one
	// list<u8>: {ptr:u32le, len:u32le}.
	retAreaBytes = 8
)
