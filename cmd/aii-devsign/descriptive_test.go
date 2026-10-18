package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A staging spec may carry the descriptive surface the SDK's package
// has; the bundle's manifest then names the plugin the way the catalog
// does. A spec without it produces the manifest it always did.
func TestDevsignCarriesTheDescriptiveFields(t *testing.T) {
	dir := stage(t)
	specPath := filepath.Join(dir, "devsign.json")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"title": "Memory", "description": "A working memory.", "publisher": "AIII",
		"license": "Apache-2.0", "homepage": "https://example.invalid/memory", "plugin_family": "tool_bridge",
	}
	for k, v := range want {
		spec[k] = v
	}
	edited, _ := json.Marshal(spec)
	if err := os.WriteFile(specPath, edited, 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "memory.aiiospkg")
	root := filepath.Join(t.TempDir(), "root.pub.json")
	var stdout, stderr bytes.Buffer
	if rc := runDevsign([]string{"-staging", dir, "-o", out, "-root-out", root}, &stdout, &stderr); rc != 0 {
		t.Fatalf("devsign rc=%d stderr=%s", rc, stderr.String())
	}
	manifest := manifestOf(t, out)
	for k, v := range want {
		if got, _ := manifest[k].(string); got != v {
			t.Fatalf("manifest %s = %q, want %q (manifest: %v)", k, got, v, manifest)
		}
	}
	if _, has := manifest["capability_envelope"]; !has {
		t.Fatalf("capability_envelope lost beside the descriptive fields: %v", manifest)
	}
}

// The host window the SDK's package declares survives platform signing, and
// a window that is not one is refused before anything is signed.
func TestDevsignCarriesTheHostWindow(t *testing.T) {
	dir := stage(t)
	specPath := filepath.Join(dir, "devsign.json")
	write := func(extra map[string]any) {
		t.Helper()
		raw, err := os.ReadFile(specPath)
		if err != nil {
			t.Fatal(err)
		}
		var spec map[string]any
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		for k, v := range extra {
			spec[k] = v
		}
		edited, _ := json.Marshal(spec)
		if err := os.WriteFile(specPath, edited, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(map[string]any{"aiios_min_version": "0.1.9", "aiios_max_exclusive_version": "0.2.0"})
	out := filepath.Join(t.TempDir(), "memory.aiiospkg")
	var stdout, stderr bytes.Buffer
	if rc := runDevsign([]string{"-staging", dir, "-o", out, "-root-out", filepath.Join(t.TempDir(), "root.pub.json")}, &stdout, &stderr); rc != 0 {
		t.Fatalf("devsign rc=%d stderr=%s", rc, stderr.String())
	}
	manifest := manifestOf(t, out)
	if manifest["aiios_min_version"] != "0.1.9" || manifest["aiios_max_exclusive_version"] != "0.2.0" {
		t.Fatalf("the signed manifest lost the host window: %v", manifest)
	}
	for _, bad := range []map[string]any{{"aiios_min_version": "0.1.9-beta"}, {"aiios_min_version": "0.2.0", "aiios_max_exclusive_version": "0.2.0"}} {
		write(bad)
		stderr.Reset()
		if rc := runDevsign([]string{"-staging", dir, "-o", filepath.Join(t.TempDir(), "x.aiiospkg"), "-root-out", filepath.Join(t.TempDir(), "r.json")}, &stdout, &stderr); rc == 0 || !strings.Contains(stderr.String(), "aiios_") {
			t.Fatalf("a window that is not one must be refused by name: %v rc=%d %s", bad, rc, stderr.String())
		}
	}
}

func manifestOf(t *testing.T, bundle string) map[string]any {
	t.Helper()
	f, err := os.Open(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(h.Name, "/manifest.json") || h.Name == "manifest.json" {
			var m map[string]any
			if err := json.NewDecoder(tr).Decode(&m); err != nil {
				t.Fatal(err)
			}
			return m
		}
	}
	t.Fatal("no manifest.json in the bundle")
	return nil
}
