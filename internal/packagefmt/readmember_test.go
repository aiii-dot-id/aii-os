package packagefmt

// FileDigests + ReadMembers: the verified-bytes-are-loaded-bytes seam.
// Built with packagetest — which also seals that package's writer
// against this reader (any drift in the extracted builder fails here
// first).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// readMemberFixture builds an unsigned T0 package via packagetest, with
// a PAX-lane member so the second pass exercises both header forms.
func readMemberFixture() (pkg []byte, files map[string][]byte) {
	longName := "long-" + strings.Repeat("n", 120) + ".bin"
	files = map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm":     []byte("\x00asm\x01\x00\x00\x00probe"),
		"variants/linux-x86_64-wasm/" + longName:     []byte("pax payload"),
	}
	manifest := packagetest.BuildManifestJSON("org.example.probe", "0.1.0",
		[]packagetest.InterfaceSpec{{
			ID: "quarantine.probe", Version: 1,
			SchemaFile: "interfaces/quarantine.probe.v1.schema.json",
			Methods:    []string{"ping"},
		}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: "linux", Arch: "x86_64",
			Topology: "full_identity_host", Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	return packagetest.Build(packagetest.PackageSpec{
		Root: "org.example.probe-0.1.0", Manifest: manifest, InstallFiles: files,
	}), files
}

func TestVerifyExposesFileDigests(t *testing.T) {
	pkg, files := readMemberFixture()
	res, err := Verify(bytes.NewReader(pkg), TrustRoots{})
	if err != nil {
		t.Fatalf("packagetest-built T0 package must verify: %v", err)
	}
	if res.Tier != TierT0 {
		t.Fatalf("unsigned package must be T0, got %s", res.Tier)
	}
	if len(res.FileDigests) != len(files) {
		t.Fatalf("FileDigests must cover every install-root file: got %d, want %d", len(res.FileDigests), len(files))
	}
	for rel, content := range files {
		sum := sha256.Sum256(content)
		want := "sha256:" + hex.EncodeToString(sum[:])
		if got := res.FileDigests[rel]; got != want {
			t.Fatalf("FileDigests[%q] = %q, want %q", rel, got, want)
		}
	}
}

func TestReadMemberReturnsVerifiedBytes(t *testing.T) {
	pkg, files := readMemberFixture()
	path := filepath.Join(t.TempDir(), "probe.aiiospkg")
	if err := os.WriteFile(path, pkg, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := VerifyFile(path, TrustRoots{})
	if err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		got, err := ReadMember(path, rel)
		if err != nil {
			t.Fatalf("ReadMember(%q): %v", rel, err)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("ReadMember(%q) returned different bytes", rel)
		}
		// The caller's invariant, exercised end to end: extracted bytes
		// hash to the verified digest.
		sum := sha256.Sum256(got)
		if want := res.FileDigests[rel]; "sha256:"+hex.EncodeToString(sum[:]) != want {
			t.Fatalf("extracted %q does not hash to the verified digest %s", rel, want)
		}
	}
}

func TestReadMemberRefusesAbsentMember(t *testing.T) {
	pkg, _ := readMemberFixture()
	path := filepath.Join(t.TempDir(), "probe.aiiospkg")
	if err := os.WriteFile(path, pkg, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadMember(path, "variants/linux-x86_64-wasm/absent.bin")
	expectReason(t, err, ReasonEnvelopeMalformed)
	// The manifest is outside install-root: unreachable by design.
	_, err = ReadMember(path, "../manifest.json")
	expectReason(t, err, ReasonEnvelopeMalformed)
}

// One walk reads every member it is asked for: readMembers is handed a
// single reader, which cannot be rewound, so each member arriving proves
// it came from the one pass. A member named twice arrives once, in
// archive order, as the bytes the package carries.
func TestReadMembersReadsEveryNamedMemberInOneWalk(t *testing.T) {
	pkg, files := readMemberFixture()
	var rels []string
	for rel := range files {
		rels = append(rels, rel, rel)
	}
	var order []string
	err := readMembers(bytes.NewReader(pkg), rels, func(rel string, raw []byte) error {
		if !bytes.Equal(raw, files[rel]) {
			t.Errorf("%s: the walk handed over other bytes", rel)
		}
		order = append(order, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(files))
	for rel := range files {
		want = append(want, rel)
	}
	sort.Strings(want)
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatalf("each named member once, in archive order: got %q, want %q", order, want)
	}
}

// A named member the package does not carry is a typed rejection naming
// the first absent one in the order named; what the walk reached before
// it has been handed over.
func TestReadMembersNamesTheFirstAbsentMember(t *testing.T) {
	pkg, _ := readMemberFixture()
	path := filepath.Join(t.TempDir(), "probe.aiiospkg")
	if err := os.WriteFile(path, pkg, 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	err := ReadMembers(path, []string{"schemas/second.json", "variants/linux-x86_64-wasm/plugin.wasm", "schemas/first.json"},
		func(rel string, _ []byte) error {
			got = append(got, rel)
			return nil
		})
	expectReason(t, err, ReasonEnvelopeMalformed)
	if !strings.Contains(err.Error(), `"schemas/second.json" is not in the package`) {
		t.Fatalf("the refusal names the first absent member as named: %v", err)
	}
	if len(got) != 1 || got[0] != "variants/linux-x86_64-wasm/plugin.wasm" {
		t.Fatalf("the present member arrives before the refusal: %q", got)
	}
}

// The walk stops at the first error fn returns, and returns it.
func TestReadMembersStopsAtTheCallersError(t *testing.T) {
	pkg, files := readMemberFixture()
	var rels []string
	for rel := range files {
		rels = append(rels, rel)
	}
	stop := errors.New("stop")
	calls := 0
	err := readMembers(bytes.NewReader(pkg), rels, func(string, []byte) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("the caller's error ends the walk: %v after %d calls", err, calls)
	}
}

// A named member over the materialization ceiling refuses at its header,
// before a byte of it is read: the archive below declares 64 MiB + 1 and
// carries none of it.
func TestReadMembersHoldsTheMaterializationCeiling(t *testing.T) {
	root := "org.example.echo-0.1.0"
	var buf bytes.Buffer
	for _, dir := range []string{root, root + "/install-root"} {
		h, err := buildTarHeader(tarStoredPath(dir, tarTypeDirectory), 0, tarModeDir, tarTypeDirectory)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(h[:])
	}
	h, err := buildTarHeader(root+"/install-root/huge.bin", maxMemberReadBytes+1, tarModeRegular, tarTypeRegular)
	if err != nil {
		t.Fatal(err)
	}
	buf.Write(h[:])
	buf.Write(make([]byte, 2*tarBlockBytes))
	err = readMembers(bytes.NewReader(gzipWrap(t, buf.Bytes())), []string{"huge.bin"}, func(string, []byte) error {
		t.Fatal("no bytes of a member over the ceiling are handed over")
		return nil
	})
	expectReason(t, err, ReasonCeilingExceeded)
}

// MemberDigest is the one comparison every loader makes: the verified
// bytes match, other bytes do not and say both digests, and a member the
// verification never recorded matches nothing.
func TestMemberDigestHoldsBytesToTheVerification(t *testing.T) {
	pkg, files := readMemberFixture()
	res, err := Verify(bytes.NewReader(pkg), TrustRoots{})
	if err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		if want, got, ok := res.MemberDigest(rel, content); !ok || want != got || want != res.FileDigests[rel] {
			t.Fatalf("%s: the verified bytes match: want %s got %s ok %v", rel, want, got, ok)
		}
		other := append([]byte("x"), content...)
		sum := sha256.Sum256(other)
		if want, got, ok := res.MemberDigest(rel, other); ok || want != res.FileDigests[rel] || got != "sha256:"+hex.EncodeToString(sum[:]) {
			t.Fatalf("%s: other bytes do not match, naming both digests: want %s got %s ok %v", rel, want, got, ok)
		}
	}
	if want, _, ok := res.MemberDigest("variants/absent.bin", nil); ok || want != "" {
		t.Fatalf("a member with no recorded digest matches nothing: want %q ok %v", want, ok)
	}
}
