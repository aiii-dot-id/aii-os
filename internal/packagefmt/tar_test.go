package packagefmt

// Grammar-level tests: the canonical writer used by every fixture is
// pinned against golden vectors computed by an INDEPENDENT
// implementation of the C recipe (python, from
// sev package_bundle.c tar_build_header) — so the reader and the test
// writer cannot drift together.

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// --- golden vectors (generated independently from the C recipe) ---

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestGoldenCanonicalHeaders(t *testing.T) {
	cases := []struct {
		stored string
		size   int64
		mode   int64
		typ    byte
		want   string
	}{
		{"org.example.echo-0.1.0/manifest.json", 123, 0o644, tarTypeRegular,
			"fd8b38f2241d14069d7cd61ea4f54691b49cccbd45f528fada463ba622e2d4e1"},
		{"org.example.echo-0.1.0/install-root/", 0, 0o755, tarTypeDirectory,
			"7be8f03fc8bf6f93cfe93b0ca3affdf972f07c81d05ecef7bacc11e8067c9f37"},
		{paxHeaderPath, 42, 0o644, tarTypePAXLocal,
			"2b396b3861cb4f4eab32fc1774bbab1ddbc4e251038ae5547906fd63857ae0f1"},
	}
	for _, c := range cases {
		hdr, err := buildTarHeader(c.stored, c.size, c.mode, c.typ)
		if err != nil {
			t.Fatalf("buildTarHeader(%q): %v", c.stored, err)
		}
		if got := sha256Hex(hdr[:]); got != c.want {
			t.Errorf("header for %q diverges from the C recipe golden vector: got %s want %s", c.stored, got, c.want)
		}
	}
}

func TestGoldenPAXRecord(t *testing.T) {
	record, err := paxBuildPathRecord(strings.Repeat("x", 511))
	if err != nil {
		t.Fatal(err)
	}
	if len(record) != 521 {
		t.Fatalf("canonical 511-byte path record must be 521 bytes (the ceiling), got %d", len(record))
	}
	if got := sha256Hex([]byte(record)); got != "a7bd45cc58d19a6a81ad0642399240cd9e84e29586789b2a29fa796207baefd7" {
		t.Errorf("PAX record diverges from golden vector: %s", got)
	}
	back, err := paxParsePathRecord([]byte(record))
	if err != nil || back != strings.Repeat("x", 511) {
		t.Errorf("PAX record does not round-trip: %v", err)
	}
}

func TestGoldenPackageDigest(t *testing.T) {
	d := newPackageDigest()
	files := map[string][]byte{
		"interfaces/channel.control.v1.schema.json": []byte("{\"iface\":1}\n"),
		"variants/linux-x86_64-wasm/plugin.wasm":    []byte("\x00asm\x01\x00\x00\x00hello"),
		"variants/linux-x86_64-wasm/variant.json":   []byte("{}\n"),
	}
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		d.addFile(p, sha256.Sum256(files[p]))
	}
	// Pinned against the independent python implementation of
	// aiiospkg.py package_digest (path + "\0" + lowercase-hex + "\n").
	if got := d.sum(); got != "sha256:be7ffab21c271f8a7725eb24578db1046930df7bad264a93aa4ff3a613d1618b" {
		t.Errorf("package digest diverges from reference vector: %s", got)
	}
}

func TestManifestHashOmitsPackageHash(t *testing.T) {
	// PACKAGE_DIGEST §3.5 by literal example: the manifest-hash view is
	// the canonical object with top-level package_hash omitted.
	raw := []byte(`{"z":2,"package_hash":"sha256:0000000000000000000000000000000000000000000000000000000000000000","a":1}`)
	got, verr := manifestHash(raw)
	if verr != nil {
		t.Fatal(verr)
	}
	want := "sha256:" + sha256Hex([]byte(`{"a":1,"z":2}`))
	if got != want {
		t.Errorf("manifest hash view wrong: got %s want %s", got, want)
	}
}

