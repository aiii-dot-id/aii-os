//go:build windows

package pluginhost

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// .
// .
// .
func activateWindowsQualification(t *testing.T, fixture string) *ActivePlugin {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixture, "candidate.aiiospkg"))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	if hex.EncodeToString(h[:]) != "ff2d24fcdfe6941feb843c072a39748ffad49d9ed6b1aba5f6618549e0ec38cf" {
		t.Fatal("qualification package differs from frozen candidate")
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	spec := packagetest.PackageSpec{InstallFiles: map[string][]byte{}}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		root, name, ok := strings.Cut(hdr.Name, "/")
		if !ok {
			t.Fatal("missing package root")
		}
		spec.Root = root
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if name == "manifest.json" {
			spec.Manifest = b
		}
		if member, ok := strings.CutPrefix(name, "install-root/"); ok {
			spec.InstallFiles[member] = b
		}
	}
	// .
	runtimeArchive := filepath.Join(fixture, "runtime.tar.gz")
	if candidate := os.Getenv("AII_WINDOWS_VOICE_CANDIDATE"); candidate != "" {
		record, err := os.ReadFile(filepath.Join(candidate, "result.json"))
		if err != nil {
			t.Fatal(err)
		}
		rh := sha256.Sum256(record)
		if hex.EncodeToString(rh[:]) != os.Getenv("AII_WINDOWS_VOICE_CANDIDATE_SHA256") {
			t.Fatal("candidate build record differs from frozen qualification input")
		}
		var built struct {
			Passed  bool   `json:"passed"`
			Carrier string `json:"carrier_sha256"`
			Runtime string `json:"runtime_manifest_sha256"`
		}
		if err := json.Unmarshal(record, &built); err != nil || !built.Passed {
			t.Fatal("candidate build incomplete")
		}
		carrier, err := os.ReadFile(filepath.Join(candidate, "runtime", "aii-voice-t3.exe"))
		if err != nil {
			t.Fatal(err)
		}
		ch := sha256.Sum256(carrier)
		if hex.EncodeToString(ch[:]) != built.Carrier {
			t.Fatal("candidate carrier changed")
		}
		profileRaw, err := os.ReadFile(filepath.Join(candidate, "runtime", "voice-runtime.json"))
		if err != nil {
			t.Fatal(err)
		}
		ph := sha256.Sum256(profileRaw)
		if hex.EncodeToString(ph[:]) != built.Runtime {
			t.Fatal("candidate runtime inventory changed")
		}
		var profile struct {
			Files map[string]struct {
				SHA   string `json:"sha256"`
				Bytes int64  `json:"bytes"`
			} `json:"files"`
		}
		if err := json.Unmarshal(profileRaw, &profile); err != nil {
			t.Fatal(err)
		}
		tree := t.TempDir()
		for name, row := range profile.Files {
			if err := validateModelPath(name); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(candidate, "runtime", filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(raw)
			if int64(len(raw)) != row.Bytes || hex.EncodeToString(hash[:]) != row.SHA {
				t.Fatal("candidate file differs: " + name)
			}
			target := filepath.Join(tree, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(tree, "voice-runtime.json"), profileRaw, 0600); err != nil {
			t.Fatal(err)
		}
		runtimeArchive = filepath.Join(t.TempDir(), "runtime.tar.gz")
		f, err := os.Create(runtimeArchive)
		if err != nil {
			t.Fatal(err)
		}
		inventory, digest, err := packagefmt.WriteTree(f, tree, "runtime", packagefmt.TreeLimits{})
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("candidate archive: %v %v", err, closeErr)
		}
		inv, err := packagefmt.ParseInventory(inventory, packagefmt.TreeLimits{})
		if err != nil {
			t.Fatal(err)
		}
		stat, err := os.Stat(runtimeArchive)
		if err != nil {
			t.Fatal(err)
		}
		decls, err := ParseRuntimes(spec.InstallFiles[RuntimesFile])
		if err != nil {
			t.Fatal(err)
		}
		for i, d := range decls {
			if d.VariantID == "windows-x86_64-native" {
				decls[i] = RuntimeDecl{VariantID: d.VariantID, URL: d.URL, SHA256: strings.TrimPrefix(digest, "sha256:"), Size: stat.Size(), InstalledBytes: inv.InstalledBytes, Files: len(inv.Files), InventorySHA256: strings.TrimPrefix(packagefmt.InventoryDigest(inventory), "sha256:")}
			}
		}
		spec.InstallFiles[RuntimesFile], err = json.Marshal(map[string]any{"runtimes": decls})
		if err != nil {
			t.Fatal(err)
		}
		spec.InstallFiles["variants/windows-x86_64-native/plugin.exe"] = carrier
		var manifest map[string]any
		if err := json.Unmarshal(spec.Manifest, &manifest); err != nil {
			t.Fatal(err)
		}
		for _, raw := range manifest["variants"].([]any) {
			v := raw.(map[string]any)
			if v["variant_id"] == "windows-x86_64-native" {
				v["artifact_hash"] = "sha256:" + built.Carrier
			}
		}
		manifest["package_hash"] = packagetest.ReferencePackageHash(spec.InstallFiles)
		spec.Manifest, err = json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("sealed path-fix candidate carrier=%s runtime=%s build_record=%x", built.Carrier, built.Runtime, rh)
	}

	roots, role := platformRootsForTest(t)
	if err := role.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	pkg := writePkg(t, spec)
	profiles, err := ParseAccelerators(spec.InstallFiles[AcceleratorFile], []string{"windows-x86_64-native", "linux-x86_64-native", "macos-arm64-native"})
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for _, name := range profiles["windows-x86_64-native"].Models {
		selected[name] = true
	}
	models, err := ParseModels(spec.InstallFiles[ModelsFile])
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, m := range models {
		if selected[m.Name] {
			paths[m.URL] = filepath.Join(fixture, "models", filepath.FromSlash(m.Dest()))
		}
	}
	runtimes, err := ParseRuntimes(spec.InstallFiles[RuntimesFile])
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runtimes {
		if r.VariantID == "windows-x86_64-native" {
			paths[r.URL] = runtimeArchive
		}
	}
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		path, ok := paths[url]
		if !ok {
			return 0, fmt.Errorf("unbound qualification asset: %s", url)
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		f, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return 0, err
		}
		return io.Copy(w, f)
	}
	dir := t.TempDir()
	host, err := broker.New(broker.Config{Store: newBrokerStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	capture := &logSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ap, err := Activate(ctx, pkg, newRegistry(t), supervisedOpts(Options{
		Roots: roots, Broker: host, PluginModelsDir: filepath.Join(dir, "models"), PluginRuntimeDir: filepath.Join(dir, "runtime"),
		PluginDataDir: filepath.Join(dir, "data"), ModelFetcher: fetch, RuntimeFetcher: fetch, RuntimeRoots: NewRuntimeRoots(),
		ReadyTimeout: map[string]time.Duration{"id.aiii.voice": 180 * time.Second}, Log: log.New(capture, "", 0),
	}))
	t.Cleanup(func() { t.Log(capture.String()) })
	if err != nil {
		t.Fatalf("test-signed production Activate: %v\n%s", err, capture.String())
	}
	t.Cleanup(func() {
		if err := ap.Deactivate(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if !ap.Contained || !ap.sup.Containment().Isolated() {
		t.Fatal("actual AppContainer activation did not reach SAFE containment")
	}
	// .
	// .
	if ap.Readiness == nil || !ap.Readiness.Real() || ap.Readiness.ModelsLoaded != 5 || ap.Readiness.Accelerator != "cpu_vulkan" {
		t.Fatalf("unexpected real backend readiness: %+v", ap.Readiness)
	}
	if ap.RuntimeRoot == "" || len(ap.Models) != 24 || ap.Voice == nil {
		t.Fatal("activation lost runtime, models, or session binding")
	}
	return ap
}
