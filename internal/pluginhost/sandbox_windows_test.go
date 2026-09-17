//go:build windows

package pluginhost

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
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
	_, before, err := containArgv([]string{"voice.exe"}, nil)
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
	sup, dir, _, err := startSupervisedNativeWith(res, &v, raw, nil, &Options{}, nil, "", false, nil)
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
