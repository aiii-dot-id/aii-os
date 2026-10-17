//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func TestANativeRuntimeThatCannotFitIsRefusedBeforeAnyFetch(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	fx := newRuntimeFixture(t)
	const id = "org.example.longpath"
	pkg := nativePackage(t, id, []byte("#!carrier\n"), role, runtimesJSON(t, fx))
	opts := supervisedOpts(Options{Roots: roots})
	store := useStore(t, opts, fx.fetch)
	// No extent is declared: the deepest file lies at least one level down.
	least := pathChars(store.Dir) + 19 + 2

	opts.PathLimit = least - 1
	_, err := Stage(context.Background(), pkg, opts)
	if fx.fetches != 0 {
		t.Fatalf("the install proceeded to download (%d fetches) past a limit of %d; err %v", fx.fetches, opts.PathLimit, err)
	}
	var long *PathLengthError
	if !errors.As(err, &long) || long.Length != least || long.Limit != least-1 || long.Store != store.Dir {
		t.Fatalf("refused %v; want a PathLengthError of %d against %d in %s", err, least, least-1, store.Dir)
	}
	if materialWaitsOnPackage(err) {
		t.Fatalf("a long store is the operator's to change, not the package's: %v", err)
	}
	for _, said := range []string{"at least " + strconv.Itoa(least), "longer than " + strconv.Itoa(least-1), store.Dir, "nothing was downloaded",
		"a store of " + strconv.Itoa(pathChars(store.Dir)-1) + " characters or fewer", "aii machine store"} {
		if !strings.Contains(err.Error(), said) {
			t.Errorf("the refusal does not say %q: %v", said, err)
		}
	}
	if left, _ := os.ReadDir(filepath.Join(store.Dir, plugstore.DirArchives)); len(left) != 0 {
		t.Fatalf("the store was written: %v", left)
	}

	opts.PathLimit = least
	staged, err := Stage(context.Background(), pkg, opts)
	if err != nil || fx.fetches != 1 {
		t.Fatalf("a runtime that fits at its limit: %v (%d fetches)", err, fx.fetches)
	}
	if got := pathChars(staged.runtimeRoot); got != pathChars(store.Dir)+19 {
		t.Fatalf("the root lies %d characters deep, the refusal counts %d", got, pathChars(store.Dir)+19)
	}
}

// THE LONGEST PATH KNOWN BEFORE THE DOWNLOAD: a model's, exactly, when it is
// longer than the runtime's deepest file can be — the store and 20 and its
// declared path; the runtime's at one level when no extent is declared —
// the store and 19; counted in the UTF-16 units Windows counts; and nothing
// at all where the platform holds no limit.
func TestPathLengthRefusalHoldsTheLongestKnownPath(t *testing.T) {
	base := filepath.Join(t.TempDir(), "plügins-𝄞")
	opts := &Options{StoreDir: base}
	const id = "org.example.paths"
	model := ModelDecl{Name: "m", Path: strings.Repeat("m", 80) + "/model.onnx"}
	mat := storeMaterial(base, id, model)
	if n, want := pathChars(base), len(filepath.Dir(base))+1+8+2; n != want {
		t.Fatalf("%s counts %d, want %d: one unit for ü, two for 𝄞", base, n, want)
	}
	modelPath := modelFile(mat.ModelsDir, model)
	if pathChars(modelPath) != pathChars(base)+20+len(model.Path) {
		t.Fatalf("a model lies %d characters deep, want the store's %d and 20 and its path's %d", pathChars(modelPath), pathChars(base), len(model.Path))
	}
	if pathChars(mat.runtimeRoot()) != pathChars(base)+19 {
		t.Fatalf("a root lies %d characters deep, want the store's %d and 19", pathChars(mat.runtimeRoot()), pathChars(base))
	}
	root := pathChars(mat.runtimeRoot()) + 2

	opts.PathLimit = pathChars(modelPath) - 1
	var long *PathLengthError
	if err := pathLengthRefusal(mat, opts); !errors.As(err, &long) || long.Path != modelPath || long.Length != pathChars(modelPath) {
		t.Fatalf("the model is the longest path: %v", err)
	}
	mat.Models = nil
	opts.PathLimit = root - 1
	if err := pathLengthRefusal(mat, opts); !errors.As(err, &long) || long.Path != mat.runtimeRoot() || long.Length != root {
		t.Fatalf("with no extent the runtime's deepest file lies one level down: %v", err)
	}
	opts.PathLimit = root
	if err := pathLengthRefusal(mat, opts); err != nil {
		t.Fatalf("at its limit: %v", err)
	}
	three := 3
	mat.Runtime.Depth = &three
	if err := pathLengthRefusal(mat, opts); !errors.As(err, &long) || long.Length != root+4 {
		t.Fatalf("a declared depth of three puts the deepest file three levels down: %v", err)
	}
	long = &PathLengthError{PluginID: id, Path: mat.runtimeRoot(), Length: root + pathChars(base), Limit: root, Store: base}
	if !strings.Contains(long.Error(), "too long for any plugin store") {
		t.Fatalf("no store can be short enough, and the refusal must not offer one: %v", long)
	}
	opts.PathLimit = 0
	if err := pathLengthRefusal(mat, opts); hostPathLimit == 0 && err != nil {
		t.Fatalf("a platform with no limit refuses nothing: %v", err)
	}
}

