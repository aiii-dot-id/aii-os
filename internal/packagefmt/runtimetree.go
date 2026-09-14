package packagefmt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
// .
// .
// .
// .
type TreeLimits struct {
	MaxInstalledBytes  int64 `json:"max_installed_bytes"`
	MaxFiles           int   `json:"max_files"`
	MaxFileBytes       int64 `json:"max_file_bytes"`
	MaxCompressedBytes int64 `json:"max_compressed_bytes"`
	MaxDepth           int   `json:"max_depth"`
	// .
	MaxInventoryBytes int64 `json:"max_inventory_bytes,omitempty"`
}

// .
// .
// .
// .
// .
// .
var DefaultTreeLimits = TreeLimits{
	MaxInstalledBytes:  1 << 30,
	MaxFiles:           32768,
	MaxFileBytes:       512 << 20,
	MaxCompressedBytes: 512 << 20,
	MaxDepth:           24,
	MaxInventoryBytes:  16 << 20,
}

func (l TreeLimits) filled() TreeLimits {
	d := DefaultTreeLimits
	if l.MaxInstalledBytes <= 0 {
		l.MaxInstalledBytes = d.MaxInstalledBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = d.MaxFiles
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = d.MaxFileBytes
	}
	if l.MaxCompressedBytes <= 0 {
		l.MaxCompressedBytes = d.MaxCompressedBytes
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxInventoryBytes <= 0 {
		l.MaxInventoryBytes = d.MaxInventoryBytes
	}
	return l
}

// .
// .
// .
// .
func (l TreeLimits) tarLimits() tarLimits {
	members := l.MaxFiles + l.MaxFiles/4 + 2
	return tarLimits{
		semanticMembers: members,
		nonEndHeaders:   members * 2,
		payloadBytes:    l.MaxInstalledBytes + l.MaxInventoryBytes,
		tarBytes: l.MaxInstalledBytes + l.MaxInventoryBytes +
			int64(members*2)*tarBlockBytes + int64(members)*maxPAXPaddedBytes + int64(members)*(tarBlockBytes-1) + 2*tarBlockBytes,
		compressedBytes: l.MaxCompressedBytes,
		firstFile:       InventoryFile,
		runtimeNames:    true,
	}
}

// .
// .
// .
// .
// .
const InventoryFile = "inventory.json"

// .
type InventoryEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   string `json:"mode"`
}

// .
type Inventory struct {
	Files          []InventoryEntry `json:"files"`
	InstalledBytes int64            `json:"installed_bytes"`
}