func TestEmbeddedContractParses(t *testing.T) {
	if !contract.Invariants.T2RequiresValidT1 || !contract.Invariants.T3RequiresPlatformReleaseSig {
		t.Fatal("embedded contract lost its core invariants")
	}
	if contract.Invariants.T3RequiresPublisherSig {
		t.Fatal("contract must not require a publisher signature for T3")
	}
	if got := reasonNativeTierIneligible(); got != Reason("NATIVE_TRUST_TIER_INELIGIBLE") {
		t.Fatalf("contract denied-reason drifted: %q", got)
	}
}

// --- the canonical test writer ---

type memberSpec struct {
	path    string // normalized (no trailing slash)
	isDir   bool
	mode    int64 // 0 → canonical default for the type
	content []byte
	// rawHeader, when set, replaces the canonical header wholesale —
	// the hostile-crafting hook.
	rawHeader *[tarBlockBytes]byte
}

func specMode(s memberSpec) int64 {
	if s.mode != 0 {
		return s.mode
	}
	if s.isDir {
		return tarModeDir
	}
	return tarModeRegular
}

// writeCanonicalTar writes members in the GIVEN order (happy-path
// callers sort; hostile callers deliberately do not) and appends the
// two end blocks.
func writeCanonicalTar(t *testing.T, specs []memberSpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, s := range specs {
		typ := byte(tarTypeRegular)
		if s.isDir {
			typ = tarTypeDirectory
		}
		stored := tarStoredPath(s.path, typ)
		if s.rawHeader != nil {
			buf.Write(s.rawHeader[:])
		} else if tarPathFitsUSTAR(s.path, typ) {
			hdr, err := buildTarHeader(stored, int64(len(s.content)), specMode(s), typ)
			if err != nil {
				t.Fatalf("header %q: %v", s.path, err)
			}
			buf.Write(hdr[:])
		} else {
			record, err := paxBuildPathRecord(s.path)
			if err != nil {
				t.Fatalf("pax record %q: %v", s.path, err)
			}
			paxHdr, err := buildTarHeader(paxHeaderPath, int64(len(record)), tarModeRegular, tarTypePAXLocal)
			if err != nil {
				t.Fatalf("pax header: %v", err)
			}
			buf.Write(paxHdr[:])
			buf.WriteString(record)
			writeBlockPadding(&buf, len(record))
			hdr, err := buildTarHeader(paxMemberPlaceholder, int64(len(s.content)), specMode(s), typ)
			if err != nil {
				t.Fatalf("pax member header %q: %v", s.path, err)
			}
			buf.Write(hdr[:])
		}
		if !s.isDir {
			buf.Write(s.content)
			writeBlockPadding(&buf, len(s.content))
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

// gzipWrap produces the canonical outer stream: Go's gzip writer at
// BestCompression emits exactly FLG=0 MTIME=0 XFL=2 OS=255.
func gzipWrap(t *testing.T, tarBytes []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(tarBytes); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	if [10]byte(out[:10]) != gzipHeaderCanonical {
		t.Fatalf("test gzip writer stopped producing the canonical header: % x", out[:10])
	}
	return out
}

func sortSpecs(specs []memberSpec) []memberSpec {
	sorted := append([]memberSpec{}, specs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })
	return sorted
}

// expectReason asserts a rejection with the exact reason code.
func expectReason(t *testing.T, err error, want Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("verification must reject with %s, got success", want)
	}
	var verr *Error
	if !errors.As(err, &verr) {
		t.Fatalf("error is not a typed packagefmt.Error: %v", err)
	}
	if verr.Reason != want {
		t.Fatalf("wrong reason: got %s (%v), want %s", verr.Reason, verr, want)
	}
}

// --- minimal unsigned bundle plumbing for grammar tests ---

// grammarPkg builds a minimal structurally-useful member list: callers
// mutate it. It is NOT a schema-valid package (grammar tests reject
// before the manifest matters); specs are pre-sorted.
func grammarPkg() []memberSpec {
	root := "org.example.echo-0.1.0"
	return sortSpecs([]memberSpec{
		{path: root, isDir: true},
		{path: root + "/manifest.json", content: []byte("{}")},
		{path: root + "/install-root", isDir: true},
		{path: root + "/install-root/payload.bin", content: []byte("bytes")},
	})
}

func verifyBytes(t *testing.T, pkg []byte) error {
	t.Helper()
	_, err := Verify(bytes.NewReader(pkg), TrustRoots{})
	return err
}

// --- gzip envelope grammar ---

func TestVerifyRejectsNonCanonicalGzipHeader(t *testing.T) {
	pkg := gzipWrap(t, writeCanonicalTar(t, grammarPkg()))
	for _, mutate := range []struct {
		name string
		at   int
		to   byte
	}{
		{"XFL", 8, 0x00}, {"OS", 9, 0x03}, {"FLG-FNAME", 3, 0x08}, {"MTIME", 4, 0x01},
	} {
		bad := append([]byte{}, pkg...)
		bad[mutate.at] = mutate.to
		expectReason(t, verifyBytes(t, bad), ReasonEnvelopeMalformed)
	}
}

func TestVerifyRejectsTrailingGarbageAfterGzip(t *testing.T) {
	pkg := gzipWrap(t, writeCanonicalTar(t, grammarPkg()))
	expectReason(t, verifyBytes(t, append(pkg, 0x00)), ReasonEnvelopeMalformed)
}

func TestVerifyRejectsCorruptGzipTrailer(t *testing.T) {
	pkg := gzipWrap(t, writeCanonicalTar(t, grammarPkg()))
	bad := append([]byte{}, pkg...)
	bad[len(bad)-6] ^= 0xff // inside CRC32/ISIZE
	expectReason(t, verifyBytes(t, bad), ReasonEnvelopeMalformed)
}

func TestVerifyRejectsTruncatedArchive(t *testing.T) {
	pkg := gzipWrap(t, writeCanonicalTar(t, grammarPkg()))
	for _, cut := range []int{5, len(pkg) / 2, len(pkg) - 3} {
		expectReason(t, verifyBytes(t, pkg[:cut]), ReasonEnvelopeMalformed)
	}
}

// The decompression-bomb shape: a tiny compressed input hiding a huge
// decompressed stream. The grammar never inflates it — the walker stops
// at the end blocks and finish() rejects the first smuggled byte.
func TestVerifyRejectsDecompressedBytesAfterEndBlocks(t *testing.T) {
	tarBytes := writeCanonicalTar(t, grammarPkg())
	bomb := append(tarBytes, make([]byte, 4<<20)...) // 4 MiB of post-end zeros
	expectReason(t, verifyBytes(t, gzipWrap(t, bomb)), ReasonEnvelopeMalformed)
}

// White-box: the explicit decompression ceiling rejects rather than
// truncates, without gigabytes in the test.
func TestGzipTotalOutCeilingGuard(t *testing.T) {
	gz, verr := newGzipStream(bytes.NewReader(gzipWrap(t, writeCanonicalTar(t, grammarPkg()))))
	if verr != nil {
		t.Fatal(verr)
	}
	gz.totalOut = maxTarBytes - 10
	err := gz.readExact(make([]byte, 11))
	expectReason(t, err, ReasonCeilingExceeded)
}

// --- tar member grammar ---

func TestVerifyRejectsMemberOrderViolation(t *testing.T) {
	specs := grammarPkg()
	// Swap two descendants (root must stay first to isolate the order check).
	specs[2], specs[3] = specs[3], specs[2]
	expectReason(t, verifyBytes(t, gzipWrap(t, writeCanonicalTar(t, specs))), ReasonMemberOrder)
}

func TestVerifyRejectsDuplicateMember(t *testing.T) {
	specs := grammarPkg()
	specs = append(specs, specs[len(specs)-1])
	expectReason(t, verifyBytes(t, gzipWrap(t, writeCanonicalTar(t, specs))), ReasonMemberOrder)
}

func TestVerifyRejectsCasefoldSiblingCollision(t *testing.T) {
	root := "org.example.echo-0.1.0"
	specs := sortSpecs(append(grammarPkg(),
		memberSpec{path: root + "/install-root/Payload.bin", content: []byte("case")}))
	expectReason(t, verifyBytes(t, gzipWrap(t, writeCanonicalTar(t, specs))), ReasonMemberOrder)
}

func TestVerifyRejectsMissingParentDir(t *testing.T) {
	root := "org.example.echo-0.1.0"
	specs := sortSpecs([]memberSpec{
		{path: root, isDir: true},
		{path: root + "/manifest.json", content: []byte("{}")},
		// install-root/ directory member deliberately absent
		{path: root + "/install-root/payload.bin", content: []byte("bytes")},
	})
	expectReason(t, verifyBytes(t, gzipWrap(t, writeCanonicalTar(t, specs))), ReasonEnvelopeMalformed)
}

func TestVerifyRejectsSecondTopLevel(t *testing.T) {
	specs := append(grammarPkg(), memberSpec{path: "zzz-other", isDir: true})
	expectReason(t, verifyBytes(t, gzipWrap(t, writeCanonicalTar(t, specs))), ReasonEnvelopeMalformed)
}

func TestVerifyRejectsForbiddenPathComponents(t *testing.T) {
	root := "org.example.echo-0.1.0"
	for _, path := range []string{
		root + "/install-root/nul.txt",                     // Windows device stem
		root + "/install-root/com1",                        // Windows device stem
		root + "/install-root/trailing.",                   // component ends in '.'
		root + "/install-root/sp ace.txt",                  // charset
		root + "/install-root/ütf8.txt",                    // charset
		root + "/install-root/" + strings.Repeat("c", 256), // component > 255
	} {
		specs := sortSpecs(append(grammarPkg(), memberSpec{path: path, content: []byte("x")}))
		expectReason(t, verifyBytes(t, gzipWrap(t, writeCanonicalTar(t, specs))), ReasonEnvelopeMalformed)
	}
}

func TestVerifyRejectsNonCanonicalHeaderBytes(t *testing.T) {
	root := "org.example.echo-0.1.0"
	craft := func(mutate func(h *[tarBlockBytes]byte)) []byte {
		hdr, err := buildTarHeader(root+"/install-root/payload.bin", 5, tarModeRegular, tarTypeRegular)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&hdr)
		specs := []memberSpec{
			{path: root, isDir: true},
			{path: root + "/install-root", isDir: true},
			{path: root + "/install-root/payload.bin", content: []byte("bytes"), rawHeader: &hdr},
			{path: root + "/manifest.json", content: []byte("{}")},
		}
		return gzipWrap(t, writeCanonicalTar(t, specs))
	}
	refixChecksum := func(h *[tarBlockBytes]byte) {
		for i := 148; i < 156; i++ {
			h[i] = ' '
		}
		var sum int64
		for _, b := range h {
			sum += int64(b)
		}
		chk := fmt.Sprintf("%06o", sum)
		copy(h[148:154], chk)
		h[154] = 0
		h[155] = ' '
	}

	// Stale checksum: content tampered under an old checksum.
	expectReason(t, verifyBytes(t, craft(func(h *[tarBlockBytes]byte) { h[0] = 'x' })), ReasonEnvelopeMalformed)
	// uid != 0 (checksum refixed so only the canonical-header memcmp can catch it).
	expectReason(t, verifyBytes(t, craft(func(h *[tarBlockBytes]byte) {
		h[108] = '1'
		refixChecksum(h)
	})), ReasonEnvelopeMalformed)
	// mtime != 0.
	expectReason(t, verifyBytes(t, craft(func(h *[tarBlockBytes]byte) {
		h[136] = '7'
		refixChecksum(h)
	})), ReasonEnvelopeMalformed)
	// Non-canonical mode 0777.
	expectReason(t, verifyBytes(t, craft(func(h *[tarBlockBytes]byte) {
		copy(h[100:108], "0000777\x00")
		refixChecksum(h)
	})), ReasonEnvelopeMalformed)
	// Symlink member.
	expectReason(t, verifyBytes(t, craft(func(h *[tarBlockBytes]byte) {
		h[156] = '2'
		copy(h[157:], "target")
		refixChecksum(h)
	})), ReasonEnvelopeMalformed)
	// Global PAX header.
	expectReason(t, verifyBytes(t, craft(func(h *[tarBlockBytes]byte) {
		h[156] = 'g'
		refixChecksum(h)
	})), ReasonEnvelopeMalformed)
	// GNU long-name member.
	expectReason(t, verifyBytes(t, craft(func(h *[tarBlockBytes]byte) {
		h[156] = 'L'
		refixChecksum(h)
	})), ReasonEnvelopeMalformed)
	// Legacy '\0' regular member.
	expectReason(t, verifyBytes(t, craft(func(h *[tarBlockBytes]byte) {
		h[156] = 0
		refixChecksum(h)
	})), ReasonEnvelopeMalformed)
	// uname set (empty owner names are canonical).
	expectReason(t, verifyBytes(t, craft(func(h *[tarBlockBytes]byte) {
		copy(h[265:], "root")
		refixChecksum(h)
	})), ReasonEnvelopeMalformed)
}

