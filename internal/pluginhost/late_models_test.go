//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func TestAPackageWhoseModelsAreAllConditionalHasItsDirectoryFromTheStart(t *testing.T) {
	roots, role := platformRootsForTest(t)
	store := openStore(t)
	language := SettingDecl{Key: "tts_language", Type: SettingEnum, Title: "Speaking language", Default: "en", Values: []string{"en", "fr"}}
	models := []ModelDecl{whenModel("fr-pack", &ModelWhen{Setting: "tts_language", Values: []string{"fr"}})}
	prepared := func(id string, stored map[string]any) (Material, *Options) {
		t.Helper()
		pkg := whenPackage(t, id, role, models, []SettingDecl{language}, packagefmt.ModelWhenMinHost)
		opts := supervisedOpts(Options{Roots: roots, HostVersion: packagefmt.ModelWhenMinHost})
		opts.StoreDir, opts.Materialize = store.Dir, func(ctx context.Context, m Material) error { return materializeInProcess(ctx, store, m, nil) }
		opts.Settings = func(string) map[string]any { return stored }
		res, err := Verify(pkg, opts)
		if err != nil {
			t.Fatal(err)
		}
		sel, err := Select(pkg, res, opts)
		if err != nil {
			t.Fatal(err)
		}
		material, err := Prepare(pkg, res, sel, opts)
		if err != nil {
			t.Fatal(err)
		}
		return material, opts
	}

	none, opts := prepared("org.example.allconditional", nil)
	if len(none.Models) != 0 || none.ModelsDir == "" {
		t.Fatalf("with no model needed yet: %d models selected, models directory %q; want none selected and a directory", len(none.Models), none.ModelsDir)
	}
	if _, err := os.Stat(none.ModelsDir); !os.IsNotExist(err) {
		t.Fatalf("the directory exists before anything acquired the material: %v", err)
	}
	if _, err := Acquire(context.Background(), none, opts); err != nil {
		t.Fatalf("acquiring a material with a directory and no model: %v", err)
	}
	if fi, err := os.Stat(none.ModelsDir); err != nil || !fi.IsDir() {
		t.Fatalf("the models directory was not made before the child would be started: %v", err)
	}
	if left, _ := os.ReadDir(none.ModelsDir); len(left) != 0 {
		t.Fatalf("the directory was made with something in it: %v", left)
	}

	// Under a value that needs the model the directory is the same one.
	french, _ := prepared("org.example.allconditional", map[string]any{"tts_language": "fr"})
	if whenNames(french.Models) != "fr-pack" || filepath.Base(french.ModelsDir) != filepath.Base(none.ModelsDir) {
		t.Fatalf("under French: models %q in %q, want fr-pack in the plugin's own directory (%q)", whenNames(french.Models), french.ModelsDir, none.ModelsDir)
	}

	// A package that declares no conditional model and selects none is as
	// it was: no directory.
	pkg := whenPackage(t, "org.example.nomodels", role, []ModelDecl{}, nil, "")
	plain := supervisedOpts(Options{Roots: roots, StoreDir: store.Dir})
	res, err := Verify(pkg, plain)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := Select(pkg, res, plain)
	if err != nil {
		t.Fatal(err)
	}
	if material, err := Prepare(pkg, res, sel, plain); err != nil || material.ModelsDir != "" {
		t.Fatalf("a package with no model at all was given a models directory: %q, %v", material.ModelsDir, err)
	}
}

