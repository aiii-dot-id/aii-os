package packagetest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// .
// .
// .
func TestSignT3VerifiesThroughTheRealVerifier(t *testing.T) {
	root, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
	if err != nil {
		t.Fatal(err)
	}
	vid := packagefmt.HostPlatform() + "-" + packagefmt.HostArch() + "-native"
	files := map[string][]byte{
		"interfaces/speech.session.v1.schema.json": []byte(`{"interface":"speech.session","v":1}`),
		"variants/" + vid + "/plugin":              []byte("#!/bin/sh\nexit 0\n"),
	}
	manifest := packagetest.BuildManifestJSON("org.example.t3", "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "speech.session", Version: 1, SchemaFile: "interfaces/speech.session.v1.schema.json", Methods: []string{"speech.session.open"}}},
		[]packagetest.VariantSpec{{ID: vid, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + vid + "/plugin"}},
		files, map[string]interface{}{"plugin_family": "voice_interface"})
	spec := packagetest.PackageSpec{Root: "org.example.t3-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(t.TempDir(), "t3.aiiospkg")
	if err := os.WriteFile(pkg, packagetest.Build(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	trust := t.TempDir()
	roots := packagefmt.TrustRoots{PlatformRelease: root.Env}
	roots.Revocation = packagefmt.LoadRevocationStatus(trust, roots, nil)
	if _, err := packagefmt.VerifyFile(pkg, roots); err == nil {
		t.Fatal("T3 must not verify without the platform root's revocation snapshot")
	}
	if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), status, 0o600); err != nil {
		t.Fatal(err)
	}
	roots.Revocation = packagefmt.LoadRevocationStatus(trust, roots, nil)
	res, err := packagefmt.VerifyFile(pkg, roots)
	if err != nil {
		t.Fatalf("T3 verify: %v", err)
	}
	if res.Tier != packagefmt.TierT3 {
		t.Fatalf("tier = %v, want T3", res.Tier)
	}
}
