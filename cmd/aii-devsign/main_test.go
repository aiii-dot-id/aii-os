package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// .
// .
func stage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "install-root")
	for rel, content := range map[string]string{
		"interfaces/memory.core.v1.schema.json": `{"interface":"memory.core","v":1}`,
		"variants/native/memoryd":               "\x7fELFfake-native-memoryd",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	platform := map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}[runtime.GOOS]
	if platform == "" {
		platform = "linux"
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
	spec := map[string]interface{}{
		"id": "org.example.memoryd", "version": "0.1.0",
		"interfaces": []map[string]interface{}{{
			"id": "memory.core", "version": 1,
			"schema_file": "interfaces/memory.core.v1.schema.json",
			"methods":     []string{"focus.set"},
		}},
		"variants": []map[string]interface{}{{
			"id": "native", "platform": platform, "arch": arch,
			"topology": "full_identity_host", "runtime": "native_t3_component",
			"profile": "platform_reserved", "entrypoint": "variants/native/memoryd",
		}},
	}
	raw, _ := json.Marshal(spec)
	if err := os.WriteFile(filepath.Join(dir, "devsign.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// .
// .
func TestDevsignEphemeralProducesVerifiedT3(t *testing.T) {
	dir := stage(t)
	out := filepath.Join(t.TempDir(), "memoryd.aiiospkg")
	rootOut := filepath.Join(t.TempDir(), "dev_platform_root.pub.json")
	var stdout, stderr bytes.Buffer
	if rc := runDevsign([]string{"-staging", dir, "-o", out, "-root-out", rootOut}, &stdout, &stderr); rc != 0 {
		t.Fatalf("devsign rc=%d stderr=%s", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SIGNED T3 org.example.memoryd 0.1.0") {
		t.Fatalf("stdout = %s", stdout.String())
	}
	// .
	// .
	// .
	// .
	statusOut := filepath.Join(filepath.Dir(rootOut), "aiii_platform_release_status.json")
	if _, err := os.Stat(statusOut); err != nil {
		t.Fatalf("devsign minted no empty snapshot beside the root: %v", err)
	}
	root, err := packagefmt.LoadPinnedRoot(rootOut)
	if err != nil {
		t.Fatal(err)
	}
	roots := packagefmt.TrustRoots{PlatformRelease: root}
	roots.Revocation = packagefmt.LoadRevocationStatus(filepath.Dir(rootOut), roots, nil)
	res, err := packagefmt.VerifyFile(out, roots)
	if err != nil {
		t.Fatalf("re-verify: %v", err)
	}
	if res.Tier != packagefmt.TierT3 {
		t.Fatalf("tier %s, want T3", res.Tier)
	}
	// .
	// .
	bare := packagefmt.TrustRoots{PlatformRelease: root}
	if _, err := packagefmt.VerifyFile(out, bare); err == nil {
		t.Fatal("T3 verified without the platform revocation snapshot — fail-closed per tier is broken")
	}
	// .
	other, err := packagetest.NewRole("aiii_dev_platform_other", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := packagefmt.VerifyFile(out, packagefmt.TrustRoots{PlatformRelease: other.Env}); err == nil {
		t.Fatal("a package signed by one dev root must not verify against another")
	}
}

// .
// .
// .
// .
func TestDevsignCeremonySeam(t *testing.T) {
	dir := stage(t)
	pair := filepath.Join(t.TempDir(), "pair.json")
	var stdout, stderr bytes.Buffer
	if rc := runDevsign([]string{"-staging", dir, "-payload-out", pair}, &stdout, &stderr); rc != 0 {
		t.Fatalf("phase1 rc=%d stderr=%s", rc, stderr.String())
	}
	raw, err := os.ReadFile(pair)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil || payload["package_hash"] == "" || payload["manifest_hash"] == "" {
		t.Fatalf("payload must be the closed hash pair, got %s (%v)", raw, err)
	}

	// .
	// .
	// .
	// .
	ceremony, err := packagetest.NewRole("aiii_ceremony_stand_in", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ceremony.Sign(packagetest.ArtifactKindPlatformSig, payload)
	if err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(t.TempDir(), "platform.sig.json")
	if err := os.WriteFile(sigPath, sig, 0o644); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(t.TempDir(), "root.pub.json")
	envRaw, _ := json.Marshal(ceremony.Env)
	if err := os.WriteFile(rootPath, envRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := ceremony.SignRevocationStatus(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(t.TempDir(), "aiii_platform_release_status.json")
	if err := os.WriteFile(statusPath, status, 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "memoryd.aiiospkg")
	stdout.Reset()
	stderr.Reset()
	// .
	// .
	if rc := runDevsign([]string{"-staging", dir, "-attach-sig", sigPath, "-root", rootPath, "-o", out}, &stdout, &stderr); rc == 0 {
		t.Fatal("phase2 without -status must refuse")
	}
	stdout.Reset()
	stderr.Reset()
	if rc := runDevsign([]string{"-staging", dir, "-attach-sig", sigPath, "-root", rootPath, "-status", statusPath, "-o", out}, &stdout, &stderr); rc != 0 {
		t.Fatalf("phase2 rc=%d stderr=%s", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SIGNED T3") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}
