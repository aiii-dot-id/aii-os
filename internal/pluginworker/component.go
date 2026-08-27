package pluginworker

// Component-binary unwrapping — the walker that lets Load admit both
// artifact classes the ecosystem produces.
//
// The SDK toolchain emits WASM *components* (binary layer 1): the
// wit-bindgen core module wrapped in component-level type, alias,
// canonical and instantiation sections, alongside auxiliary core
// modules. The vendored wit-component the SDK builds with embeds the
// main module raw and unchanged (encoding.rs:428-433) and adds a
// "shim" plus a "fixup" module whenever an import lowering needs
// canonical-ABI options (encoding.rs:1216-1222) — which every
// list<u8>-carrying aiii:bbb/bbb import does (encoding/world.rs:
// 503-507), so real SDK components carry several embedded modules.
// wazero runs *core* modules (layer 0). The bridge is extraction, not
// interpretation: walk the component's top-level sections, collect the
// embedded core modules, and admit the ONE that exports the ADR-033
// world surface. Component-level sections are never interpreted — the
// core lowering pinned in abi.go is the contract, and everything the
// component layer adds (types, aliases, canon lifts) is a redundant
// description of what the worker already enforces at that boundary.
//
// This is an unwrapper, not a component-model runtime (KISS): no
// nested components — the SDK toolchain emits those only for exported
// interfaces (wit-component encoding.rs:739-748, 941-943) and the
// ADR-033 world exports only world-level functions (wit/plugin.wit) —
// no instantiation graph, no adapter linking. Anything outside the
// verified shape is a typed rejection, never a guess: this parser
// feeds on untrusted bytes, so every length is bounds-checked and
// every malformation is an error, never a panic.
//
// Format facts, verified against the SDK's own vendored toolchain
// (aii-os-plugin-sdk/sdk/rust/vendor — the same crates that build the
// artifacts):
//
//   - preambles: a core module is `\0asm` + version u16le 0x0001 +
//     layer u16le 0x0000 (wasm-encoder core.rs:129-134; wasmparser
//     parser.rs:21,33,696); a component is `\0asm` + version u16le
//     0x000d + layer u16le 0x0001 (wasm-encoder component.rs:115-120;
//     wasmparser parser.rs:31,34,697);
//   - after the preamble a component is a run of sections: one id byte
//     with the high bit clear (wasmparser parser.rs:734-737 "malformed
//     section id"), a LEB128 u32 payload length (five bytes at most,
//     no overflow bits — binary_reader.rs:441-472), then the payload
//     (wasm-encoder component.rs:135-139);
//   - section id 1 is a core-module section whose payload is one
//     COMPLETE core wasm binary, its own preamble included
//     (wasm-encoder ComponentSectionId::CoreModule component.rs:65,
//     whole module bytes embedded: component/modules.rs:20-30 and
//     builder.rs:173-180; wasmparser hands the payload to a fresh
//     nested parser: parser.rs:843-872);
//   - section id 4 is a nested component (wasm-encoder component.rs:70;
//     wasmparser parser.rs:381) — rejected here, see above;
//   - every other id is skipped by declared length, matching the
//     vendored parser's own posture (unknown sections are yielded
//     uninterpreted, parser.rs:937-946).

import "fmt"

// MaxArtifactBytes caps the artifact (either class) at admission, and
// independently caps each core module embedded in a component: 64 MiB,
// adopted verbatim from the C host, which refuses to even read a
// component file past this size (SEV_WASM_HOST_COMPONENT_FILE_LIMIT_
// BYTES, sev_wasm_host.h:68; enforced in wasm_host_read_component_file,
// wasm_host.c:1762-1764). The C stack's larger 256 MiB ceiling is for
// wasmtime-AOT artifacts (sev_wasm_host.h:69) — a path this pure-Go
// worker does not have.
const MaxArtifactBytes = 64 << 20

// ArtifactClass names which artifact class Load admitted — telemetry
// for the worker banner and the step-5 supervisor.
type ArtifactClass string

const (
	// ArtifactCoreModule is a layer-0 core wasm module, run as-is.
	ArtifactCoreModule ArtifactClass = "core-module"

	// ArtifactComponent is a layer-1 component binary; its single
	// world-exporting embedded core module was extracted and admitted
	// in its place.
	ArtifactComponent ArtifactClass = "component"
)

// The two artifact preambles (provenance in the package comment above).
var (
	preambleCore      = [8]byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	preambleComponent = [8]byte{0x00, 0x61, 0x73, 0x6D, 0x0D, 0x00, 0x01, 0x00}
)