// THE STORE'S ARITHMETIC FROM A REALISTIC PROFILE. The account's store is
// %USERPROFILE%\.aii\plugins; a Windows account's name is at most 20
// characters, so C:\Users\<20>\.aii\plugins is 42. A root is the store and
// 19; a model the store and 20 and its declared path. The deepest files
// these tests make, and the meaning plugin's worker as the guest measured it
// (19 characters below its root and its own name), fit the 259 a program a
// plugin runs can open; and a model at the longest path a declaration may
// name is refused there, before the download, naming the store.
func TestTheStoresPathsFitFromARealisticProfile(t *testing.T) {
	store := `C:\Users\` + strings.Repeat("u", 20) + `\.aii\plugins`
	if len(store) != 42 {
		t.Fatalf("the realistic store is %d characters", len(store))
	}
	key := strings.Repeat("0", 16)
	root := store + `\r\` + key
	view := store + `\v\` + key
	if len(root) != len(store)+19 || len(view)+1 != len(store)+20 {
		t.Fatalf("root %d, view %d: the store and 19, the store and 20 with the separator", len(root), len(view)+1)
	}
	for _, rel := range []string{`engine\session.py`, `python\python.exe`, `lib\libtorch.dll`, `bin\worker.exe`,
		`meaning\worker\` + strings.Repeat("w", 32) + `.exe`} {
		if n := len(root) + 1 + len(rel); n > 259 {
			t.Errorf("a runtime file %s lies %d characters deep from a realistic profile", rel, n)
		}
	}
	for _, rel := range []string{`stt\model.bin`, `tts\languages\fr\pack.bin`, strings.Repeat("m", 80) + `\model.onnx`, `stt\tokenizer\config.json`} {
		if n := len(view) + 1 + len(rel); n > 259 {
			t.Errorf("a model %s lies %d characters deep from a realistic profile", rel, n)
		}
	}
	// The longest a declaration may name: past the limit from any profile,
	// and refused as the store's length.
	longest := ModelDecl{Name: "m", Path: strings.Repeat("a", 127) + "/" + strings.Repeat("b", 127)}
	mat := Material{PluginID: "org.example.deep", Models: []ModelDecl{longest}, ModelsDir: filepath.Join(store, "v", key), store: store}
	var long *PathLengthError
	if err := pathLengthRefusal(mat, &Options{PathLimit: 259}); !errors.As(err, &long) || long.Store != store {
		t.Fatalf("a model at the longest declared path from a realistic profile: %v", err)
	}
}

// storeMaterial is a material of one model and a runtime, named in a store
// at base, nothing of it written.
func storeMaterial(base, id string, model ModelDecl) Material {
	v := &ViewWork{ModelsSHA256: modelsDigest, Names: []string{model.Name}}
	dir, _ := plugstore.Store{Dir: base}.View(plugstore.ViewKey(v.ModelsSHA256, v.Names))
	return Material{PluginID: id, Models: []ModelDecl{model}, ModelsDir: dir, view: v, store: base,
		Runtime: &RuntimeDecl{VariantID: "v", InventorySHA256: strings.Repeat("a", 64)}, RuntimeDir: filepath.Join(base, plugstore.DirRoots),
		entry: entrypointSpec{Name: "carrier.exe", Digest: "sha256:" + strings.Repeat("b", 64)}}
}