func TestVerifyRejectsPAXForUSTARFitPath(t *testing.T) {
	// A short path smuggled through PAX must reject even though the
	// record itself is canonical (PAX only when USTAR cannot fit).
	root := "org.example.echo-0.1.0"
	shortPath := root + "/install-root/payload.bin"
	record, err := paxBuildPathRecord(shortPath)
	if err != nil {
		t.Fatal(err)
	}
	paxHdr, err := buildTarHeader(paxHeaderPath, int64(len(record)), tarModeRegular, tarTypePAXLocal)
	if err != nil {
		t.Fatal(err)
	}
	memberHdr, err := buildTarHeader(paxMemberPlaceholder, 5, tarModeRegular, tarTypeRegular)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for _, s := range []memberSpec{{path: root, isDir: true}, {path: root + "/install-root", isDir: true}} {
		hdr, err := buildTarHeader(tarStoredPath(s.path, tarTypeDirectory), 0, tarModeDir, tarTypeDirectory)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(hdr[:])
	}
	buf.Write(paxHdr[:])
	buf.WriteString(record)
	writeBlockPadding(&buf, len(record))
	buf.Write(memberHdr[:])
	buf.WriteString("bytes")
	writeBlockPadding(&buf, 5)
	buf.Write(make([]byte, 2*tarBlockBytes))
	expectReason(t, verifyBytes(t, gzipWrap(t, buf.Bytes())), ReasonEnvelopeMalformed)
}

