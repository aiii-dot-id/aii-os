package packagefmt

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

// .
// .
// .
// .
// .
// .
const (
	tarBlockBytes = 512

	// .
	// .
	// .
	maxComponentBytes  = 255
	maxMemberPathBytes = 511

	// .
	// .
	maxSemanticMembers = 1024

	// .
	// .
	maxNonEndHeaders = maxSemanticMembers * 2

	// .
	// .
	maxRegularPayloadBytes = int64(2147483648)

	// .
	// .
	// .
	maxPAXRecordBytes = 521
	maxPAXPaddedBytes = 1024

	// .
	// .
	// .
	// .
	maxFilePaddingBytes = int64(maxSemanticMembers-1) * (tarBlockBytes - 1)
	maxTarBytes         = maxRegularPayloadBytes +
		int64(maxNonEndHeaders)*tarBlockBytes +
		int64(maxSemanticMembers)*maxPAXPaddedBytes +
		maxFilePaddingBytes +
		2*tarBlockBytes

	// .
	// .
	// .
	// .
	// .
	maxDeflateBytes    = 128 + (maxTarBytes*110)/100
	gzipEnvelopeBytes  = 18
	maxCompressedBytes = maxDeflateBytes + gzipEnvelopeBytes

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	maxJSONMemberBytes = 1 << 20
)

// .
const (
	tarTypeRegular       = '0'
	tarTypeRegularLegacy = 0
	tarTypeDirectory     = '5'
	tarTypePAXLocal      = 'x'

	tarModeDir        = 0o755
	tarModeRegular    = 0o644
	tarModeExecutable = 0o755

	paxHeaderPath        = "PaxHeaders/aiiospkg"
	paxMemberPlaceholder = "PaxPayload/aiiospkg"
)

// .
// .
var gzipHeaderCanonical = [10]byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff}

// .
// .
// .
// .
type tarLimits struct {
	semanticMembers int
	nonEndHeaders   int
	payloadBytes    int64
	tarBytes        int64
	compressedBytes int64
	// .
	// .
	// .
	firstFile string
	// .
	// .
	// .
	runtimeNames bool
}

var bundleLimits = tarLimits{
	semanticMembers: maxSemanticMembers,
	nonEndHeaders:   maxNonEndHeaders,
	payloadBytes:    maxRegularPayloadBytes,
	tarBytes:        maxTarBytes,
	compressedBytes: maxCompressedBytes,
}

// .
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

// .
// .
type gzipStream struct {
	src      *cappedReader
	br       *bufio.Reader
	fr       io.ReadCloser
	crc      uint32
	isize    uint32
	totalOut int64
	limits   tarLimits
}

func newGzipStream(src io.Reader) (*gzipStream, *Error) {
	return newGzipStreamWith(src, bundleLimits)
}

func newGzipStreamWith(src io.Reader, limits tarLimits) (*gzipStream, *Error) {
	capped := &cappedReader{r: src, max: limits.compressedBytes}
	br := bufio.NewReader(capped)
	var hdr [10]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return nil, fail(ReasonEnvelopeMalformed, "gzip", "short gzip header: %v", err)
	}
	if hdr != gzipHeaderCanonical {
		return nil, fail(ReasonEnvelopeMalformed, "gzip", "gzip header is not the canonical FLG=0 MTIME=0 XFL=2 OS=255 form")
	}
	return &gzipStream{src: capped, br: br, fr: flate.NewReader(br), limits: limits}, nil
}

// .
// .
func (g *gzipStream) readExact(buf []byte) *Error {
	if g.totalOut+int64(len(buf)) > g.limits.tarBytes {
		return fail(ReasonCeilingExceeded, "gzip", "decompressed stream exceeds the %d-byte tar ceiling", g.limits.tarBytes)
	}
	if _, err := io.ReadFull(g.fr, buf); err != nil {
		if g.src.over {
			return fail(ReasonCeilingExceeded, "gzip", "compressed input exceeds the %d-byte archive ceiling", g.limits.compressedBytes)
		}
		return fail(ReasonEnvelopeMalformed, "gzip", "truncated or corrupt deflate stream: %v", err)
	}
	g.crc = crc32.Update(g.crc, crc32.IEEETable, buf)
	g.isize += uint32(len(buf))
	g.totalOut += int64(len(buf))
	return nil
}