// .
type TreeReport struct {
	Root           string
	Files          int
	InstalledBytes int64
	InventorySHA   string
	InventoryRaw   []byte
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func ExtractTree(r io.Reader, want string, limits TreeLimits, dest string) (*TreeReport, error) {
	limits = limits.filled()
	if !reSHA256.MatchString(want) {
		return nil, fail(ReasonManifestInvalid, "runtime", "inventory digest %q is not sha256:<hex>", want)
	}
	gz, verr := newGzipStreamWith(r, limits.tarLimits())
	if verr != nil {
		return nil, verr
	}
	walker := newTarWalkerWith(gz, limits.tarLimits())
	rep := &TreeReport{}
	var inv *Inventory
	expected := map[string]InventoryEntry{}
	seen := map[string]bool{}
	var root string
	for {
		member, done, verr := walker.next()
		if verr != nil {
			return nil, verr
		}
		if done {
			break
		}
		if root == "" {
			root = member.path
			rep.Root = root
			continue
		}
		rel := strings.TrimPrefix(member.path, root+"/")
		if strings.Count(rel, "/")+1 > limits.MaxDepth {
			return nil, fail(ReasonCeilingExceeded, "runtime", "member %q is deeper than %d segments", rel, limits.MaxDepth)
		}
		if member.isDir {
			if inv == nil {
				return nil, fail(ReasonEnvelopeMalformed, "runtime", "the inventory must be the archive's first file, before %q", rel)
			}
			if err := os.Mkdir(filepath.Join(dest, filepath.FromSlash(rel)), 0o700); err != nil {
				return nil, fail(ReasonEnvelopeMalformed, "runtime", "directory %q: %v", rel, err)
			}
			continue
		}
		if inv == nil {
			if rel != InventoryFile {
				return nil, fail(ReasonEnvelopeMalformed, "runtime", "the archive's first file is %q, not %s", rel, InventoryFile)
			}
			if member.size > limits.MaxInventoryBytes {
				return nil, fail(ReasonCeilingExceeded, "runtime", "the inventory exceeds %d bytes", limits.MaxInventoryBytes)
			}
			raw, verr := materializeUpTo(walker, member, limits.MaxInventoryBytes)
			if verr != nil {
				return nil, verr
			}
			sum := sha256.Sum256(raw)
			rep.InventorySHA = "sha256:" + hex.EncodeToString(sum[:])
			rep.InventoryRaw = raw
			if rep.InventorySHA != want {
				return nil, fail(ReasonPackageHashMismatch, "runtime", "the inventory's digest %s is not the pinned %s", rep.InventorySHA, want)
			}
			parsed, err := ParseInventory(raw, limits)
			if err != nil {
				return nil, fail(ReasonManifestInvalid, "runtime", "inventory: %v", err)
			}
			inv = parsed
			for _, e := range inv.Files {
				expected[e.Path] = e
			}
			continue
		}
		e, ok := expected[rel]
		if !ok {
			return nil, fail(ReasonEnvelopeMalformed, "runtime", "member %q is not in the inventory", rel)
		}
		if seen[rel] {
			return nil, fail(ReasonMemberOrder, "runtime", "member %q arrives twice", rel)
		}
		if member.size != e.Size {
			return nil, fail(ReasonPackageHashMismatch, "runtime", "member %q is %d bytes, the inventory says %d", rel, member.size, e.Size)
		}
		if member.size > limits.MaxFileBytes {
			return nil, fail(ReasonCeilingExceeded, "runtime", "member %q exceeds %d bytes", rel, limits.MaxFileBytes)
		}
		wantExec := e.Mode == "exec"
		if (member.mode == tarModeExecutable) != wantExec {
			return nil, fail(ReasonEnvelopeMalformed, "runtime", "member %q has mode %04o, the inventory says %s", rel, member.mode, e.Mode)
		}
		mode := os.FileMode(0o600)
		if wantExec {
			mode = 0o700
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return nil, fail(ReasonEnvelopeMalformed, "runtime", "member %q: %v", rel, err)
		}
		h := sha256.New()
		verr = walker.readPayload(member, io.MultiWriter(f, h))
		cerr := f.Close()
		if verr != nil {
			return nil, verr
		}
		if cerr != nil {
			return nil, fail(ReasonEnvelopeMalformed, "runtime", "member %q: %v", rel, cerr)
		}
		if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != e.SHA256 {
			return nil, fail(ReasonPackageHashMismatch, "runtime", "member %q digests %s, the inventory says %s", rel, got, e.SHA256)
		}
		seen[rel] = true
		rep.Files++
		rep.InstalledBytes += member.size
		if rep.InstalledBytes > limits.MaxInstalledBytes {
			return nil, fail(ReasonCeilingExceeded, "runtime", "the tree exceeds %d installed bytes", limits.MaxInstalledBytes)
		}
	}
	if verr := gz.finish(); verr != nil {
		return nil, verr
	}
	if inv == nil {
		return nil, fail(ReasonEnvelopeMalformed, "runtime", "the archive carries no inventory")
	}
	var missing []string
	for path := range expected {
		if !seen[path] {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		if len(missing) > 8 {
			missing = append(missing[:8], fmt.Sprintf("… %d more", len(missing)-8))
		}
		return nil, fail(ReasonEnvelopeMalformed, "runtime", "the archive lacks inventory files: %s", strings.Join(missing, ", "))
	}
	if inv.InstalledBytes != rep.InstalledBytes {
		return nil, fail(ReasonPackageHashMismatch, "runtime", "the inventory declares %d installed bytes, the archive carried %d", inv.InstalledBytes, rep.InstalledBytes)
	}
	return rep, nil
}

// .
func ParseInventory(raw []byte, limits TreeLimits) (*Inventory, error) {
	limits = limits.filled()
	var inv Inventory
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&inv); err != nil {
		return nil, err
	}
	if len(inv.Files) == 0 {
		return nil, fmt.Errorf("no files")
	}
	if len(inv.Files) > limits.MaxFiles {
		return nil, fmt.Errorf("%d files; at most %d", len(inv.Files), limits.MaxFiles)
	}
	seen := map[string]bool{}
	var total int64
	for i, e := range inv.Files {
		if e.Path == "" || e.Path == InventoryFile || strings.HasPrefix(e.Path, "/") || strings.Contains(e.Path, "\\") || strings.Contains(e.Path, "//") {
			return nil, fmt.Errorf("file %d: path %q is not a relative forward-slash path", i, e.Path)
		}
		for _, seg := range strings.Split(e.Path, "/") {
			if seg == "" || seg == "." || seg == ".." || componentForbiddenUnder(seg, true) {
				return nil, fmt.Errorf("file %d: path %q has a forbidden segment %q", i, e.Path, seg)
			}
		}
		if strings.Count(e.Path, "/")+1 > limits.MaxDepth {
			return nil, fmt.Errorf("file %d: path %q is deeper than %d", i, e.Path, limits.MaxDepth)
		}
		if seen[e.Path] {
			return nil, fmt.Errorf("file %d: path %q listed twice", i, e.Path)
		}
		seen[e.Path] = true
		if e.Size < 0 || e.Size > limits.MaxFileBytes {
			return nil, fmt.Errorf("file %d: size %d is out of bounds", i, e.Size)
		}
		if !reSHA256.MatchString(e.SHA256) {
			return nil, fmt.Errorf("file %d: sha256 is not sha256:<hex>", i)
		}
		if e.Mode != "file" && e.Mode != "exec" {
			return nil, fmt.Errorf("file %d: mode %q is file or exec", i, e.Mode)
		}
		total += e.Size
	}
	if total != inv.InstalledBytes {
		return nil, fmt.Errorf("installed_bytes %d does not sum the files (%d)", inv.InstalledBytes, total)
	}
	if total > limits.MaxInstalledBytes {
		return nil, fmt.Errorf("%d installed bytes; at most %d", total, limits.MaxInstalledBytes)
	}
	return &inv, nil
}

// .
// .
func InventoryDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
