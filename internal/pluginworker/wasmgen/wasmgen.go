// Package wasmgen hand-assembles the pluginworker test-fixture guests
// as WebAssembly core binaries — the fixtures' single source of truth.
//
// Why hand-assembly: the fixtures must pin the canonical-ABI CORE
// lowering of ../abi.go byte-for-byte, and the C SDK's own scaffold
// toolchain emits whole Component Model binaries (componentize +
// wit-bindgen) — the worker unwraps those by extracting exactly this
// core layer (../component.go), so the fixtures build the layer
// directly and the component fixtures wrap these same core modules.
// dev8 carries no wabt (wat2wasm), so the remaining reproducible path
// is a checked-in generator: `go generate ./internal/pluginworker`
// rebuilds every .wasm in testdata byte-for-byte, and the package
// tests assert the checked-in fixtures match this source (never a
// fixture whose source is not in the repo).
//
// The encoder covers exactly what the fixtures need — nothing more.
// Encoding follows the WebAssembly core binary format (magic/version,
// sections 1/2/3/5/6/7/10/11, LEB128) plus the component wrapping at
// the bottom of this file.
package wasmgen

// valtype bytes.
const vtI32 = 0x7F

// export kinds.
const (
	kindFunc   = 0x00
	kindMemory = 0x02
	kindGlobal = 0x03
)

// Module accumulates one core module. Imports must all be added before
// local functions (index spaces: imported functions first).
type Module struct {
	types      [][]byte // encoded functype entries
	typeKeys   map[string]uint32
	imports    [][]byte
	numImpFunc uint32
	funcTypes  []uint32
	bodies     [][]byte
	memMin     uint32
	globals    [][]byte
	exports    [][]byte
	data       [][]byte // encoded active data segments
	sealed     bool     // true once a local function exists
}

func NewModule(memMinPages uint32) *Module {
	return &Module{typeKeys: map[string]uint32{}, memMin: memMinPages}
}

// Type interns a function type and returns its index.
func (m *Module) Type(params, results []byte) uint32 {
	enc := cat([]byte{0x60}, vecBytes(params), vecBytes(results))
	key := string(enc)
	if idx, ok := m.typeKeys[key]; ok {
		return idx
	}
	idx := uint32(len(m.types))
	m.types = append(m.types, enc)
	m.typeKeys[key] = idx
	return idx
}

// ImportFunc declares one imported function and returns its function
// index.
func (m *Module) ImportFunc(module, name string, typeIdx uint32) uint32 {
	if m.sealed {
		panic("wasmgen: imports must precede local functions")
	}
	m.imports = append(m.imports, cat(nameBytes(module), nameBytes(name), []byte{0x00}, uleb(uint64(typeIdx))))
	idx := m.numImpFunc
	m.numImpFunc++
	return idx
}

// Func declares one local function (extraI32Locals beyond the params)
// and returns its function index.
func (m *Module) Func(typeIdx, extraI32Locals uint32, body []byte) uint32 {
	m.sealed = true
	var locals []byte
	if extraI32Locals == 0 {
		locals = uleb(0)
	} else {
		locals = cat(uleb(1), uleb(uint64(extraI32Locals)), []byte{vtI32})
	}
	m.funcTypes = append(m.funcTypes, typeIdx)
	m.bodies = append(m.bodies, cat(locals, body, []byte{0x0B}))
	return m.numImpFunc + uint32(len(m.bodies)) - 1
}

// GlobalI32 declares one mutable i32 global and returns its index.
func (m *Module) GlobalI32(init int32) uint32 {
	m.globals = append(m.globals, cat([]byte{vtI32, 0x01}, i32Const(init), []byte{0x0B}))
	return uint32(len(m.globals)) - 1
}

func (m *Module) ExportFunc(name string, idx uint32) {
	m.exports = append(m.exports, cat(nameBytes(name), []byte{kindFunc}, uleb(uint64(idx))))
}

func (m *Module) ExportMemory(name string) {
	m.exports = append(m.exports, cat(nameBytes(name), []byte{kindMemory}, uleb(0)))
}

func (m *Module) ExportGlobal(name string, idx uint32) {
	m.exports = append(m.exports, cat(nameBytes(name), []byte{kindGlobal}, uleb(uint64(idx))))
}

// Data declares one active data segment in memory 0 at a constant
// offset (mode 0: i32.const offset, then the byte vector).
func (m *Module) Data(offset int32, content []byte) {
	m.data = append(m.data, cat([]byte{0x00}, i32Const(offset), []byte{0x0B}, vecBytes(content)))
}

