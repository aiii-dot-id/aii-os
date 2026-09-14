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

import "fmt"

// .
// .
// .
// .
// .
// .
// .
// .
const MaxArtifactBytes = 64 << 20

// .
// .
type ArtifactClass string

const (
	// .
	ArtifactCoreModule ArtifactClass = "core-module"

	// .
	// .
	// .
	ArtifactComponent ArtifactClass = "component"
)

// .
var (
	preambleCore      = [8]byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	preambleComponent = [8]byte{0x00, 0x61, 0x73, 0x6D, 0x0D, 0x00, 0x01, 0x00}
)

// .
// .
const (
	componentSectionCoreModule = 1
	componentSectionNested     = 4
)

// .
// .
// .
const (
	coreSectionExport = 7
	coreExportFunc    = 0x00
)

// .
// .
// .
// .
// .
// .
// .
// .
var worldSurfaceExports = []string{
	ExportPluginInvoke,
	ExportProtocolVersion,
	ExportSmoke,
	ExportRealloc,
}

// .
// .
// .
// .
// .
// .
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

// .
// .
// .
func unwrapComponent(artifact []byte) ([]byte, error) {
	var modules [][]byte
	off := 8
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
			// .
			// .
			// .
			if len(payload) > MaxArtifactBytes {
				return nil, &ArtifactTooLargeError{What: "embedded core module", Size: len(payload), Limit: MaxArtifactBytes}
			}
			modules = append(modules, payload)
		case componentSectionNested:
			return nil, &NestedComponentError{Offset: idOff}
		default:
			// .
			// .
			// .
			// .
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

// .
// .
// .
// .
// .
func exportsWorldSurface(mod []byte, idx int) (bool, error) {
	where := fmt.Sprintf("embedded core module %d: ", idx)
	if len(mod) < 8 || [8]byte(mod[:8]) != preambleCore {
		// .
		// .
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
	// .
	// .
	// .
	return false, nil
}

// .
// .
// .
func scanExportSection(sec []byte, where string) (bool, error) {
	var found uint
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
			continue
		}
		for bit, want := range worldSurfaceExports {
			if name == want {
				found |= 1 << bit
				break
			}
		}
	}
	if off != len(sec) {
		// .
		// .
		return false, &ArtifactFormatError{Offset: off, Detail: where + "export section payload longer than its declared entries"}
	}
	return found == (1<<len(worldSurfaceExports))-1, nil
}

// .
// .
// .
// .
func readULEB32(b []byte, off int, what string) (uint32, int, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		if off+i >= len(b) {
			return 0, 0, &ArtifactFormatError{Offset: off, Detail: what + " truncated mid-LEB128"}
		}
		c := b[off+i]
		if i == 4 && c&0xF0 != 0 {
			// .
			// .
			return 0, 0, &ArtifactFormatError{Offset: off, Detail: what + " is not a valid LEB128 u32"}
		}
		v |= uint32(c&0x7F) << (7 * i)
		if c&0x80 == 0 {
			return v, i + 1, nil
		}
	}
	// .
	// .
	// .
	return 0, 0, &ArtifactFormatError{Offset: off, Detail: what + " is not a valid LEB128 u32"}
}