func TestVerifyRejectsNonZeroPayloadPadding(t *testing.T) {
	tarBytes := writeCanonicalTar(t, grammarPkg())
	// payload.bin carries "bytes" (5 bytes); its padding starts right
	// after. Find it and poison one padding byte.
	idx := bytes.Index(tarBytes, []byte("bytes"))
	if idx < 0 {
		t.Fatal("payload not found")
	}
	bad := append([]byte{}, tarBytes...)
	bad[idx+5] = 0x41
	expectReason(t, verifyBytes(t, gzipWrap(t, bad)), ReasonEnvelopeMalformed)
}

// --- ceilings ---

func TestVerifyRejectsOversizeDeclaredPayload(t *testing.T) {
	// A single declared size past the inclusive 2-GiB sum rejects at
	// the header — before any payload byte exists or is read.
	root := "org.example.echo-0.1.0"
	hdr, err := buildTarHeader(root+"/install-root/huge.bin", maxRegularPayloadBytes+1, tarModeRegular, tarTypeRegular)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for _, s := range []memberSpec{{path: root, isDir: true}, {path: root + "/install-root", isDir: true}} {
		h, err := buildTarHeader(tarStoredPath(s.path, tarTypeDirectory), 0, tarModeDir, tarTypeDirectory)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(h[:])
	}
	buf.Write(hdr[:])
	buf.Write(make([]byte, 2*tarBlockBytes))
	expectReason(t, verifyBytes(t, gzipWrap(t, buf.Bytes())), ReasonCeilingExceeded)
}

