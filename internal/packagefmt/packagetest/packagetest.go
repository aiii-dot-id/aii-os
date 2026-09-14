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
package packagetest

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

const (
	tarBlockBytes = 512

	tarTypeRegular   = '0'
	tarTypeDirectory = '5'
	tarTypePAXLocal  = 'x'

	tarModeDir     = 0o755
	tarModeRegular = 0o644

	// .
	// .
	paxHeaderPath        = "PaxHeaders/aiiospkg"
	paxMemberPlaceholder = "PaxPayload/aiiospkg"
)

// .

// .
// .
// .
// .
// .
// .
// .
type VariantSpec struct {
	ID, Platform, Arch, Topology, Runtime, Profile string
	Entrypoint                                     string
	Capabilities                                   []string
	RequiresRequired                               []string
	RequiresOptional                               []string
}

// .
// .
// .
type InterfaceSpec struct {
	ID         string
	Version    int
	SchemaFile string
	Methods    []string
}

// .
// .
// .
// .
// .
// .
func BuildManifestJSON(id, version string, ifaces []InterfaceSpec, variants []VariantSpec, installFiles map[string][]byte, extraTop map[string]interface{}) []byte {
	var ilist []map[string]interface{}
	var refs []string
	for _, ifc := range ifaces {
		ilist = append(ilist, map[string]interface{}{
			"id": ifc.ID, "version": ifc.Version,
			"schema_hash": "sha256:" + sha256Hex(installFiles[ifc.SchemaFile]),
			"methods":     ifc.Methods,
		})
		refs = append(refs, fmt.Sprintf("%s@%d", ifc.ID, ifc.Version))
	}
	var vlist []map[string]interface{}
	for _, v := range variants {
		caps := v.Capabilities
		if caps == nil {
			caps = []string{}
		}
		entry := map[string]interface{}{
			"variant_id": v.ID, "platform": v.Platform, "arch": v.Arch,
			"topology": v.Topology, "execution_runtime": v.Runtime,
			"admission_profile": v.Profile, "entrypoint": v.Entrypoint,
			"artifact_hash":        "sha256:" + sha256Hex(installFiles[v.Entrypoint]),
			"implements":           map[string]interface{}{"core": refs},
			"variant_capabilities": caps,
		}
		if v.RequiresRequired != nil || v.RequiresOptional != nil {
			req := map[string]interface{}{}
			if v.RequiresRequired != nil {
				req["required"] = v.RequiresRequired
			}
			if v.RequiresOptional != nil {
				req["optional"] = v.RequiresOptional
			}
			entry["requirements"] = req
		}
		vlist = append(vlist, entry)
	}
	m := map[string]interface{}{
		"kind": "plugin", "id": id, "version": version,
		"package_hash":  ReferencePackageHash(installFiles),
		"plugin_family": "tool_bridge", "bbb_protocol_version": 2,
		"interfaces":          map[string]interface{}{"core": ilist},
		"capability_envelope": []string{},
		"variants":            vlist,
	}
	for k, v := range extraTop {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return raw
}

// .
// .
// .
// .
func ReferencePackageHash(files map[string][]byte) string {
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	agg := sha256.New()
	for _, p := range paths {
		agg.Write([]byte(p))
		agg.Write([]byte{0})
		agg.Write([]byte(sha256Hex(files[p])))
		agg.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(agg.Sum(nil))
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// .
// .
// .
type PackageSpec struct {
	Root         string
	Manifest     []byte
	InstallFiles map[string][]byte
	Signatures   map[string][]byte
	Provenance   map[string][]byte
	README       []byte
}

// .
// .
// .
func Build(spec PackageSpec) []byte {
	return gzipWrap(writeCanonicalTar(members(spec)))
}

// .
type member struct {
	path    string
	isDir   bool
	content []byte
}

// .
// .
func members(spec PackageSpec) []member {
	dirs := map[string]bool{spec.Root: true}
	var files []member
	addFile := func(rel string, content []byte) {
		full := spec.Root + "/" + rel
		for i := len(spec.Root) + 1; i < len(full); i++ {
			if full[i] == '/' {
				dirs[full[:i]] = true
			}
		}
		files = append(files, member{path: full, content: content})
	}
	if spec.Manifest != nil {
		addFile("manifest.json", spec.Manifest)
	}
	dirs[spec.Root+"/install-root"] = true
	for rel, content := range spec.InstallFiles {
		addFile("install-root/"+rel, content)
	}
	if len(spec.Signatures) > 0 {
		dirs[spec.Root+"/signatures"] = true
	}
	for name, content := range spec.Signatures {
		addFile("signatures/"+name, content)
	}
	if len(spec.Provenance) > 0 {
		dirs[spec.Root+"/provenance"] = true
	}
	for name, content := range spec.Provenance {
		addFile("provenance/"+name, content)
	}
	if spec.README != nil {
		addFile("README.md", spec.README)
	}
	all := make([]member, 0, len(dirs)+len(files))
	for d := range dirs {
		all = append(all, member{path: d, isDir: true})
	}
	all = append(all, files...)
	sort.Slice(all, func(i, j int) bool { return all[i].path < all[j].path })
	return all
}

// .

func writeCanonicalTar(ms []member) []byte {
	var buf bytes.Buffer
	for _, m := range ms {
		typ := byte(tarTypeRegular)
		mode := int64(tarModeRegular)
		if m.isDir {
			typ = tarTypeDirectory
			mode = tarModeDir
		}
		stored := m.path
		if m.isDir {
			stored += "/"
		}
		if pathFitsUSTAR(stored) {
			hdr := buildTarHeader(stored, int64(len(m.content)), mode, typ)
			buf.Write(hdr[:])
		} else {
			// .
			record := paxBuildPathRecord(m.path)
			paxHdr := buildTarHeader(paxHeaderPath, int64(len(record)), tarModeRegular, tarTypePAXLocal)
			buf.Write(paxHdr[:])
			buf.WriteString(record)
			writeBlockPadding(&buf, len(record))
			hdr := buildTarHeader(paxMemberPlaceholder, int64(len(m.content)), mode, typ)
			buf.Write(hdr[:])
		}
		if !m.isDir {
			buf.Write(m.content)
			writeBlockPadding(&buf, len(m.content))
		}
	}
	buf.Write(make([]byte, 2*tarBlockBytes))
	return buf.Bytes()
}

func writeBlockPadding(buf *bytes.Buffer, contentLen int) {
	if pad := contentLen % tarBlockBytes; pad != 0 {
		buf.Write(make([]byte, tarBlockBytes-pad))
	}
}

// .
// .
// .
func gzipWrap(tarBytes []byte) []byte {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		panic(err)
	}
	if _, err := zw.Write(tarBytes); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// .
// .
// .
func tarSplitPath(path string) (name, prefix string, err error) {
	if len(path) == 0 || len(path) > 511 {
		return "", "", fmt.Errorf("stored path length %d outside canonical bounds", len(path))
	}
	if len(path) <= 100 {
		return path, "", nil
	}
	for split := len(path) - 1; split > 0; split-- {
		if path[split] != '/' {
			continue
		}
		prefixLen := split
		nameLen := len(path) - split - 1
		if prefixLen <= 155 && nameLen > 0 && nameLen <= 100 {
			return path[split+1:], path[:split], nil
		}
	}
	return "", "", fmt.Errorf("stored path does not fit USTAR name/prefix")
}

func pathFitsUSTAR(stored string) bool {
	_, _, err := tarSplitPath(stored)
	return err == nil
}

// .
// .
func writeOctal(field []byte, value int64) {
	width := len(field) - 1
	s := strconv.FormatInt(value, 8)
	if len(s) > width {
		panic(fmt.Sprintf("octal value needs %d digits, field holds %d", len(s), width))
	}
	for i := 0; i < width-len(s); i++ {
		field[i] = '0'
	}
	copy(field[width-len(s):], s)
	field[width] = 0
}

// .
// .
// .
func buildTarHeader(storedPath string, size, mode int64, typ byte) [tarBlockBytes]byte {
	var h [tarBlockBytes]byte
	name, prefix, err := tarSplitPath(storedPath)
	if err != nil {
		panic(err)
	}
	copy(h[0:100], name)
	copy(h[345:500], prefix)
	writeOctal(h[100:108], mode&0o777)
	writeOctal(h[124:136], size)
	writeOctal(h[108:116], 0)
	writeOctal(h[116:124], 0)
	writeOctal(h[136:148], 0)
	for i := 148; i < 156; i++ {
		h[i] = ' '
	}
	h[156] = typ
	copy(h[257:263], "ustar\x00")
	copy(h[263:265], "00")
	var sum int64
	for _, b := range h {
		sum += int64(b)
	}
	chk := strconv.FormatInt(sum, 8)
	if len(chk) > 6 {
		panic("checksum overflows canonical field")
	}
	for i := 0; i < 6-len(chk); i++ {
		h[148+i] = '0'
	}
	copy(h[148+6-len(chk):154], chk)
	h[154] = 0
	h[155] = ' '
	return h
}

// .
// .
func paxBuildPathRecord(path string) string {
	if len(path) == 0 || len(path) > 511 {
		panic(fmt.Sprintf("PAX path length %d outside canonical bounds", len(path)))
	}
	digits := 1
	total := len(path) + 7 + digits
	for {
		nextDigits := len(strconv.Itoa(total))
		if nextDigits == digits {
			break
		}
		digits = nextDigits
		total = len(path) + 7 + digits
	}
	record := fmt.Sprintf("%d path=%s\n", total, path)
	if len(record) != total {
		panic("PAX record length self-consistency failed")
	}
	return record
}