// Component section ids the walker acts on (wasm-encoder
// ComponentSectionId, component.rs:60-85; wasmparser parser.rs:375-395).
const (
	componentSectionCoreModule = 1
	componentSectionNested     = 4
)

// Core-module encoding the candidate scan touches — the same layout
// wasmgen writes (wasmgen.go section()/ExportFunc): export section id
// 7, entries name/kind/index, function kind 0x00.
const (
	coreSectionExport = 7
	coreExportFunc    = 0x00
)

// worldSurfaceExports is the candidate-selection key: the four
// function exports every conformant guest core module carries (abi.go
// pins each to its audited source). The auxiliary modules
// wit-component adds never carry them — the shim exports functions
// named "0","1",… (encoding.rs:2976: shim names are decimal strings)
// plus the "$imports" funcref table (encoding.rs:93,1180), and the
// fixup module exports nothing at all (encoding.rs:1202-1210) — so
// exactly one module matching is the well-formed case.
var worldSurfaceExports = []string{
	ExportPluginInvoke,
	ExportProtocolVersion,
	ExportSmoke,
	ExportRealloc,
}

// unwrapArtifact classifies artifact bytes by preamble and returns the
// core module to run: a core module passes through unchanged; a
// component yields its single world-exporting embedded module. Fail
// closed: oversize artifacts, unrecognized preambles, malformed
// structure, and zero or multiple candidates are each their own typed
// rejection.
func unwrapArtifact(artifact []byte) ([]byte, ArtifactClass, error) {
	if len(artifact) > MaxArtifactBytes {
		return nil, "", &ArtifactTooLargeError{What: "artifact", Size: len(artifact), Limit: MaxArtifactBytes}
	}
	if len(artifact) < 8 {
		return nil, "", &ArtifactFormatError{Offset: 0, Detail: "shorter than the 8-byte wasm preamble"}
	}
	switch [8]byte(artifact[:8]) {
	case preambleCore:
		return artifact, ArtifactCoreModule, nil
	case preambleComponent:
		core, err := unwrapComponent(artifact)
		if err != nil {
			return nil, "", err
		}
		return core, ArtifactComponent, nil
	default:
		return nil, "", &ArtifactFormatError{Offset: 0, Detail: fmt.Sprintf(
			"preamble % x is neither a core module (% x) nor a component (% x)",
			artifact[:8], preambleCore, preambleComponent)}
	}
}