// .
// .
// .
func (g *gzipStream) finish() *Error {
	var one [1]byte
	if n, err := g.fr.Read(one[:]); n != 0 || err != io.EOF {
		if g.src.over {
			return fail(ReasonCeilingExceeded, "gzip", "compressed input exceeds the %d-byte archive ceiling", g.limits.compressedBytes)
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

// .

// .
func tarFieldString(field []byte) string {
	if i := bytes.IndexByte(field, 0); i >= 0 {
		return string(field[:i])
	}
	return string(field)
}

// .
// .
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

// .
// .
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

// .
// .
// .
func tarSplitPath(path string) (name, prefix string, err error) {
	// .
	// .
	// .
	// .
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

// .
// .
func tarStoredPath(path string, typ byte) string {
	if typ == tarTypeDirectory {
		return path + "/"
	}
	return path
}

// .
// .
func tarPathFitsUSTAR(path string, typ byte) bool {
	_, _, err := tarSplitPath(tarStoredPath(path, typ))
	return err == nil
}

// .
// .
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

// .
// .
// .
// .
// .
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
	if err := writeOctal(h[108:116], 0); err != nil {
		return h, err
	}
	if err := writeOctal(h[116:124], 0); err != nil {
		return h, err
	}
	if err := writeOctal(h[136:148], 0); err != nil {
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

// .

func asciiLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// .
// .
// .
// .
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

// .
// .
func componentForbidden(component string) bool { return componentForbiddenUnder(component, false) }

// .
// .
// .
// .
// .
// .
// .
// .
// .
func componentForbiddenUnder(component string, runtimeNames bool) bool {
	if len(component) == 0 || len(component) > maxComponentBytes {
		return true
	}
	if component == "." || component == ".." {
		return true
	}
	if component[len(component)-1] == '.' || componentMatchesWindowsDeviceStem(component) {
		return true
	}
	if runtimeNames && (component[0] == ' ' || component[len(component)-1] == ' ') {
		return true
	}
	for i := 0; i < len(component); i++ {
		c := component[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '+' || c == '-' {
			continue
		}
		if runtimeNames && (c == ' ' || c == '(' || c == ')') {
			continue
		}
		return true
	}
	return false
}

// .
// .
// .
// .
func normalizeMemberPathUnder(raw string, topLevel *string, runtimeNames bool) (string, error) {
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
		if componentForbiddenUnder(component, runtimeNames) {
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

// .
func pathIsExactAncestor(ancestor, path string) bool {
	return strings.HasPrefix(path, ancestor) && len(path) > len(ancestor) && path[len(ancestor)] == '/'
}

// .
// .
// .
// .
// .
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

// .

// .
// .
func paxBuildPathRecord(path string) (string, error) {
	if len(path) == 0 || len(path) > maxMemberPathBytes {
		return "", fmt.Errorf("PAX path length %d outside canonical bounds", len(path))
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
	if total > maxPAXRecordBytes {
		return "", fmt.Errorf("PAX record length %d exceeds the %d-byte ceiling", total, maxPAXRecordBytes)
	}
	record := fmt.Sprintf("%d path=%s\n", total, path)
	if len(record) != total {
		return "", fmt.Errorf("PAX record length self-consistency failed")
	}
	return record, nil
}

// .
// .
// .
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

// .

// .
type tarMember struct {
	path  string
	size  int64
	mode  int64
	typ   byte
	isDir bool
}

type seenEntry struct {
	path  string
	isDir bool
}

// .
type tarWalker struct {
	gz             *gzipStream
	seen           []seenEntry
	topLevel       string
	previousMember string
	pendingPAXPath string
	nonEndHeaders  int
	payloadBytes   int64
	limits         tarLimits
}

func newTarWalker(gz *gzipStream) *tarWalker {
	return newTarWalkerWith(gz, bundleLimits)
}

func newTarWalkerWith(gz *gzipStream, limits tarLimits) *tarWalker {
	return &tarWalker{gz: gz, limits: limits}
}

// .
// .
// .
// .
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
		if w.nonEndHeaders > w.limits.nonEndHeaders {
			return nil, false, fail(ReasonCeilingExceeded, "tar", "more than %d non-end headers", w.limits.nonEndHeaders)
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
			continue
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
		normalized, err := normalizeMemberPathUnder(w.pendingPAXPath, &w.topLevel, w.limits.runtimeNames)
		if err != nil {
			return nil, fail(ReasonEnvelopeMalformed, "tar", "PAX member path: %v", err)
		}
		if tarPathFitsUSTAR(normalized, typ) {
			return nil, fail(ReasonEnvelopeMalformed, "tar", "PAX used for the USTAR-fit path %q", normalized)
		}
		path, expectedStored = normalized, paxMemberPlaceholder
	} else {
		normalized, err := normalizeMemberPathUnder(rawName, &w.topLevel, w.limits.runtimeNames)
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
	if len(w.seen) >= w.limits.semanticMembers {
		return fail(ReasonCeilingExceeded, "tar", "more than %d semantic members", w.limits.semanticMembers)
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
	// .
	// .
	// .
	// .
	// .
	// .
	leading := w.limits.firstFile != "" && len(w.seen) == 1 && !m.isDir && m.path == w.seen[0].path+"/"+w.limits.firstFile
	if !leading {
		if w.previousMember != "" && w.previousMember >= m.path {
			return fail(ReasonMemberOrder, "tar", "member %q breaks canonical bytewise order after %q", m.path, w.previousMember)
		}
		w.previousMember = m.path
	}
	if !m.isDir {
		// .
		// .
		if !strings.Contains(m.path, "/") {
			return fail(ReasonEnvelopeMalformed, "tar", "the sole top-level member must be the package directory")
		}
		if m.size > w.limits.payloadBytes-w.payloadBytes {
			return fail(ReasonCeilingExceeded, "tar", "regular payload exceeds the inclusive %d-byte sum ceiling", w.limits.payloadBytes)
		}
		w.payloadBytes += m.size
	}
	w.seen = append(w.seen, seenEntry{path: m.path, isDir: m.isDir})
	return nil
}

func (w *tarWalker) parentExists(path string) bool {
	slash := strings.LastIndexByte(path, '/')
	if slash < 0 {
		return len(w.seen) == 0
	}
	parent := path[:slash]
	for _, e := range w.seen {
		if e.isDir && e.path == parent {
			return true
		}
	}
	return false
}

// .
// .
// .
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
