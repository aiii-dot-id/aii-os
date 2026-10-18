//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

func TestPluginWithoutPrivateFilesDoesNotNeedAStorageUpgrade(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(base, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := brokerPkgSpec("id.example.no-files", "1.0.0", []string{"plain"}, wasmgen.Responder(), []string{"ring4.kv"})
	if err := signer(t).SignT1(&spec); err != nil {
		t.Fatal(err)
	}
	ap, err := Activate(t.Context(), writePkg(t, spec), newRegistry(t), &Options{Roots: signerRoots(t), PluginDataDir: base})
	if err != nil {
		t.Fatalf("a plugin without private file access was penalized: %v", err)
	}
	t.Cleanup(func() { _ = ap.CloseQuiet(context.Background()) })
	if b, err := os.ReadFile(base); err != nil || string(b) != "untouched" {
		t.Fatalf("unneeded storage path was changed: %q %v", b, err)
	}
}

func TestPluginStorageRenameReachesTheCertifiedBrokerAndKeepsBothPluginsUsable(t *testing.T) {
	const firstID, secondID = "id.example.reader", "id_example.reader"
	base := t.TempDir()
	storageFixture(t, filepath.Join(base, "id_example_reader"), "existing private content")
	host, err := broker.New(broker.Config{Store: newBrokerStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	opts := &Options{Roots: signerRoots(t), Broker: host, PluginDataDir: base,
		LegacyStorageOwners: func(context.Context) ([]string, error) { return []string{firstID}, nil }}
	reg := newRegistry(t)
	start := func(id, method string) *ActivePlugin {
		t.Helper()
		spec := brokerPkgSpec(id, "1.0.0", []string{method}, wasmgen.Responder(), []string{"fs.private"})
		if err := signer(t).SignT1(&spec); err != nil {
			t.Fatal(err)
		}
		ap, err := Activate(t.Context(), writePkg(t, spec), reg, opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ap.CloseQuiet(context.Background()) })
		return ap
	}
	read := func(ap *ActivePlugin) string {
		t.Helper()
		status, result := activationStorageCall(t, ap, `{"operation":"fs.read","target":{"root":"private","path":"state"}}`)
		if status != "succeeded" {
			t.Fatalf("read failed: %s %s", status, result)
		}
		var file struct {
			Data string `json:"data_b64"`
		}
		if err := json.Unmarshal(result, &file); err != nil {
			t.Fatal(err)
		}
		b, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := start(firstID, "first")
	if got := read(first); got != "existing private content" {
		t.Fatalf("upgrade lost old files: %q", got)
	}
	opts.LegacyStorageOwners = func(context.Context) ([]string, error) { return []string{firstID, secondID}, nil }
	second := start(secondID, "second")
	if status, result := activationStorageCall(t, second, `{"operation":"fs.read","target":{"root":"private","path":"state"}}`); status == "succeeded" {
		t.Fatalf("another plugin read private state: %s", result)
	}
	if status, result := activationStorageCall(t, second, `{"operation":"fs.write","target":{"root":"private","path":"state"},"arguments":{"data":"new plugin"}}`); status != "succeeded" {
		t.Fatalf("new plugin cannot use its own files: %s %s", status, result)
	}
	if got := read(first); got != "existing private content" {
		t.Fatalf("newcomer changed incumbent: %q", got)
	}
	if got := read(second); got != "new plugin" {
		t.Fatalf("newcomer lost own files: %q", got)
	}
	if err := second.Deactivate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := read(first); got != "existing private content" {
		t.Fatalf("retirement damaged incumbent: %q", got)
	}
}

func TestASlotsModelUnderItsOldNameIsAdoptedOffline(t *testing.T) {
	const id = "id.example.models"
	base := t.TempDir()
	storageFixture(t, filepath.Join(base, "id_example_models"), "verified model bytes")
	decl := ModelDecl{Name: "state", URL: "https://unreachable.invalid/model", SHA256: sha([]byte("verified model bytes")), Size: int64(len("verified model bytes"))}
	opts := &Options{AdoptModelsDir: base}
	store := useStore(t, opts, nil)
	mat := modelMaterial(store, id, "1", decl)
	mat.adopt = opts.adoptionFor(id)
	if _, err := Acquire(t.Context(), mat, opts); err != nil {
		t.Fatalf("an intact model in the slot should not need a download: %v", err)
	}
	assertStorage(t, mat.ModelsDir, "verified model bytes")
	assertStorage(t, filepath.Join(base, "id_example_models"), "verified model bytes")
}

// MATERIAL IS NAMED BY WHAT IT IS, ADOPTION BY WHOSE IT IS. Two plugins whose
// ids collapse to one old spelling prepare their view and root in the store
// by content, and adopt each from its own full-id directory.
func TestPluginStoragePreparationSeparatesAllResourceRoots(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	rt := newRuntimeFixture(t)
	models, err := json.Marshal([]ModelDecl{{Name: "state", URL: "https://unreachable.invalid/model", SHA256: sha([]byte("model")), Size: 5}})
	if err != nil {
		t.Fatal(err)
	}
	slot := t.TempDir()
	opts := supervisedOpts(Options{Roots: roots, StoreDir: t.TempDir(), AdoptModelsDir: filepath.Join(slot, "plugins-models"), AdoptRuntimeDir: filepath.Join(slot, "plugins-runtime")})
	var prior Material
	for _, id := range []string{"id.example.paths", "id_example.paths"} {
		pkg := nativePackageWithModels(t, id, child, role, runtimesJSON(t, rt), models)
		res, err := Verify(pkg, opts)
		if err != nil {
			t.Fatal(err)
		}
		selection, err := Select(pkg, res, opts)
		if err != nil {
			t.Fatal(err)
		}
		mat, err := Prepare(pkg, res, selection, opts)
		if err != nil {
			t.Fatal(err)
		}
		view, _ := plugstore.Store{Dir: opts.StoreDir}.View(mat.viewKey())
		if mat.ModelsDir != view || mat.RuntimeDir != filepath.Join(opts.StoreDir, plugstore.DirRoots) {
			t.Fatalf("the material is not named in the store: %+v", mat)
		}
		if a := mat.Adoption(); a.ModelDirs[0] != pluginStorageDir(opts.AdoptModelsDir, id) || a.ArchiveDirs[0] != filepath.Join(pluginStorageDir(opts.AdoptRuntimeDir, id), archivesDir) {
			t.Fatalf("adoption does not look in the plugin's own full-id directories: %+v", a)
		}
		if prior.PluginID != "" && prior.Adoption().ModelDirs[0] == mat.Adoption().ModelDirs[0] {
			t.Fatal("colliding IDs adopt from one directory")
		}
		prior = mat
	}
}

func TestActivationLifecycleCannotRemoveAnotherActivationsWorkingState(t *testing.T) {
	for _, boundary := range []string{"candidate_start", "candidate_refusal", "predecessor_retirement"} {
		t.Run(boundary, func(t *testing.T) {
			const id = "org.example.activationstorage"
			st := newBrokerStore(t)
			h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true, Memory: true}}})
			if err != nil {
				t.Fatal(err)
			}
			reg := newRegistry(t)
			opts := &Options{Broker: h, PluginDataDir: t.TempDir()}
			pkg := func(version string) string {
				return writePkg(t, brokerPkgSpec(id, version, []string{"roundtrip"}, wasmgen.Responder(), []string{"ring4.kv", "ring4.memory", "fs.private"}))
			}
			old, err := Activate(t.Context(), pkg("0.1.0"), reg, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = old.CloseQuiet(context.Background()) })
			var memory string
			if boundary == "candidate_start" {
				memory = writeActivationStorage(t, old)
			}
			candidate, err := ActivateShadow(t.Context(), pkg("0.2.0"), reg, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = candidate.CloseQuiet(context.Background()) })
			switch boundary {
			case "candidate_start":
				requireActivationStorage(t, old, memory)
			case "candidate_refusal":
				memory = writeActivationStorage(t, old)
				if err := candidate.CloseQuiet(t.Context()); err != nil {
					t.Fatal(err)
				}
				requireActivationStorage(t, old, memory)
			case "predecessor_retirement":
				if err := candidate.Redirect(old); err != nil {
					t.Fatal(err)
				}
				memory = writeActivationStorage(t, candidate)
				if err := old.Deactivate(t.Context()); err != nil {
					t.Fatal(err)
				}
				requireActivationStorage(t, candidate, memory)
			}
		})
	}
}