func TestARunningPluginsModelsFollowItsSettingsWithoutARestart(t *testing.T) {
	base := newModelFixture(t, "base", 512)
	french := newModelFixture(t, "fr-pack", 2048)
	french.decl.When = &ModelWhen{Setting: "tts_language", Values: []string{"fr"}}
	french.decl.Path = "tts/languages/fr/pack.bin"
	lowlands := newModelFixture(t, "lowlands-pack", 1024)
	lowlands.decl.When = &ModelWhen{Setting: "tts_language", Values: []string{"de", "nl"}}
	server := &modelServer{byURL: map[string]*modelFixture{base.decl.URL: base, french.decl.URL: french, lowlands.decl.URL: lowlands}}

	store := openStore(t)
	started := modelMaterial(store, "org.example.speaks", "0.1.0", base.decl)
	if err := materializeInProcess(t.Context(), store, started, server.fetch); err != nil {
		t.Fatal(err)
	}
	dir := started.ModelsDir
	ap := &ActivePlugin{ID: "org.example.speaks", Version: "0.1.0", ModelsDir: dir, material: started,
		Settings:   []SettingDecl{{Key: "tts_language", Type: SettingEnum, Title: "Speaking language", Default: "en", Values: []string{"en", "fr", "de", "nl"}}, {Key: "speed", Type: SettingNumber, Title: "Speed"}},
		ModelDecls: []ModelDecl{base.decl, french.decl, lowlands.decl}, Models: []ModelDecl{base.decl}}

	if keys := ap.ModelSettings(); len(keys) != 1 || !keys["tts_language"] {
		t.Fatalf("the settings a model is conditional on: %v, want tts_language alone", keys)
	}
	wanted := func(stored map[string]any) string {
		t.Helper()
		got, err := ap.ModelsWanted(stored)
		if err != nil {
			t.Fatal(err)
		}
		return whenNames(got)
	}
	if got := wanted(nil); got != "base" {
		t.Fatalf("with nothing stored: %q, want the default's set", got)
	}
	if got := wanted(map[string]any{"tts_language": "fr", "speed": 2}); got != "base fr-pack" {
		t.Fatalf("with French stored: %q", got)
	}
	want, _ := ap.ModelsWanted(map[string]any{"tts_language": "fr"})
	absent := ap.ModelsAbsent(want)
	if whenNames(absent) != "fr-pack" {
		t.Fatalf("absent under French: %q, want the pack alone (the base model is on disk)", whenNames(absent))
	}
	material := ap.LateMaterial(absent)
	if material.PluginID != ap.ID || material.ModelsDir != dir || material.Runtime != nil || material.RuntimeDir != "" || whenNames(material.Models) != "fr-pack" {
		t.Fatalf("the material for a running plugin is not its absent models alone, in its own directory: %+v", material)
	}

	// The first fetch is held; the operator changes their mind.
	french.block = make(chan struct{})
	var ready readyLog
	q := newAttachedAcquirer(store, server.fetch, AcquirerConfig{Ready: ready.ready})
	q.Want(material)
	waitFor(t, "the French pack's fetch to begin", func() bool { n, _ := french.seen(); return n > 0 })
	dutch, _ := ap.ModelsWanted(map[string]any{"tts_language": "nl"})
	q.Want(ap.LateMaterial(ap.ModelsAbsent(dutch)))
	waitFor(t, "the pack for the second choice", func() bool { return len(ready.list()) == 1 })
	if _, cancelled := french.cancelled(); !cancelled {
		t.Fatal("the fetch for the value the operator left was not stopped")
	}
	got, err := os.ReadFile(modelFile(dir, lowlands.decl))
	if err != nil || string(got) != string(lowlands.data) {
		t.Fatalf("the pack the second value needs is not whole beside the model in use: %v", err)
	}
	if kept, err := os.ReadFile(modelFile(dir, base.decl)); err != nil || string(kept) != string(base.data) {
		t.Fatalf("the model the running child was started with was touched: %v", err)
	}
	if len(ap.ModelsAbsent(dutch)) != 0 {
		t.Fatal("after the fetch the second value's models still read as absent")
	}

	// And back to French, now unblocked: its pack lands at its declared
	// path under the same directory.
	close(french.block)
	q.Want(ap.LateMaterial(ap.ModelsAbsent(want)))
	waitFor(t, "the French pack", func() bool { return len(ap.ModelsAbsent(want)) == 0 })
	if _, err := os.Stat(filepath.Join(dir, "tts", "languages", "fr", "pack.bin")); err != nil {
		t.Fatalf("the pack is not at its declared path: %v", err)
	}

	// A plugin with no models directory, or that declares no model, is
	// asked nothing.
	bare := &ActivePlugin{ID: "org.example.bare", ModelDecls: []ModelDecl{french.decl}}
	if got, err := bare.ModelsWanted(map[string]any{"tts_language": "fr"}); err != nil || got != nil {
		t.Fatalf("a plugin with no models directory was told it wants %v (%v)", got, err)
	}
}
