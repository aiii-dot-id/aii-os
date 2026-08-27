package packagefmt

// FileDigests + ReadMember: the verified-bytes-are-loaded-bytes seam.
// Built with packagetest — which also seals that package's writer
// against this reader (any drift in the extracted builder fails here
// first).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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
