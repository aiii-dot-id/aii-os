package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// t3Package is a T3 package of the tree's own making: one native variant
// for this host, signed with a throwaway platform_release root. It
// returns the package, the root's envelope written as a file to pin with
// -platform-key, and the root's (empty) revocation snapshot.
func t3Package(t *testing.T) (pkg, rootFile string, status []byte) {
	t.Helper()
	root, status, err := packagetest.NewPlatformRelease("aiii_platform_release_verify_cli")
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
		files, map[string]any{"plugin_family": "voice_interface"})
	spec := packagetest.PackageSpec{Root: "org.example.t3-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pkg = filepath.Join(dir, "t3.aiiospkg")
	if err := os.WriteFile(pkg, packagetest.Build(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := json.Marshal(root.Env)
	if err != nil {
		t.Fatal(err)
	}
	rootFile = filepath.Join(dir, "platform_release_root.json")
	if err := os.WriteFile(rootFile, env, 0o644); err != nil {
		t.Fatal(err)
	}
	return pkg, rootFile, status
}

func TestPluginVerifyWithNoTrustDirectoryReadsTheShippedStatus(t *testing.T) {
	pkg, rootFile, status := t3Package(t)

	// No flags: the shipped roots and the shipped status. The package is
	// signed by another root, so the signature is what refuses it, never
	// the absence of a snapshot.
	var stdout, stderr bytes.Buffer
	if code := runPluginVerify([]string{pkg}, &stdout, &stderr); code == 0 {
		t.Fatalf("a package signed by a throwaway root verified under the shipped root: %s", stdout.String())
	}
	says := stderr.String()
	for _, d := range packagefmt.RevocationDomains() {
		if !strings.Contains(says, "revocation "+d.RootKeyType+": epoch ") {
			t.Errorf("the shipped %s status was not loaded with no -trust-dir:\n%s", d.RootKeyType, says)
		}
	}
	if strings.Contains(says, string(packagefmt.ReasonRevocationStatusUnavailable)) || !strings.Contains(says, string(packagefmt.ReasonPlatformSigInvalid)) {
		t.Errorf("want the signature's refusal, not an unavailable status:\n%s", says)
	}

	// A root named by path, no directory: refused, saying why and what
	// to name.
	stdout.Reset()
	stderr.Reset()
	if code := runPluginVerify([]string{"-platform-key", rootFile, pkg}, &stdout, &stderr); code == 0 {
		t.Fatalf("a T3 package verified with no snapshot for its root: %s", stdout.String())
	}
	says = stderr.String()
	if !strings.Contains(says, "not the one this program ships") || !strings.Contains(says, "-trust-dir") {
		t.Errorf("the refusal does not say why no snapshot stands in, or what to name:\n%s", says)
	}

	// The same root with its directory named: VERIFIED T3 (the control).
	trust := t.TempDir()
	if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), status, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runPluginVerify([]string{"-platform-key", rootFile, "-trust-dir", trust, pkg}, &stdout, &stderr); code != 0 {
		t.Fatalf("the control did not verify: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "VERIFIED") || !strings.Contains(stdout.String(), "T3") {
		t.Fatalf("control: %s", stdout.String())
	}
}