// Encode assembles the binary.
func (m *Module) Encode() []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00} // \0asm v1
	out = append(out, section(1, vec(m.types))...)
	if len(m.imports) > 0 {
		out = append(out, section(2, vec(m.imports))...)
	}
	fsec := make([][]byte, len(m.funcTypes))
	for i, t := range m.funcTypes {
		fsec[i] = uleb(uint64(t))
	}
	out = append(out, section(3, vec(fsec))...)
	// One memory, min pages, no declared max: the runtime's
	// WithMemoryLimitPages cap governs (a declared max would let the
	// fixture, not the host, pick the ceiling). NewModule(0) declares
	// no memory at all — the decoy shim, like wit-component's real
	// shim module, has none.
	if m.memMin > 0 {
		out = append(out, section(5, vec([][]byte{cat([]byte{0x00}, uleb(uint64(m.memMin)))}))...)
	}
	if len(m.globals) > 0 {
		out = append(out, section(6, vec(m.globals))...)
	}
	out = append(out, section(7, vec(m.exports))...)
	codes := make([][]byte, len(m.bodies))
	for i, b := range m.bodies {
		codes[i] = cat(uleb(uint64(len(b))), b)
	}
	out = append(out, section(10, vec(codes))...)
	if len(m.data) > 0 {
		out = append(out, section(11, vec(m.data))...)
	}
	return out
}

// --- encoding primitives ---

func uleb(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}

func sleb(v int64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7F)
		v >>= 7
		done := (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0)
		if !done {
			b |= 0x80
		}
		out = append(out, b)
		if done {
			return out
		}
	}
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func vecBytes(b []byte) []byte { return cat(uleb(uint64(len(b))), b) }

func nameBytes(s string) []byte { return vecBytes([]byte(s)) }

func vec(items [][]byte) []byte {
	out := uleb(uint64(len(items)))
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

func section(id byte, content []byte) []byte {
	return cat([]byte{id}, uleb(uint64(len(content))), content)
}

// --- instruction helpers (only what the fixtures use) ---

var (
	opUnreachable = []byte{0x00}
	opI32Add      = []byte{0x6A}
	opI32And      = []byte{0x71}
	opI32Shl      = []byte{0x74}
	opI32Eq       = []byte{0x46}
	opI32LeU      = []byte{0x4D}
)

func i32Const(v int32) []byte     { return cat([]byte{0x41}, sleb(int64(v))) }
func localGet(i uint32) []byte    { return cat([]byte{0x20}, uleb(uint64(i))) }
func localSet(i uint32) []byte    { return cat([]byte{0x21}, uleb(uint64(i))) }
func globalGet(i uint32) []byte   { return cat([]byte{0x23}, uleb(uint64(i))) }
func globalSet(i uint32) []byte   { return cat([]byte{0x24}, uleb(uint64(i))) }
func call(f uint32) []byte        { return cat([]byte{0x10}, uleb(uint64(f))) }
func br(depth uint32) []byte      { return cat([]byte{0x0C}, uleb(uint64(depth))) }
func brIf(depth uint32) []byte    { return cat([]byte{0x0D}, uleb(uint64(depth))) }
func blockVoid(b []byte) []byte   { return cat([]byte{0x02, 0x40}, b, []byte{0x0B}) }
func loopVoid(b []byte) []byte    { return cat([]byte{0x03, 0x40}, b, []byte{0x0B}) }
func ifVoid(b []byte) []byte      { return cat([]byte{0x04, 0x40}, b, []byte{0x0B}) }
func i32Load(off uint32) []byte   { return cat([]byte{0x28, 0x02}, uleb(uint64(off))) }
func i32Store(off uint32) []byte  { return cat([]byte{0x36, 0x02}, uleb(uint64(off))) }
func i32Store8(off uint32) []byte { return cat([]byte{0x3A, 0x00}, uleb(uint64(off))) }
func memorySize() []byte          { return []byte{0x3F, 0x00} }
func memoryGrow() []byte          { return []byte{0x40, 0x00} }
func memoryCopy() []byte          { return []byte{0xFC, 0x0A, 0x00, 0x00} }

// --- component wrapping (the component fixtures only) ---
//
// The component binary layer, adopted from the vendored wasm-encoder
// the SDK builds with: preamble `\0asm` + version u16le 0x000d + layer
// u16le 0x0001 (component.rs:115-120), then sections in the same
// id/LEB-length/payload shape as core sections (component.rs:135-139;
// the section() helper above encodes both). A core-module section's
// payload is one complete core binary, preamble included
// (component/modules.rs:20-30). Ids from ComponentSectionId
// (component.rs:60-85).

const (
	secComponentCustom     = 0 // CoreCustom
	secComponentCoreModule = 1 // CoreModule
	secComponentNested     = 4 // Component (a nested one)
	secComponentAlias      = 6 // Alias
)

func componentHeader() []byte {
	return []byte{0x00, 0x61, 0x73, 0x6D, 0x0D, 0x00, 0x01, 0x00}
}
