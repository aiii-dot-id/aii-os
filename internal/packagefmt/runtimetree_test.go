package packagefmt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .

type treeFile struct {
	rel     string
	content []byte
	exec    bool
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// .
// .
func buildTree(t *testing.T, root string, files []treeFile, mutate func(inv *Inventory)) (archive []byte, invDigest string) {
	t.Helper()
	inv := Inventory{}
	for _, f := range files {
		mode := "file"
		if f.exec {
			mode = "exec"
		}
		inv.Files = append(inv.Files, InventoryEntry{Path: f.rel, Size: int64(len(f.content)), SHA256: digestOf(f.content), Mode: mode})
		inv.InstalledBytes += int64(len(f.content))
	}
	if mutate != nil {
		mutate(&inv)
	}
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	specs := []memberSpec{{path: root, isDir: true}, {path: root + "/" + InventoryFile, content: raw}}
	dirs := map[string]bool{}
	var rest []memberSpec
	for _, f := range files {
		parts := strings.Split(f.rel, "/")
		for i := 1; i < len(parts); i++ {
			d := root + "/" + strings.Join(parts[:i], "/")
			if !dirs[d] {
				dirs[d] = true
				rest = append(rest, memberSpec{path: d, isDir: true})
			}
		}
		mode := int64(tarModeRegular)
		if f.exec {
			mode = tarModeExecutable
		}
		rest = append(rest, memberSpec{path: root + "/" + f.rel, content: f.content, mode: mode})
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].path < rest[j].path })
	specs = append(specs, rest...)
	return gzipWrap(t, writeCanonicalTar(t, specs)), digestOf(raw)
}

var treeFixture = []treeFile{
	{rel: "bin/carrier", content: []byte("#!carrier\n"), exec: true},
	{rel: "lib/libtorch.so", content: bytes.Repeat([]byte("torch"), 2000)},
	{rel: "lib/sub/deep/notes.txt", content: []byte("deep")},
	{rel: "python/pyvenv.cfg", content: []byte("home = here\n")},
}

func TestExtractTreeMaterializesAVerifiedRuntime(t *testing.T) {
	archive, want := buildTree(t, "cp1", treeFixture, nil)
	dest := filepath.Join(t.TempDir(), "root.partial")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	rep, err := ExtractTree(bytes.NewReader(archive), want, TreeLimits{}, dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if rep.Files != 4 || rep.Root != "cp1" || rep.InventorySHA != want || rep.InstalledBytes != 10+10000+4+12 {
		t.Fatalf("report = %+v", rep)
	}
	for _, f := range treeFixture {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(f.rel)))
		if err != nil || !bytes.Equal(got, f.content) {
			t.Fatalf("%s: %v", f.rel, err)
		}
		st, _ := os.Stat(filepath.Join(dest, filepath.FromSlash(f.rel)))
		if exec := st.Mode().Perm()&0o100 != 0; exec != f.exec {
			t.Fatalf("%s: exec bit %v, want %v", f.rel, exec, f.exec)
		}
	}
	// .
	var count int
	filepath.Walk(dest, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			count++
		}
		return nil
	})
	if count != 4 {
		t.Fatalf("%d files landed, want 4 (the inventory is not written)", count)
	}
}

func TestExtractTreeRefusesWhatTheInventoryDoesNotBless(t *testing.T) {
	cases := []struct {
		name   string
		files  []treeFile
		mutate func(*Inventory)
		want   string
		limits TreeLimits
		reason string
	}{
		{name: "altered content", files: append([]treeFile(nil), treeFixture...), mutate: func(inv *Inventory) {
			inv.Files[1].SHA256 = digestOf([]byte("not torch"))
		}, reason: "digests"},
		{name: "extra member", files: treeFixture, mutate: func(inv *Inventory) {
			inv.Files = inv.Files[:len(inv.Files)-1]
			inv.InstalledBytes -= 12
		}, reason: "not in the inventory"},
		{name: "missing member", files: treeFixture, mutate: func(inv *Inventory) {
			inv.Files = append(inv.Files, InventoryEntry{Path: "lib/ghost.so", Size: 3, SHA256: digestOf([]byte("abc")), Mode: "file"})
			inv.InstalledBytes += 3
		}, reason: "lacks inventory files"},
		{name: "wrong mode", files: treeFixture, mutate: func(inv *Inventory) {
			inv.Files[0].Mode = "file"
		}, reason: "mode"},
		{name: "pinned digest differs", files: treeFixture, want: digestOf([]byte("another inventory")), reason: "not the pinned"},
		{name: "file over the ceiling", files: treeFixture, limits: TreeLimits{MaxFileBytes: 100}, reason: "out of bounds"},
		{name: "tree over the ceiling", files: treeFixture, limits: TreeLimits{MaxInstalledBytes: 5000}, reason: "installed bytes"},
		{name: "too deep", files: treeFixture, limits: TreeLimits{MaxDepth: 3}, reason: "deeper"},
		{name: "size lies", files: treeFixture, mutate: func(inv *Inventory) {
			inv.Files[2].Size = 5
			inv.InstalledBytes++
		}, reason: "bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			archive, real := buildTree(t, "cp1", c.files, c.mutate)
			want := c.want
			if want == "" {
				want = real
			}
			dest := filepath.Join(t.TempDir(), "root.partial")
			if err := os.Mkdir(dest, 0o700); err != nil {
				t.Fatal(err)
			}
			_, err := ExtractTree(bytes.NewReader(archive), want, c.limits, dest)
			if err == nil {
				t.Fatalf("%s must be refused", c.name)
			}
			if !strings.Contains(err.Error(), c.reason) {
				t.Fatalf("%s: refusal %q does not name %q", c.name, err, c.reason)
			}
		})
	}
	// .
	// .
	specs := []memberSpec{{path: "cp1", isDir: true}, {path: "cp1/bin", isDir: true}, {path: "cp1/bin/x", content: []byte("x")}}
	dest := filepath.Join(t.TempDir(), "root.partial")
	_ = os.Mkdir(dest, 0o700)
	if _, err := ExtractTree(bytes.NewReader(gzipWrap(t, writeCanonicalTar(t, specs))), digestOf([]byte("x")), TreeLimits{}, dest); err == nil || !strings.Contains(err.Error(), "inventory") {
		t.Fatalf("a tree without a leading inventory must be refused: %v", err)
	}
}