// unwrapComponent walks the component's top-level sections, collects
// the embedded core modules, and returns the one exporting the world
// surface.
func unwrapComponent(artifact []byte) ([]byte, error) {
	var modules [][]byte
	off := 8 // past the component preamble
	for off < len(artifact) {
		idOff := off
		id := artifact[off]
		if id&0x80 != 0 {
			return nil, &ArtifactFormatError{Offset: idOff, Detail: fmt.Sprintf("malformed section id 0x%02x", id)}
		}
		off++
		length, n, err := readULEB32(artifact, off, "section length")
		if err != nil {
			return nil, err
		}
		off += n
		if uint64(length) > uint64(len(artifact)-off) {
			return nil, &ArtifactFormatError{Offset: off, Detail: fmt.Sprintf(
				"section id %d declares %d payload bytes but only %d remain", id, length, len(artifact)-off)}
		}
		payload := artifact[off : off+int(length)]
		switch id {
		case componentSectionCoreModule:
			// Redundant under the artifact ceiling — a section cannot
			// outgrow its container — but enforced locally so the
			// walker's bounds never depend on its caller's.
			if len(payload) > MaxArtifactBytes {
				return nil, &ArtifactTooLargeError{What: "embedded core module", Size: len(payload), Limit: MaxArtifactBytes}
			}
			modules = append(modules, payload)
		case componentSectionNested:
			return nil, &NestedComponentError{Offset: idOff}
		default:
			// Type/alias/canon/instance/custom/export sections carry
			// the component-level description of the contract the
			// worker already enforces at the core boundary — skipped,
			// never interpreted.
		}
		off += int(length)
	}

	var matches []int
	for i, mod := range modules {
		ok, err := exportsWorldSurface(mod, i)
		if err != nil {
			return nil, err
		}
		if ok {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 1:
		return modules[matches[0]], nil
	case 0:
		return nil, &NoCandidateModuleError{EmbeddedModules: len(modules)}
	default:
		return nil, &AmbiguousCandidateError{Modules: matches, EmbeddedModules: len(modules)}
	}
}

// exportsWorldSurface reports whether embedded module idx exports the
// full world surface, by decoding ONLY its export section. Everything
// else is skipped by declared length: wazero fully validates the one
// module that is actually admitted, and the others are never run, so
// decoding more of them here would be verification theater.
func exportsWorldSurface(mod []byte, idx int) (bool, error) {
	where := fmt.Sprintf("embedded core module %d: ", idx)
	if len(mod) < 8 || [8]byte(mod[:8]) != preambleCore {
		// A module section's payload is a complete core binary,
		// preamble included (wasmparser parser.rs:855-862).
		return false, &ArtifactFormatError{Offset: 0, Detail: where + "payload does not begin with the core-module preamble"}
	}
	off := 8
	for off < len(mod) {
		id := mod[off]
		if id&0x80 != 0 {
			return false, &ArtifactFormatError{Offset: off, Detail: fmt.Sprintf("%smalformed section id 0x%02x", where, id)}
		}
		off++
		length, n, err := readULEB32(mod, off, where+"section length")
		if err != nil {
			return false, err
		}
		off += n
		if uint64(length) > uint64(len(mod)-off) {
			return false, &ArtifactFormatError{Offset: off, Detail: fmt.Sprintf(
				"%ssection id %d declares %d payload bytes but only %d remain", where, id, length, len(mod)-off)}
		}
		if id == coreSectionExport {
			return scanExportSection(mod[off:off+int(length)], where)
		}
		off += int(length)
	}
	// No export section at all is a legal core module (wit-component's
	// fixup module has none, encoding.rs:1202-1210) — a non-candidate,
	// not an error.
	return false, nil
}

// scanExportSection decodes one core export section — vec(name kind
// index), the exact layout wasmgen's ExportFunc/ExportMemory emit —
// and reports whether all four world-surface functions are present.
func scanExportSection(sec []byte, where string) (bool, error) {
	var found uint // bit per worldSurfaceExports entry: distinct names, immune to duplicate exports
	count, off, err := readULEB32(sec, 0, where+"export count")
	if err != nil {
		return false, err
	}
	for i := uint32(0); i < count; i++ {
		nameLen, n, err := readULEB32(sec, off, where+"export name length")
		if err != nil {
			return false, err
		}
		off += n
		if uint64(nameLen) > uint64(len(sec)-off) {
			return false, &ArtifactFormatError{Offset: off, Detail: where + "export name overruns the export section"}
		}
		name := string(sec[off : off+int(nameLen)])
		off += int(nameLen)
		if off >= len(sec) {
			return false, &ArtifactFormatError{Offset: off, Detail: where + "export entry truncated before its kind byte"}
		}
		kind := sec[off]
		off++
		if _, n, err := readULEB32(sec, off, where+"export index"); err != nil {
			return false, err
		} else {
			off += n
		}
		if kind != coreExportFunc {
			continue // memory/table/global exports never satisfy the key
		}
		for bit, want := range worldSurfaceExports {
			if name == want {
				found |= 1 << bit
				break
			}
		}
	}
	if off != len(sec) {
		// Exact consumption, the vendored parser's own posture for
		// section contents (wasmparser SectionLimited).
		return false, &ArtifactFormatError{Offset: off, Detail: where + "export section payload longer than its declared entries"}
	}
	return found == (1<<len(worldSurfaceExports))-1, nil
}

// readULEB32 decodes one unsigned LEB128 u32 at off, mirroring the
// vendored parser's rules (wasmparser binary_reader.rs:441-472): five
// bytes at most, and the fifth byte may neither continue nor carry
// bits past 32. Returns the value and the encoded width.
func readULEB32(b []byte, off int, what string) (uint32, int, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		if off+i >= len(b) {
			return 0, 0, &ArtifactFormatError{Offset: off, Detail: what + " truncated mid-LEB128"}
		}
		c := b[off+i]
		if i == 4 && c&0xF0 != 0 {
			// A set continuation bit (0x80) or overflow bits (0x70) on
			// the fifth byte — both malformed for a u32.
			return 0, 0, &ArtifactFormatError{Offset: off, Detail: what + " is not a valid LEB128 u32"}
		}
		v |= uint32(c&0x7F) << (7 * i)
		if c&0x80 == 0 {
			return v, i + 1, nil
		}
	}
	// Unreachable: the fifth iteration always returns or errors above;
	// kept as a rejection so a future edit cannot fall through to a
	// silent success.
	return 0, 0, &ArtifactFormatError{Offset: off, Detail: what + " is not a valid LEB128 u32"}
}
