package packagefmt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestWriteTreeAdmitsAPythonRuntimesNames(t *testing.T) {
	src := t.TempDir()
	for _, rel := range []string{
		"lib/python3.12/site-packages/pkg/__init__.py", "lib/python3.12/__pycache__/os.cpython-312.pyc",
		"lib/libstdc++.so.6", "lib/PIL/.dylibs/libtiff.6.dylib", "lib/numpy-2.1.0.dist-info/RECORD", "bin/python3",
		// .
		"python/lib/python3.11/site-packages/scipy/io/tests/data/Transparent Busy.ani",
		"python/lib/python3.11/site-packages/setuptools/launcher manifest.xml",
		"python/lib/python3.11/site-packages/setuptools/script (dev).tmpl",
		"python/lib/python3.11/site-packages/setuptools/_vendor/jaraco/text/Lorem ipsum.txt",
		"vendor/pocket/source/docs/API Reference/Reference/tts_model.md",
		"vendor/pocket/source/docs/CLI Commands/serve.md",
	} {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	inv, _, err := WriteTree(&buf, src, "cp1", TreeLimits{})
	if err != nil {
		t.Fatalf("the names a frozen Python runtime carries must pack: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	rep, err := ExtractTree(bytes.NewReader(buf.Bytes()), InventoryDigest(inv), TreeLimits{}, dest)
	if err != nil {
		t.Fatalf("and extract: %v", err)
	}
	if rep.Files != 12 {
		t.Fatalf("files = %d, want 12", rep.Files)
	}
	for _, bad := range []string{"lib/ leading.txt", "lib/trailing ", "lib/nul.txt", "lib/x.", "lib/café.py", "lib/a\tb.txt", "lib/a{b}.txt"} {
		src := t.TempDir()
		p := filepath.Join(src, filepath.FromSlash(bad))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if _, _, err := WriteTree(&b, src, "cp1", TreeLimits{}); err == nil || !strings.Contains(err.Error(), "not admitted") {
			t.Fatalf("%q must be refused by name: %v", bad, err)
		}
	}
}

func TestTheBundleRuleStillRefusesWhatTheRuntimeClassAdmits(t *testing.T) {
	for _, name := range []string{"Transparent Busy.ani", "script (dev).tmpl"} {
		if !componentForbidden(name) {
			t.Fatalf("the bundle grammar (the C owner's) must still refuse %q", name)
		}
		if componentForbiddenUnder(name, true) {
			t.Fatalf("the runtime class must admit %q", name)
		}
	}
	for _, name := range []string{" leading", "trailing ", "x.", "con.txt", "a\tb", "a{b}", "..", "."} {
		if !componentForbiddenUnder(name, true) {
			t.Fatalf("the runtime class must refuse %q", name)
		}
	}
}
