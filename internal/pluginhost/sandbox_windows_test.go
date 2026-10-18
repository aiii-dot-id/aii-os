//go:build windows

package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func TestWindowsProfilesBindFullPluginAndGrantRoots(t *testing.T) {
	root := []string{`C:\identities\alice\runtime\generation-a`}
	a := wallProfileName("id.aiii.voice_one", root)
	for _, other := range []string{
		wallProfileName("id.aiii.voice-one", root),
		wallProfileName("id.aiii.voice_one", []string{`C:\identities\bob\runtime\generation-a`}),
		wallProfileName("id.aiii.voice_one", []string{`C:\identities\alice\runtime\generation-b`}),
	} {
		if a == other {
			t.Fatal("distinct plugin/identity/generation shares a profile")
		}
	}
	if wallProfileName(strings.Repeat("a", 80)+"one", root) == wallProfileName(strings.Repeat("a", 80)+"two", root) {
		t.Fatal("long IDs collide")
	}
	if len(a) > 64 {
		t.Fatal("profile exceeds Windows limit")
	}
	_, before, err := containArgv(context.Background(), []string{"voice.exe"}, nil, nativeFiles{})
	if err != nil || before.Isolated() {
		t.Fatal("argv description claims a Windows process is already contained")
	}
}

func TestWindowsNativeImageBindingLivesUntilRetirement(t *testing.T) {
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.image-life"
	v := packagefmt.Variant{VariantID: "native", Entrypoint: "variants/native/plugin.exe"}
	res := &packagefmt.Result{Tier: packagefmt.TierT3, Manifest: &packagefmt.Manifest{ID: id}, FileDigests: map[string]string{v.Entrypoint: digestOf(raw)}}
	sup, dir, _, err := startSupervisedNativeWith(context.Background(), res, &v, raw, nil, &Options{}, nil, startupAllowance(&Options{}, res.Manifest.ID, nil), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	defer sup.Close()
	runtime.GC()
	path := filepath.Join(dir, "artifact.exe")
	if err := os.Rename(path, path+".swap"); err == nil {
		t.Fatal("verified image became replaceable while child was active")
	}
	if err := sup.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".retired"); err != nil {
		t.Fatalf("retired image binding leaked: %v", err)
	}
}

// A WINDOWS CHILD READS ITS STORE MATERIAL BY CAPABILITY, AND IS GRANTED ONLY
// WHAT IS ITS IDENTITY'S OWN (Q3-b): its root and view become their entries'
// capabilities; an extracted artifact of the identity's own stays a grant.
func TestAWindowsChildHoldsItsMaterialsCapabilities(t *testing.T) {
	store := `C:\Users\a\.aii\plugins`
	root := filepath.Join(store, "r", "0123456789abcdef")
	view := filepath.Join(store, "v", "fedcba9876543210")
	var grants []string
	var material []plugstore.Entry
	for _, p := range []string{root, view, `C:\Users\a\.aii\identity-0\data\artifact`} {
		if e, ok := storeEntryAt(store, p); ok {
			material = append(material, e)
		} else {
			grants = append(grants, p)
		}
	}
	ac := wallFor("org.example.voice", grants, material)
	if !slices.Equal(ac.GrantRead, []string{`C:\Users\a\.aii\identity-0\data\artifact`}) {
		t.Fatalf("grants: %v", ac.GrantRead)
	}
	if !slices.Equal(ac.Capabilities, []string{"aiios.store.r.0123456789abcdef", "aiios.store.v.fedcba9876543210"}) {
		t.Fatalf("capabilities: %v", ac.Capabilities)
	}
}
