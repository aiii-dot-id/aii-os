package packagefmt

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteTreeAdmitsAPythonRuntimesNames(t *testing.T) {
	src := t.TempDir()
	for _, rel := range []string{
		"lib/python3.12/site-packages/pkg/__init__.py", "lib/python3.12/__pycache__/os.cpython-312.pyc",
		"lib/libstdc++.so.6", "lib/PIL/.dylibs/libtiff.6.dylib", "lib/numpy-2.1.0.dist-info/RECORD", "bin/python3",

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
		if !plantVerbatim(t, src, bad) {
			// This filesystem cannot hold the name as written, so no tree
			// on it carries one. The rule the writer applies to every
			// segment is the one asserted.
			if !componentForbiddenUnder(path.Base(bad), true) {
				t.Fatalf("%q must be refused by name", bad)
			}
			continue
		}
		var b bytes.Buffer
		if _, _, err := WriteTree(&b, src, "cp1", TreeLimits{}); err == nil || !strings.Contains(err.Error(), "not admitted") {
			t.Fatalf("%q must be refused by name: %v", bad, err)
		}
	}
}

// plantVerbatim writes rel under dir with its name exactly as given and
// reports whether the directory then lists it so. On Windows the Win32
// layer rewrites a name on the way in — a trailing space or dot is
// dropped, so "lib/trailing " would land as "lib/trailing" and the writer
// would never meet the name under test — and the \\?\ form passes it to
// the filesystem untouched. A name NTFS itself refuses (a control
// character) is not planted, and false says so. Elsewhere every one of
// these names is a legal file name, and failing to plant one fails the
// test.
func plantVerbatim(t *testing.T, dir, rel string) bool {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	windows := runtime.GOOS == "windows"
	name := p
	if windows {
		name = `\\?\` + p
	}
	if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
		if !windows {
			t.Fatal(err)
		}
		return false
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == path.Base(rel) {
			return true
		}
	}
	if !windows {
		t.Fatalf("%q was written and is not listed as written: %v", rel, entries)
	}
	return false
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
