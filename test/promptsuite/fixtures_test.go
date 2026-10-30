package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/promptsuite"
)

// THE FIXTURE PLUGINS ARE THE OFFLINE PROOFS' ALONE. A live run measures
// the published Memory and Planning packages, whose own descriptions are
// part of the prompt (plugins.go); the proofs, which call no model and
// touch no network, need a plugin whose answer they choose, so that a
// grader is shown, say, a refused store. These are canned wasm guests
// with the operations of a role, defined in testdata/fixtures.json, built
// and signed here, and bound to a task's roles by its proof.

// fixture is one canned plugin: the operations a resident sees and the
// one answer every call receives.
type fixture struct {
	Key        string             `json:"key"`
	Role       string             `json:"role"`
	ID         string             `json:"id"`
	Version    string             `json:"version"`
	Interface  string             `json:"interface"`
	Operations []fixtureOperation `json:"operations"`
	// Answer is the operation_result every call receives: the guest reads
	// nothing of the call.
	Answer json.RawMessage `json:"answer"`
}

// fixtureOperation is one operation, as its descriptor declares it.
type fixtureOperation struct {
	ID      string          `json:"id"`
	Summary string          `json:"summary"`
	Primary bool            `json:"primary"`
	Input   json.RawMessage `json:"input"`
}

// loadFixtures reads testdata/fixtures.json by key.
func loadFixtures(t *testing.T) map[string]*fixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list []*fixture
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]*fixture{}
	for _, f := range list {
		out[f.Key] = f
	}
	return out
}

// fixturePlugins is a trial's plugin source in a proof: each role the
// task names is played by the fixture the proof binds to it.
type fixturePlugins struct {
	signer   *fixtureSigner
	fixtures map[string]*fixture
	bind     map[string]string // role -> fixture key
}

func (fp fixturePlugins) install(t *promptsuite.Task, slot string) (map[string]any, []bound, error) {
	if len(t.Setup.Plugins) == 0 {
		return nil, nil, nil
	}
	var picked []*fixture
	var roles []bound
	for _, role := range t.Setup.Plugins {
		f := fp.fixtures[fp.bind[role]]
		if f == nil || f.Role != role {
			return nil, nil, fmt.Errorf("the proof of %s binds no fixture of the %s role", t.ID, role)
		}
		picked = append(picked, f)
		roles = append(roles, bound{role: role, id: f.ID})
	}
	config, err := fp.signer.install(picked, slot)
	return config, roles, err
}

type fixtureSigner struct {
	dir string // where the run keeps the root and the packages

	mu       sync.Mutex
	root     *packagetest.Role
	rootFile string
	status   []byte
	minted   time.Time
	built    map[string]string // fixture key -> package file
}

// rootLife is how long one minted root is used: well inside the day the
// throwaway roots are valid for, so a long run mints a fresh one before
// the first expires under it.
const rootLife = 12 * time.Hour

// maxFrame is the largest answer frame a canned wasm guest carries.
const maxFrame = 960

func newFixtureSigner(dir string) *fixtureSigner {
	return &fixtureSigner{dir: dir, built: map[string]string{}}
}

// install puts fixtures into a trial's slot, and returns the
// configuration that pins their root.
func (f *fixtureSigner) install(fixtures []*fixture, slot string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.root == nil || time.Since(f.minted) > rootLife {
		root, status, err := packagetest.NewPlatformRelease("promptsuite-fixtures")
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(root.Env)
		if err != nil {
			return nil, err
		}
		dir, err := os.MkdirTemp(f.dir, "fixtures-")
		if err != nil {
			return nil, err
		}
		f.rootFile = filepath.Join(dir, "platform-root.json")
		if err := os.WriteFile(f.rootFile, raw, 0o600); err != nil {
			return nil, err
		}
		f.root, f.status, f.minted, f.built = root, status, time.Now(), map[string]string{}
	}
	trust := filepath.Join(slot, "data", "trust")
	if err := os.MkdirAll(trust, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), f.status, 0o600); err != nil {
		return nil, err
	}
	for _, fx := range fixtures {
		pkg, ok := f.built[fx.Key]
		if !ok {
			var err error
			if pkg, err = f.build(fx); err != nil {
				return nil, fmt.Errorf("fixture %s: %w", fx.Key, err)
			}
			f.built[fx.Key] = pkg
		}
		dir := filepath.Join(slot, "plugins", fx.ID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := copyFile(filepath.Join(dir, filepath.Base(pkg)), pkg); err != nil {
			return nil, err
		}
	}
	return map[string]any{"plugins": map[string]any{"platform_root": f.rootFile}}, nil
}

// build makes one fixture's package: a wasm guest that answers every call
// with the fixture's frozen answer, its operations' descriptors and
// argument schemas, signed at the platform tier.
func (f *fixtureSigner) build(fx *fixture) (string, error) {
	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "h1",
		"result": map[string]any{"status": "succeeded", "operation_result": json.RawMessage(fx.Answer)},
	})
	if err != nil {
		return "", err
	}
	// The guest keeps its answer in its memory below the heap it
	// allocates from (wasmgen: a frame from byte 64 up to 1024).
	if len(frame) > maxFrame {
		return "", fmt.Errorf("its answer is %d bytes as a frame; a canned guest holds at most %d", len(frame), maxFrame)
	}
	files := map[string][]byte{}
	var descriptors []map[string]any
	var methods []string
	for _, op := range fx.Operations {
		d := map[string]any{"id": op.ID, "summary": op.Summary, "effects": "read.internal", "capabilities": []string{}, "primary": op.Primary}
		if len(op.Input) > 0 && string(op.Input) != "null" {
			name := "schemas/" + op.ID + ".input.json"
			files[name] = op.Input
			d["input"] = name
		}
		descriptors = append(descriptors, d)
		methods = append(methods, op.ID)
	}
	desc, err := json.Marshal(descriptors)
	if err != nil {
		return "", err
	}
	schema := fmt.Sprintf("interfaces/%s.v1.schema.json", fx.Interface)
	variant := packagefmt.HostPlatform() + "-" + packagefmt.HostArch() + "-wasm"
	entry := "variants/" + variant + "/plugin.wasm"
	files[schema] = desc
	files[entry] = wasmgen.CannedResponder(frame)
	manifest := packagetest.BuildManifestJSON(fx.ID, fx.Version,
		[]packagetest.InterfaceSpec{{ID: fx.Interface, Version: 1, SchemaFile: schema, Methods: methods}},
		[]packagetest.VariantSpec{{
			ID: variant, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: entry,
		}},
		files, nil)
	spec := packagetest.PackageSpec{Root: fx.ID + "-" + fx.Version, Manifest: manifest, InstallFiles: files}
	if err := f.root.SignT3(&spec); err != nil {
		return "", err
	}
	out := filepath.Join(filepath.Dir(f.rootFile), fx.Key, fx.ID+"-"+fx.Version+".aiiospkg")
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", err
	}
	return out, os.WriteFile(out, packagetest.Build(spec), 0o600)
}
