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
package wasmgen

// .
const vtI32 = 0x7F

// .
const (
	kindFunc   = 0x00
	kindMemory = 0x02
	kindGlobal = 0x03
)

// .
// .
type Module struct {
	types      [][]byte
	typeKeys   map[string]uint32
	imports    [][]byte
	numImpFunc uint32
	funcTypes  []uint32
	bodies     [][]byte
	memMin     uint32
	globals    [][]byte
	exports    [][]byte
	data       [][]byte
	sealed     bool
}

func NewModule(memMinPages uint32) *Module {
	return &Module{typeKeys: map[string]uint32{}, memMin: memMinPages}
}

// .
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

// .
// .
func (m *Module) ImportFunc(module, name string, typeIdx uint32) uint32 {
	if m.sealed {
		panic("wasmgen: imports must precede local functions")
	}
	m.imports = append(m.imports, cat(nameBytes(module), nameBytes(name), []byte{0x00}, uleb(uint64(typeIdx))))
	idx := m.numImpFunc
	m.numImpFunc++
	return idx
}

// .
// .
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

// .
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

// .
// .
func (m *Module) Data(offset int32, content []byte) {
	m.data = append(m.data, cat([]byte{0x00}, i32Const(offset), []byte{0x0B}, vecBytes(content)))
}

// .
func (m *Module) Encode() []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	out = append(out, section(1, vec(m.types))...)
	if len(m.imports) > 0 {
		out = append(out, section(2, vec(m.imports))...)
	}
	fsec := make([][]byte, len(m.funcTypes))
	for i, t := range m.funcTypes {
		fsec[i] = uleb(uint64(t))
	}
	out = append(out, section(3, vec(fsec))...)
	// .
	// .
	// .
	// .
	// .
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

// .

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

// .

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

const (
	secComponentCustom     = 0
	secComponentCoreModule = 1
	secComponentNested     = 4
	secComponentAlias      = 6
)

func componentHeader() []byte {
	return []byte{0x00, 0x61, 0x73, 0x6D, 0x0D, 0x00, 0x01, 0x00}
}