func TestParseInventoryHoldsTheBounds(t *testing.T) {
	good := Inventory{Files: []InventoryEntry{{Path: "bin/x", Size: 1, SHA256: digestOf([]byte("a")), Mode: "exec"}}, InstalledBytes: 1}
	raw, _ := json.Marshal(good)
	if _, err := ParseInventory(raw, TreeLimits{}); err != nil {
		t.Fatalf("a good inventory parses: %v", err)
	}
	bad := []struct {
		name   string
		mutate func(*Inventory)
	}{
		{"traversal", func(i *Inventory) { i.Files[0].Path = "../x" }},
		{"absolute", func(i *Inventory) { i.Files[0].Path = "/bin/x" }},
		{"backslash", func(i *Inventory) { i.Files[0].Path = "bin\\x" }},
		{"the inventory itself", func(i *Inventory) { i.Files[0].Path = InventoryFile }},
		{"bad mode", func(i *Inventory) { i.Files[0].Mode = "setuid" }},
		{"bad digest", func(i *Inventory) { i.Files[0].SHA256 = "abc" }},
		{"sum lies", func(i *Inventory) { i.InstalledBytes = 2 }},
		{"duplicate", func(i *Inventory) { i.Files = append(i.Files, i.Files[0]); i.InstalledBytes = 2 }},
		{"windows device", func(i *Inventory) { i.Files[0].Path = "bin/con.exe" }},
	}
	for _, c := range bad {
		inv := good
		inv.Files = append([]InventoryEntry(nil), good.Files...)
		c.mutate(&inv)
		raw, _ := json.Marshal(inv)
		if _, err := ParseInventory(raw, TreeLimits{}); err == nil {
			t.Fatalf("%s must be refused", c.name)
		}
	}
	if _, err := ParseInventory([]byte(`{"files":[],"installed_bytes":0,"extra":1}`), TreeLimits{}); err == nil {
		t.Fatal("unknown fields and empty inventories are refused")
	}
}

func TestBundleLimitsAreTheSharedConstants(t *testing.T) {
	if bundleLimits.semanticMembers != maxSemanticMembers || bundleLimits.payloadBytes != maxRegularPayloadBytes || bundleLimits.compressedBytes != maxCompressedBytes {
		t.Fatal("the bundle profile must be the C-mirrored constants, untouched")
	}
	l := DefaultTreeLimits.tarLimits()
	if l.semanticMembers <= DefaultTreeLimits.MaxFiles || l.payloadBytes < DefaultTreeLimits.MaxInstalledBytes {
		t.Fatalf("the runtime profile must admit its own files: %+v", l)
	}
}

// .
// .
// .
func TestExtractTreeReadsALargeInventoryUnderTheProfilesCeiling(t *testing.T) {
	var files []treeFile
	for i := 0; i < 9000; i++ {
		files = append(files, treeFile{rel: fmt.Sprintf("python/site-packages/pkg%03d/module_%05d_with_a_reasonably_long_name.py", i%97, i), content: []byte("x")})
	}
	archive, want := buildTree(t, "cp1", files, nil)
	dest := filepath.Join(t.TempDir(), "root.partial")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	rep, err := ExtractTree(bytes.NewReader(archive), want, TreeLimits{}, dest)
	if err != nil {
		t.Fatalf("an inventory past 1 MiB but within the profile's ceiling must be read: %v", err)
	}
	if rep.Files != 9000 || int64(len(rep.InventoryRaw)) <= 1<<20 {
		t.Fatalf("report files=%d inventory=%d bytes (the case must exceed the JSON plane's 1 MiB)", rep.Files, len(rep.InventoryRaw))
	}
	// .
	dest2 := filepath.Join(t.TempDir(), "root2.partial")
	if err := os.Mkdir(dest2, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTree(bytes.NewReader(archive), want, TreeLimits{MaxInventoryBytes: 1 << 20}, dest2); err == nil || !strings.Contains(err.Error(), "inventory exceeds") {
		t.Fatalf("the profile's inventory ceiling must refuse by name, got %v", err)
	}
}
