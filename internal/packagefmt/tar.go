package packagefmt

// The streaming-bounded .aiiospkg reader: one raw-DEFLATE gzip member
// wrapping a canonical USTAR stream, consumed strictly in canonical
// member order with every ceiling enforced before bytes are
// materialized (PLUGIN_BUNDLE_FORMAT.md §3.2 "Readers enforce the same
// grammar before extracted bytes can become an admitted package").
//
// This is a faithful port of the C reader
// (opensuperclaw/src/aii-os-plugin-sdk/sdk/package/src/package_bundle.c):
// for every semantic member the reader rebuilds the one canonical
// 512-byte header the encoder could have written for (path, size, mode,
// type) and compares it byte-for-byte — any deviation in uid, gid,
// mtime, owner names, link fields, magic, version, checksum formatting,
// or padding rejects wholesale. archive/tar is deliberately not used
// here: its tolerance for non-canonical input is exactly the divergence
// the one-format discipline forbids (a package Go accepts and C rejects
// is a chain split).

import (
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
)

// Ceilings — mirrored constant-for-constant from the C owner
// (sev_package_bundle.h); none is independently chosen here. WHY each
// exists: a hostile or broken bundle must never make the reader
// materialize unbounded compressed or decompressed data before
// admission (§1 streaming-bounded). On exceed the reader rejects — it
// never truncates and continues.
const (
	tarBlockBytes = 512

	// maxComponentBytes / maxMemberPathBytes: §3.2 — every path
	// component is 1..255 bytes; the complete normalized member path
	// including the root is at most 511 bytes.
	maxComponentBytes  = 255
	maxMemberPathBytes = 511

	// maxSemanticMembers: §3.2 — at most 1,024 semantic members
	// including the root (so at most 1,023 descendants).
	maxSemanticMembers = 1024

	// maxNonEndHeaders: §3.2 — no more than 2,048 non-end headers
	// (each semantic member may carry at most one local PAX header).
	maxNonEndHeaders = maxSemanticMembers * 2

	// maxRegularPayloadBytes: §3.2 — an inclusive 2-GiB sum of all
	// regular-file payload bytes across the whole archive.
	maxRegularPayloadBytes = int64(2147483648)

	// maxPAXRecordBytes / maxPAXPaddedBytes: a canonical PAX path
	// record for a ≤511-byte path is at most 521 bytes ("digits
	// path=…\n"), block-padded to at most 1,024 bytes.
	maxPAXRecordBytes = 521
	maxPAXPaddedBytes = 1024

	// maxTarBytes: the derived decompression ceiling — the largest tar
	// stream the canonical grammar permits: payload cap + all non-end
	// header blocks + all PAX record blocks + per-file block padding +
	// the two end blocks (SEV_PACKAGE_BUNDLE_MAX_TAR_BYTES).
	maxFilePaddingBytes = int64(maxSemanticMembers-1) * (tarBlockBytes - 1)
	maxTarBytes         = maxRegularPayloadBytes +
		int64(maxNonEndHeaders)*tarBlockBytes +
		int64(maxSemanticMembers)*maxPAXPaddedBytes +
		maxFilePaddingBytes +
		2*tarBlockBytes

	// maxCompressedBytes: §3.2 — the compressed-input ceiling is the
	// pinned compressor's conservative bound over the maximum tar
	// stream plus the 18-byte gzip envelope (miniz 3.0.2
	// mz_deflateBound first branch: 128 + 110% of the input), not an
	// independently chosen limit.
	maxDeflateBytes    = 128 + (maxTarBytes*110)/100
	gzipEnvelopeBytes  = 18
	maxCompressedBytes = maxDeflateBytes + gzipEnvelopeBytes

	// maxJSONMemberBytes: per-member ceiling for the bundle members the
	// verifier materializes in memory (manifest.json and the signature
	// envelopes). Mirrors the C stack's MAX_JSON_BYTES (aiiospkg.py);
	// a dual-PQ envelope with an SLH-DSA-SHA2-256s signature and an
	// embedded ML-DSA-87 key tops out well under 128 KiB, so 1 MiB is
	// generous without inviting memory abuse. install-root artifacts
	// are never materialized — they stream through the digest.
	maxJSONMemberBytes = 1 << 20
)