func TestVerifyRejectsTooManySemanticMembers(t *testing.T) {
	root := "org.example.echo-0.1.0"
	specs := []memberSpec{
		{path: root, isDir: true},
		{path: root + "/install-root", isDir: true},
	}
	for i := 0; i < maxSemanticMembers; i++ {
		specs = append(specs, memberSpec{
			path:    fmt.Sprintf("%s/install-root/f%08d", root, i),
			content: []byte("x"),
		})
	}
	expectReason(t, verifyBytes(t, gzipWrap(t, writeCanonicalTar(t, sortSpecs(specs)))), ReasonCeilingExceeded)
}

func TestVerifyRejectsOversizeJSONMember(t *testing.T) {
	root := "org.example.echo-0.1.0"
	specs := sortSpecs([]memberSpec{
		{path: root, isDir: true},
		{path: root + "/install-root", isDir: true},
		{path: root + "/install-root/p.bin", content: []byte("x")},
		{path: root + "/manifest.json", content: bytes.Repeat([]byte("m"), maxJSONMemberBytes+1)},
	})
	expectReason(t, verifyBytes(t, gzipWrap(t, writeCanonicalTar(t, specs))), ReasonCeilingExceeded)
}

func TestVerifyRejectsOversizePAXRecord(t *testing.T) {
	// A PAX header whose declared record exceeds the 1,024-byte padded
	// ceiling rejects before the record is materialized.
	paxHdr, err := buildTarHeader(paxHeaderPath, maxPAXPaddedBytes+1, tarModeRegular, tarTypePAXLocal)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	rootHdr, err := buildTarHeader("org.example.echo-0.1.0/", 0, tarModeDir, tarTypeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	buf.Write(rootHdr[:])
	buf.Write(paxHdr[:])
	buf.Write(make([]byte, 2*tarBlockBytes))
	expectReason(t, verifyBytes(t, gzipWrap(t, buf.Bytes())), ReasonCeilingExceeded)
}

// --- layout admission ---

func TestVerifyRejectsUnknownLayoutMembers(t *testing.T) {
	root := "org.example.echo-0.1.0"
	cases := [][]memberSpec{
		{{path: root + "/extra.txt", content: []byte("smuggle")}},
		{{path: root + "/signatures", isDir: true}, {path: root + "/signatures/evil.sig", content: []byte("{}")}},
		{{path: root + "/provenance", isDir: true}, {path: root + "/provenance/notes.txt", content: []byte("x")}},
		{{path: root + "/signatures", isDir: true}, {path: root + "/signatures/sub", isDir: true}},
	}
	for _, extras := range cases {
		specs := sortSpecs(append(grammarPkg(), extras...))
		expectReason(t, verifyBytes(t, gzipWrap(t, writeCanonicalTar(t, specs))), ReasonEnvelopeMalformed)
	}
}

func TestVerifyRejectsEmptyArchive(t *testing.T) {
	expectReason(t, verifyBytes(t, gzipWrap(t, make([]byte, 2*tarBlockBytes))), ReasonEnvelopeMalformed)
}
