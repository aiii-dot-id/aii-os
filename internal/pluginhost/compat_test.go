package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

// .
// .
// .
func TestTheHostWindowIsInclusiveBelowAndExclusiveAbove(t *testing.T) {
	win := func(min, max string) *packagefmt.Manifest {
		return &packagefmt.Manifest{ID: "org.example.w", Version: "1.0.0", AiiosMinVersion: min, AiiosMaxExclusiveVersion: max}
	}
	for _, tc := range []struct {
		name   string
		m      *packagefmt.Manifest
		host   string
		admits bool
		tooOld bool
	}{
		{"no window runs anywhere", win("", ""), "0.0.1", true, false},
		{"the minimum is inclusive", win("0.1.6", ""), "0.1.6", true, false},
		{"below the minimum refuses", win("0.1.6", ""), "0.1.5", false, true},
		{"the maximum is exclusive", win("", "0.2.0"), "0.2.0", false, false},
		{"below the maximum runs", win("", "0.2.0"), "0.1.9", true, false},
		{"inside both runs", win("0.1.0", "0.2.0"), "0.1.6", true, false},
		{"a prerelease host cannot be ranked against a bound, and refuses", win("0.2.0", ""), "0.2.0-beta.1", false, false},
		{"a package with no window runs on a host that cannot rank itself", win("", ""), "not-a-version", true, false},
		{"a nil manifest is not a refusal", nil, "0.1.6", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkHostWindow(tc.m, tc.host)
			if tc.admits {
				if err != nil {
					t.Fatalf("admitted, got: %v", err)
				}
				return
			}
			var hv *HostVersionError
			if !errors.As(err, &hv) {
				t.Fatalf("a refusal must be typed, got: %v", err)
			}
			if hv.HostTooOld != tc.tooOld {
				t.Fatalf("HostTooOld = %v, want %v (%v)", hv.HostTooOld, tc.tooOld, hv)
			}
			if !strings.Contains(hv.Error(), tc.host) {
				t.Fatalf("the refusal must name this host: %v", hv)
			}
		})
	}
	// .
	// .
	// .
	err := checkHostWindow(win("9.9.9", "10.0.0"), "not-a-version")
	var unknown *HostVersionError
	if !errors.As(err, &unknown) || !unknown.Unknown {
		t.Fatalf("an undecidable window must refuse typed: %v", err)
	}
	if !strings.Contains(unknown.Error(), "cannot be decided") || !strings.Contains(unknown.Error(), "9.9.9") {
		t.Fatalf("the refusal must say what could not be decided: %v", unknown)
	}

	// .
	// .
	// .
	if err := checkHostWindow(win(version.Authored(), ""), hostVersionFor(nil)); err != nil {
		t.Fatalf("this host must run a package authored for it: %v", err)
	}
	if got := hostVersionFor(&Options{HostVersion: "1.2.3"}); got != "1.2.3" {
		t.Fatalf("a caller's host version is not used: %q", got)
	}
}

// .
// .
func windowedPackage(t *testing.T, id string, root *packagetest.Role, models []byte, extra map[string]interface{}) string {
	t.Helper()
	vid := hostVariantID()
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/" + vid + "/child":                 []byte("#!/bin/sh\nexit 0\n"),
	}
	if models != nil {
		files[ModelsFile] = models
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{ID: vid, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + vid + "/child"}},
		files, extra)
	spec := packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	return writePkg(t, spec)
}

// .
// .
// .
// .
// .
func TestAnActivationOutsideTheHostWindowRefusesBeforeAnyMaterial(t *testing.T) {
	roots, role := platformRootsForTest(t)
	fx := newModelFixture(t, "stt.bin", 4096)
	models, err := json.Marshal([]ModelDecl{fx.decl})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pkg := windowedPackage(t, "org.example.toonew", role, models, map[string]interface{}{"aiios_min_version": "99.0.0"})
	opts := supervisedOpts(Options{Roots: roots, PluginModelsDir: dir, ModelFetcher: fx.fetch, HostVersion: "0.1.6"})
	_, err = Activate(ctx, pkg, newRegistry(t), opts)
	var hv *HostVersionError
	if !errors.As(err, &hv) {
		t.Fatalf("a package authored for a newer host must refuse typed: %v", err)
	}
	if !hv.HostTooOld || !strings.Contains(hv.Error(), "99.0.0") || !strings.Contains(hv.Error(), "0.1.6") {
		t.Fatalf("the refusal names both versions: %v", hv)
	}
	if calls, _ := fx.seen(); calls != 0 {
		t.Fatalf("material was fetched for a package this host cannot run: %d calls", calls)
	}
	if _, serr := os.Stat(dir + "/org_example_toonew"); serr == nil {
		t.Fatal("a models directory was made for a package this host cannot run")
	}

	// .
	old := windowedPackage(t, "org.example.tooold", role, nil, map[string]interface{}{"aiios_max_exclusive_version": "0.1.0"})
	_, err = Activate(ctx, old, newRegistry(t), supervisedOpts(Options{Roots: roots, HostVersion: "0.1.6"}))
	if !errors.As(err, &hv) || hv.HostTooOld {
		t.Fatalf("a package authored for older hosts must refuse typed, not too-old: %v", err)
	}

	// .
	// .
	inside := windowedPackage(t, "org.example.inside", role, nil, map[string]interface{}{
		"aiios_min_version": "0.1.0", "aiios_max_exclusive_version": "9.0.0"})
	_, err = Activate(ctx, inside, newRegistry(t), supervisedOpts(Options{Roots: roots, HostVersion: "0.1.6"}))
	if errors.As(err, &hv) {
		t.Fatalf("a host inside the window was refused by it: %v", hv)
	}
}