// Canonical member types and PAX member names (package_bundle.c).
const (
	tarTypeRegular       = '0'
	tarTypeRegularLegacy = 0 // '\0' legacy regular — explicitly rejected
	tarTypeDirectory     = '5'
	tarTypePAXLocal      = 'x'

	tarModeDir        = 0o755
	tarModeRegular    = 0o644
	tarModeExecutable = 0o755

	paxHeaderPath        = "PaxHeaders/aiiospkg"
	paxMemberPlaceholder = "PaxPayload/aiiospkg"
)

// gzipHeaderCanonical: §3.2 — FLG=0, MTIME=0, XFL=2, OS=255 over
// raw-DEFLATE method 8. Anything else is not a canonical bundle.
var gzipHeaderCanonical = [10]byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff}

// cappedReader counts compressed bytes and rejects past the ceiling.
type cappedReader struct {
	r    io.Reader
	n    int64
	max  int64
	over bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.n >= c.max {
		c.over = true
		return 0, fmt.Errorf("compressed input exceeds the %d-byte archive ceiling", c.max)
	}
	if int64(len(p)) > c.max-c.n {
		p = p[:c.max-c.n]
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// gzipStream is the strict single-member gzip layer: canonical 10-byte
// header, raw DEFLATE, CRC32/ISIZE trailer, immediate EOF both sides.
type gzipStream struct {
	src      *cappedReader
	br       *bufio.Reader // over src; flate reads bytewise near stream end, so it never overshoots into the trailer
	fr       io.ReadCloser
	crc      uint32
	isize    uint32 // ISIZE arithmetic is mod 2^32 by format definition
	totalOut int64
}

func newGzipStream(src io.Reader) (*gzipStream, *Error) {
	capped := &cappedReader{r: src, max: maxCompressedBytes}
	br := bufio.NewReader(capped)
	var hdr [10]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return nil, fail(ReasonEnvelopeMalformed, "gzip", "short gzip header: %v", err)
	}
	if hdr != gzipHeaderCanonical {
		return nil, fail(ReasonEnvelopeMalformed, "gzip", "gzip header is not the canonical FLG=0 MTIME=0 XFL=2 OS=255 form")
	}
	return &gzipStream{src: capped, br: br, fr: flate.NewReader(br)}, nil
}

// readExact fills buf from the decompressed stream, accounting CRC,
// ISIZE, and the decompression ceiling.
func (g *gzipStream) readExact(buf []byte) *Error {
	if g.totalOut+int64(len(buf)) > maxTarBytes {
		return fail(ReasonCeilingExceeded, "gzip", "decompressed stream exceeds the %d-byte tar ceiling", int64(maxTarBytes))
	}
	if _, err := io.ReadFull(g.fr, buf); err != nil {
		if g.src.over {
			return fail(ReasonCeilingExceeded, "gzip", "compressed input exceeds the %d-byte archive ceiling", int64(maxCompressedBytes))
		}
		return fail(ReasonEnvelopeMalformed, "gzip", "truncated or corrupt deflate stream: %v", err)
	}
	g.crc = crc32.Update(g.crc, crc32.IEEETable, buf)
	g.isize += uint32(len(buf))
	g.totalOut += int64(len(buf))
	return nil
}

// finish requires immediate decompressed EOF, a valid CRC32/ISIZE
// trailer, and immediate raw EOF after it (one gzip member, nothing
// smuggled after the end blocks on either layer).
func (g *gzipStream) finish() *Error {
	var one [1]byte
	if n, err := g.fr.Read(one[:]); n != 0 || err != io.EOF {
		if g.src.over {
			return fail(ReasonCeilingExceeded, "gzip", "compressed input exceeds the %d-byte archive ceiling", int64(maxCompressedBytes))
		}
		return fail(ReasonEnvelopeMalformed, "gzip", "decompressed data continues past the tar end blocks")
	}
	var trailer [8]byte
	if _, err := io.ReadFull(g.br, trailer[:]); err != nil {
		return fail(ReasonEnvelopeMalformed, "gzip", "missing gzip trailer: %v", err)
	}
	if binary.LittleEndian.Uint32(trailer[0:4]) != g.crc {
		return fail(ReasonEnvelopeMalformed, "gzip", "gzip CRC32 mismatch")
	}
	if binary.LittleEndian.Uint32(trailer[4:8]) != g.isize {
		return fail(ReasonEnvelopeMalformed, "gzip", "gzip ISIZE mismatch")
	}
	if _, err := g.br.ReadByte(); err != io.EOF {
		return fail(ReasonEnvelopeMalformed, "gzip", "trailing bytes after the gzip member")
	}
	return nil
}

// --- Canonical tar primitives (ports of package_bundle.c) ---

// tarFieldString trims a fixed header field at its first NUL.
func tarFieldString(field []byte) string {
	if i := bytes.IndexByte(field, 0); i >= 0 {
		return string(field[:i])
	}
	return string(field)
}

// tarParseOctal ports tar_parse_octal: high bit clear, NUL/space
// skipped anywhere, digits 0-7 only, at least one digit.
func tarParseOctal(field []byte) (int64, error) {
	if len(field) == 0 {
		return 0, fmt.Errorf("empty octal field")
	}
	if field[0]&0x80 != 0 {
		return 0, fmt.Errorf("base-256 tar numeric field is not canonical")
	}
	var value int64
	sawDigit := false
	for _, c := range field {
		if c == 0 || c == ' ' {
			continue
		}
		if c < '0' || c > '7' {
			return 0, fmt.Errorf("non-octal byte %q in numeric field", c)
		}
		if value > (1<<62)/8 {
			return 0, fmt.Errorf("octal field overflows")
		}
		value = value*8 + int64(c-'0')
		sawDigit = true
	}
	if !sawDigit {
		return 0, fmt.Errorf("octal field has no digits")
	}
	return value, nil
}

// tarVerifyChecksum ports tar_verify_checksum: sum all header bytes
// with the checksum field counted as spaces.
func tarVerifyChecksum(block *[tarBlockBytes]byte) error {
	expected, err := tarParseOctal(block[148:156])
	if err != nil {
		return fmt.Errorf("header checksum unreadable: %v", err)
	}
	var actual int64
	for i, b := range block {
		if i >= 148 && i < 156 {
			actual += int64(' ')
		} else {
			actual += int64(b)
		}
	}
	if actual != expected {
		return fmt.Errorf("header checksum mismatch")
	}
	return nil
}

// tarSplitPath ports tar_split_path: a stored path of ≤100 bytes lives
// wholly in name; longer paths split at the rightmost '/' where the
// prefix fits 155 bytes and the name fits 100.
func tarSplitPath(path string) (name, prefix string, err error) {
	// The 511-byte bound applies to the STORED path (directories carry
	// their trailing slash here) — so a 511-byte normalized directory
	// path does not fit USTAR and must travel via PAX, exactly as the
	// C reader decides it.
	if len(path) == 0 || len(path) > maxMemberPathBytes {
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

// tarStoredPath ports tar_stored_path_for_type: directories are stored
// with a trailing slash.
func tarStoredPath(path string, typ byte) string {
	if typ == tarTypeDirectory {
		return path + "/"
	}
	return path
}

// tarPathFitsUSTAR ports tar_path_fits_ustar for the PAX-necessity rule:
// PAX used for a USTAR-fit path rejects.
func tarPathFitsUSTAR(path string, typ byte) bool {
	_, _, err := tarSplitPath(tarStoredPath(path, typ))
	return err == nil
}

// writeOctal mirrors tar_write_octal: zero-padded octal of width
// len(field)-1 followed by a NUL.
func writeOctal(field []byte, value int64) error {
	width := len(field) - 1
	s := strconv.FormatInt(value, 8)
	if len(s) > width {
		return fmt.Errorf("octal value needs %d digits, field holds %d", len(s), width)
	}
	for i := 0; i < width-len(s); i++ {
		field[i] = '0'
	}
	copy(field[width-len(s):], s)
	field[width] = 0
	return nil
}

// buildTarHeader ports tar_build_header: THE canonical 512-byte header
// for (storedPath, size, mode, type). The reader compares every arriving
// header against this reconstruction, so any non-canonical field —
// uid, gid, mtime, uname, gname, linkname, dev numbers, magic, version,
// checksum formatting, stray padding — rejects without per-field code.
func buildTarHeader(storedPath string, size int64, mode int64, typ byte) ([tarBlockBytes]byte, error) {
	var h [tarBlockBytes]byte
	name, prefix, err := tarSplitPath(storedPath)
	if err != nil {
		return h, err
	}
	copy(h[0:100], name)
	copy(h[345:500], prefix)
	if err := writeOctal(h[100:108], mode&0o777); err != nil {
		return h, err
	}
	if err := writeOctal(h[124:136], size); err != nil {
		return h, err
	}
	if err := writeOctal(h[108:116], 0); err != nil { // uid = 0
		return h, err
	}
	if err := writeOctal(h[116:124], 0); err != nil { // gid = 0
		return h, err
	}
	if err := writeOctal(h[136:148], 0); err != nil { // mtime = 0
		return h, err
	}
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
		return h, fmt.Errorf("checksum overflows canonical field")
	}
	for i := 0; i < 6-len(chk); i++ {
		h[148+i] = '0'
	}
	copy(h[148+6-len(chk):154], chk)
	h[154] = 0
	h[155] = ' '
	return h, nil
}

// --- Canonical path grammar (ports of package_bundle.c) ---

func asciiLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// componentMatchesWindowsDeviceStem ports the C check: the stem (up to
// the first '.') must not be con/prn/aux/nul or com1-9/lpt1-9 in any
// case — a member that Windows cannot create as a plain file breaks the
// five-platform reproducibility contract.
func componentMatchesWindowsDeviceStem(component string) bool {
	stem := component
	if i := strings.IndexByte(component, '.'); i >= 0 {
		stem = component[:i]
	}
	lower := make([]byte, len(stem))
	for i := 0; i < len(stem); i++ {
		lower[i] = asciiLower(stem[i])
	}
	switch string(lower) {
	case "con", "prn", "aux", "nul":
		return true
	}
	if len(lower) == 4 && (string(lower[:3]) == "com" || string(lower[:3]) == "lpt") &&
		lower[3] >= '1' && lower[3] <= '9' {
		return true
	}
	return false
}

// componentForbidden ports component_is_forbidden.
func componentForbidden(component string) bool {
	if len(component) == 0 || len(component) > maxComponentBytes {
		return true
	}
	if component == "." || component == ".." {
		return true
	}
	if component[len(component)-1] == '.' || componentMatchesWindowsDeviceStem(component) {
		return true
	}
	for i := 0; i < len(component); i++ {
		c := component[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '+' || c == '-') {
			return true
		}
	}
	return false
}

// normalizeMemberPath ports normalize_member_path: trims trailing
// slashes, validates every component, and binds/enforces the sole
// top-level directory.
func normalizeMemberPath(raw string, topLevel *string) (string, error) {
	if len(raw) == 0 || len(raw) > maxMemberPathBytes {
		return "", fmt.Errorf("member path length %d outside canonical bounds", len(raw))
	}
	if raw[0] == '/' || raw[0] == '\\' {
		return "", fmt.Errorf("member path is absolute or backslashed")
	}
	normalized := strings.TrimRight(raw, "/")
	if normalized == "" {
		return "", fmt.Errorf("member path is empty after normalization")
	}
	first := true
	for _, component := range strings.Split(normalized, "/") {
		if componentForbidden(component) {
			return "", fmt.Errorf("forbidden path component %q", component)
		}
		if first {
			if *topLevel == "" {
				*topLevel = component
			} else if *topLevel != component {
				return "", fmt.Errorf("member escapes the sole top-level directory %q", *topLevel)
			}
			first = false
		}
	}
	return normalized, nil
}

// pathIsExactAncestor ports path_is_exact_ancestor.
func pathIsExactAncestor(ancestor, path string) bool {
	return strings.HasPrefix(path, ancestor) && len(path) > len(ancestor) && path[len(ancestor)] == '/'
}

// pathsCasefoldSiblingCollision ports paths_have_casefold_sibling_collision:
// walking component pairs, an exactly-equal component descends; a
// casefold-equal but byte-different component is a sibling collision
// (case-insensitive filesystems would merge what the archive keeps
// distinct); any other difference clears.
func pathsCasefoldSiblingCollision(left, right string) bool {
	for {
		li := strings.IndexByte(left, '/')
		ri := strings.IndexByte(right, '/')
		lc, rc := left, right
		if li >= 0 {
			lc = left[:li]
		}
		if ri >= 0 {
			rc = right[:ri]
		}
		if len(lc) != len(rc) {
			return false
		}
		exact := true
		for i := 0; i < len(lc); i++ {
			if asciiLower(lc[i]) != asciiLower(rc[i]) {
				return false
			}
			if lc[i] != rc[i] {
				exact = false
			}
		}
		if !exact {
			return true
		}
		if li < 0 || ri < 0 {
			return false
		}
		left, right = left[li+1:], right[ri+1:]
	}
}

// --- PAX path record (ports of package_bundle.c) ---

// paxBuildPathRecord ports pax_build_path_record: "<total> path=<path>\n"
// where <total> is the self-consistent decimal record length.
func paxBuildPathRecord(path string) (string, error) {
	if len(path) == 0 || len(path) > maxMemberPathBytes {
		return "", fmt.Errorf("PAX path length %d outside canonical bounds", len(path))
	}
	digits := 1
	total := len(path) + 7 + digits // 7 = len(" path=") + len("\n")
	for {
		nextDigits := len(strconv.Itoa(total))
		if nextDigits == digits {
			break
		}
		digits = nextDigits
		total = len(path) + 7 + digits
	}
	if total > maxPAXRecordBytes {
		return "", fmt.Errorf("PAX record length %d exceeds the %d-byte ceiling", total, maxPAXRecordBytes)
	}
	record := fmt.Sprintf("%d path=%s\n", total, path)
	if len(record) != total {
		return "", fmt.Errorf("PAX record length self-consistency failed")
	}
	return record, nil
}

// paxParsePathRecord ports pax_parse_path_record: strict decimal length
// (no leading zero), exactly one " path=" record, trailing newline, and
// a byte-identical canonical re-encoding.
func paxParsePathRecord(record []byte) (string, error) {
	space := 0
	declared := 0
	for space < len(record) && record[space] != ' ' {
		b := record[space]
		if b < '0' || b > '9' || (space == 0 && b == '0') {
			return "", fmt.Errorf("PAX record length is not canonical decimal")
		}
		if declared > (1<<31)/10 {
			return "", fmt.Errorf("PAX record length overflows")
		}
		declared = declared*10 + int(b-'0')
		space++
	}
	const prefix = " path="
	if space == 0 || space+len(prefix)+1 > len(record) ||
		declared != len(record) ||
		string(record[space:space+len(prefix)]) != prefix ||
		record[len(record)-1] != '\n' {
		return "", fmt.Errorf("PAX record is not a sole canonical path record")
	}
	value := record[space+len(prefix) : len(record)-1]
	if len(value) == 0 || len(value) > maxMemberPathBytes {
		return "", fmt.Errorf("PAX path length outside canonical bounds")
	}
	expected, err := paxBuildPathRecord(string(value))
	if err != nil || expected != string(record) {
		return "", fmt.Errorf("PAX record does not round-trip to its canonical encoding")
	}
	return string(value), nil
}

// --- The member walk ---

// tarMember is one admitted semantic member.
type tarMember struct {
	path  string // normalized (no trailing slash)
	size  int64
	mode  int64
	typ   byte
	isDir bool
}

type seenEntry struct {
	path  string
	isDir bool
}

// tarWalker enforces the canonical archive grammar member by member.
type tarWalker struct {
	gz             *gzipStream
	seen           []seenEntry
	topLevel       string
	previousMember string
	pendingPAXPath string
	nonEndHeaders  int
	payloadBytes   int64
}

func newTarWalker(gz *gzipStream) *tarWalker {
	return &tarWalker{gz: gz}
}

// next returns the next admitted member, or done=true after the two end
// blocks. The member's payload has NOT been consumed yet — the caller
// must read exactly member.size bytes via readPayload before calling
// next again.
func (w *tarWalker) next() (member *tarMember, done bool, verr *Error) {
	for {
		var block [tarBlockBytes]byte
		if err := w.gz.readExact(block[:]); err != nil {
			return nil, false, err
		}

		if isZeroBlock(&block) {
			if w.pendingPAXPath != "" {
				return nil, false, fail(ReasonEnvelopeMalformed, "tar", "PAX header with no following member")
			}
			var second [tarBlockBytes]byte
			if err := w.gz.readExact(second[:]); err != nil {
				return nil, false, err
			}
			if !isZeroBlock(&second) {
				return nil, false, fail(ReasonEnvelopeMalformed, "tar", "single zero block followed by data is not a canonical end")
			}
			if w.topLevel == "" {
				return nil, false, fail(ReasonEnvelopeMalformed, "tar", "empty archive")
			}
			return nil, true, nil
		}

		if err := tarVerifyChecksum(&block); err != nil {
			return nil, false, fail(ReasonEnvelopeMalformed, "tar", "%v", err)
		}
		w.nonEndHeaders++
		if w.nonEndHeaders > maxNonEndHeaders {
			return nil, false, fail(ReasonCeilingExceeded, "tar", "more than %d non-end headers", maxNonEndHeaders)
		}

		rawName := tarFieldString(block[0:100])
		if prefix := tarFieldString(block[345:500]); prefix != "" {
			rawName = prefix + "/" + rawName
		}
		if rawName == "" {
			return nil, false, fail(ReasonEnvelopeMalformed, "tar", "member has no name")
		}
		size, err := tarParseOctal(block[124:136])
		if err != nil {
			return nil, false, fail(ReasonEnvelopeMalformed, "tar", "member size: %v", err)
		}
		mode, err := tarParseOctal(block[100:108])
		if err != nil {
			return nil, false, fail(ReasonEnvelopeMalformed, "tar", "member mode: %v", err)
		}
		typ := block[156]

		if typ == tarTypePAXLocal {
			if verr := w.consumePAXHeader(&block, rawName, size); verr != nil {
				return nil, false, verr
			}
			continue // the PAX header is not a semantic member
		}

		if !tarModeIsCanonical(typ, mode) || typ == tarTypeRegularLegacy {
			return nil, false, fail(ReasonEnvelopeMalformed, "tar", "member %q has non-canonical type %q or mode %04o (links, sparse, specials, and stray modes reject)", rawName, typ, mode)
		}
		if typ == tarTypeDirectory && size != 0 {
			return nil, false, fail(ReasonEnvelopeMalformed, "tar", "directory %q has nonzero size", rawName)
		}

		m, verr := w.resolveMember(&block, rawName, size, mode, typ)
		if verr != nil {
			return nil, false, verr
		}
		if verr := w.admitMember(m); verr != nil {
			return nil, false, verr
		}
		return m, false, nil
	}
}

func (w *tarWalker) consumePAXHeader(block *[tarBlockBytes]byte, rawName string, size int64) *Error {
	if w.pendingPAXPath != "" || rawName != paxHeaderPath {
		return fail(ReasonEnvelopeMalformed, "tar", "PAX header %q is not the sole canonical %s record", rawName, paxHeaderPath)
	}
	expected, err := buildTarHeader(paxHeaderPath, size, tarModeRegular, tarTypePAXLocal)
	if err != nil || expected != *block {
		return fail(ReasonEnvelopeMalformed, "tar", "PAX header block is not canonical")
	}
	if size <= 0 {
		return fail(ReasonEnvelopeMalformed, "tar", "PAX header has no record")
	}
	padded := ((size + tarBlockBytes - 1) / tarBlockBytes) * tarBlockBytes
	if padded > maxPAXPaddedBytes {
		return fail(ReasonCeilingExceeded, "tar", "PAX record exceeds the %d-byte padded ceiling", maxPAXPaddedBytes)
	}
	buf := make([]byte, padded)
	if verr := w.gz.readExact(buf); verr != nil {
		return verr
	}
	for _, b := range buf[size:] {
		if b != 0 {
			return fail(ReasonEnvelopeMalformed, "tar", "PAX record padding is not zero")
		}
	}
	path, err := paxParsePathRecord(buf[:size])
	if err != nil {
		return fail(ReasonEnvelopeMalformed, "tar", "%v", err)
	}
	w.pendingPAXPath = path
	return nil
}

func (w *tarWalker) resolveMember(block *[tarBlockBytes]byte, rawName string, size, mode int64, typ byte) (*tarMember, *Error) {
	var path, expectedStored string
	if w.pendingPAXPath != "" {
		if rawName != paxMemberPlaceholder {
			return nil, fail(ReasonEnvelopeMalformed, "tar", "member after a PAX header must be stored as %s", paxMemberPlaceholder)
		}
		normalized, err := normalizeMemberPath(w.pendingPAXPath, &w.topLevel)
		if err != nil {
			return nil, fail(ReasonEnvelopeMalformed, "tar", "PAX member path: %v", err)
		}
		if tarPathFitsUSTAR(normalized, typ) {
			return nil, fail(ReasonEnvelopeMalformed, "tar", "PAX used for the USTAR-fit path %q", normalized)
		}
		path, expectedStored = normalized, paxMemberPlaceholder
	} else {
		normalized, err := normalizeMemberPath(rawName, &w.topLevel)
		if err != nil {
			return nil, fail(ReasonEnvelopeMalformed, "tar", "member path: %v", err)
		}
		path, expectedStored = normalized, tarStoredPath(normalized, typ)
		if rawName != expectedStored {
			return nil, fail(ReasonEnvelopeMalformed, "tar", "member %q is not stored in its canonical form %q", rawName, expectedStored)
		}
	}

	expected, err := buildTarHeader(expectedStored, size, mode, typ)
	if err != nil || expected != *block {
		return nil, fail(ReasonEnvelopeMalformed, "tar", "member %q header deviates from the canonical encoding", path)
	}
	w.pendingPAXPath = ""
	return &tarMember{path: path, size: size, mode: mode, typ: typ, isDir: typ == tarTypeDirectory}, nil
}

func (w *tarWalker) admitMember(m *tarMember) *Error {
	if len(w.seen) >= maxSemanticMembers {
		return fail(ReasonCeilingExceeded, "tar", "more than %d semantic members", maxSemanticMembers)
	}
	for _, e := range w.seen {
		if e.path == m.path || pathsCasefoldSiblingCollision(e.path, m.path) {
			return fail(ReasonMemberOrder, "tar", "member %q duplicates or casefold-collides with %q", m.path, e.path)
		}
		if (!e.isDir && pathIsExactAncestor(e.path, m.path)) ||
			(!m.isDir && pathIsExactAncestor(m.path, e.path)) {
			return fail(ReasonEnvelopeMalformed, "tar", "member %q conflicts with a file ancestor", m.path)
		}
	}
	if !w.parentExists(m.path) {
		return fail(ReasonEnvelopeMalformed, "tar", "member %q has no preceding parent directory", m.path)
	}
	if w.previousMember != "" && w.previousMember >= m.path {
		return fail(ReasonMemberOrder, "tar", "member %q breaks canonical bytewise order after %q", m.path, w.previousMember)
	}
	w.previousMember = m.path
	if !m.isDir {
		// A regular member at top level would be a rootless file; the
		// C reader rejects it in extraction — reject at admission here.
		if !strings.Contains(m.path, "/") {
			return fail(ReasonEnvelopeMalformed, "tar", "the sole top-level member must be the package directory")
		}
		if m.size > maxRegularPayloadBytes-w.payloadBytes {
			return fail(ReasonCeilingExceeded, "tar", "regular payload exceeds the inclusive %d-byte sum ceiling", maxRegularPayloadBytes)
		}
		w.payloadBytes += m.size
	}
	w.seen = append(w.seen, seenEntry{path: m.path, isDir: m.isDir})
	return nil
}

func (w *tarWalker) parentExists(path string) bool {
	slash := strings.LastIndexByte(path, '/')
	if slash < 0 {
		return len(w.seen) == 0 // only the root may sit at top level
	}
	parent := path[:slash]
	for _, e := range w.seen {
		if e.isDir && e.path == parent {
			return true
		}
	}
	return false
}

// readPayload streams a member's exact payload plus its block padding,
// requiring zero padding bytes, handing content to sink (which may be
// nil to discard).
func (w *tarWalker) readPayload(m *tarMember, sink io.Writer) *Error {
	remaining := m.size
	var buf [8192]byte
	for remaining > 0 {
		n := int64(len(buf))
		if remaining < n {
			n = remaining
		}
		if err := w.gz.readExact(buf[:n]); err != nil {
			return err
		}
		if sink != nil {
			if _, err := sink.Write(buf[:n]); err != nil {
				return fail(ReasonEnvelopeMalformed, "tar", "consuming member %q: %v", m.path, err)
			}
		}
		remaining -= n
	}
	if pad := m.size % tarBlockBytes; pad != 0 {
		padding := make([]byte, tarBlockBytes-pad)
		if err := w.gz.readExact(padding); err != nil {
			return err
		}
		for _, b := range padding {
			if b != 0 {
				return fail(ReasonEnvelopeMalformed, "tar", "member %q payload padding is not zero", m.path)
			}
		}
	}
	return nil
}

func tarModeIsCanonical(typ byte, mode int64) bool {
	if mode&^int64(0o777) != 0 {
		return false
	}
	switch typ {
	case tarTypeDirectory:
		return mode == tarModeDir
	case tarTypeRegular, tarTypeRegularLegacy:
		return mode == tarModeRegular || mode == tarModeExecutable
	}
	return false
}

func isZeroBlock(block *[tarBlockBytes]byte) bool {
	for _, b := range block {
		if b != 0 {
			return false
		}
	}
	return true
}
