package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
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
const RuntimesFile = "runtime.json"

// .
const MaxRuntimes = 8

var reHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// .
type RuntimeDecl struct {
	VariantID       string `json:"variant_id"`
	URL             string `json:"url"`
	SHA256          string `json:"sha256"`
	Size            int64  `json:"size"`
	InstalledBytes  int64  `json:"installed_bytes"`
	Files           int    `json:"files"`
	InventorySHA256 string `json:"inventory_sha256"`
}

// .
type RuntimeError struct {
	PluginID string
	Detail   string
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("plugin %s: runtime: %s", e.PluginID, e.Detail)
}

// .
// .
type RuntimeMissingError struct {
	PluginID string
	Cause    error
}

func (e *RuntimeMissingError) Error() string {
	return fmt.Sprintf("plugin %s: its runtime tree is not installed and cannot be fetched now (%v) — the activation refuses rather than run without it", e.PluginID, e.Cause)
}

// .
func ParseRuntimes(raw []byte) ([]RuntimeDecl, error) {
	var doc struct {
		Runtimes []RuntimeDecl `json:"runtimes"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if len(doc.Runtimes) == 0 || len(doc.Runtimes) > MaxRuntimes {
		return nil, fmt.Errorf("runtime.json declares %d runtimes; one to %d", len(doc.Runtimes), MaxRuntimes)
	}
	seen := map[string]bool{}
	for i, d := range doc.Runtimes {
		if !reModelName.MatchString(d.VariantID) {
			return nil, fmt.Errorf("runtime %d: variant_id %q is not a token", i, d.VariantID)
		}
		if seen[d.VariantID] {
			return nil, fmt.Errorf("runtime %d: variant %s declared twice", i, d.VariantID)
		}
		seen[d.VariantID] = true
		if !strings.HasPrefix(d.URL, "https://") {
			return nil, fmt.Errorf("runtime %d: url must be https", i)
		}
		if !reHex64.MatchString(d.SHA256) || !reHex64.MatchString(d.InventorySHA256) {
			return nil, fmt.Errorf("runtime %d: sha256 and inventory_sha256 are 64 hex digits", i)
		}
		if d.Size <= 0 || d.InstalledBytes <= 0 || d.Files <= 0 {
			return nil, fmt.Errorf("runtime %d: size, installed_bytes and files are positive", i)
		}
	}
	return doc.Runtimes, nil
}

// .
// .
func loadRuntime(pkgPath string, res *packagefmt.Result, m *packagefmt.Manifest, variantID string) (*RuntimeDecl, error) {
	if _, present := res.FileDigests[RuntimesFile]; !present {
		return nil, nil
	}
	raw, err := loadVerifiedMember(pkgPath, res, RuntimesFile)
	if err != nil {
		return nil, err
	}
	decls, err := ParseRuntimes(raw)
	if err != nil {
		return nil, &RuntimeError{PluginID: m.ID, Detail: err.Error()}
	}
	for i := range decls {
		if decls[i].VariantID == variantID {
			return &decls[i], nil
		}
	}
	return nil, nil
}

// .
// .
// .
// .
// .
type RuntimeRoots struct {
	mu   sync.Mutex
	refs map[string]int
}

// .
func NewRuntimeRoots() *RuntimeRoots { return &RuntimeRoots{refs: map[string]int{}} }

// .
func (r *RuntimeRoots) Pin(root string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refs[root]++
}

// .
func (r *RuntimeRoots) Release(root string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refs[root] > 0 {
		r.refs[root]--
	}
	if r.refs[root] == 0 {
		delete(r.refs, root)
	}
}

// .
func (r *RuntimeRoots) Refs(root string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refs[root]
}

// .
// .
// .
// .
// .
// .
func (r *RuntimeRoots) Retire(pluginDir string, keep int) ([]string, error) {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type root struct {
		path string
		at   time.Time
	}
	var roots []root
	for _, e := range entries {
		if !e.IsDir() || !reHex64.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		roots = append(roots, root{path: filepath.Join(pluginDir, e.Name()), at: info.ModTime()})
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].at.After(roots[j].at) })
	var removed []string
	kept := map[string]bool{}
	for i, rt := range roots {
		if i < keep || r.Refs(rt.path) > 0 {
			kept[rt.path] = true
			continue
		}
		if err := os.RemoveAll(rt.path); err != nil {
			return removed, err
		}
		_ = os.Remove(recordPath(rt.path))
		removed = append(removed, rt.path)
	}
	// .
	// .
	// .
	referenced := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, recordSuffix) {
			continue
		}
		root := filepath.Join(pluginDir, strings.TrimSuffix(name, recordSuffix))
		if !kept[root] {
			if _, err := os.Stat(root); err != nil {
				_ = os.Remove(filepath.Join(pluginDir, name))
			}
			continue
		}
		if rec, err := readRecord(root); err == nil {
			referenced[rec.ArchiveSHA256] = true
		}
	}
	archives, _ := os.ReadDir(filepath.Join(pluginDir, archivesDir))
	for _, a := range archives {
		name := a.Name()
		if a.IsDir() || !strings.HasSuffix(name, archiveSuffix) {
			continue
		}
		if !referenced[strings.TrimSuffix(name, archiveSuffix)] {
			_ = os.Remove(filepath.Join(pluginDir, archivesDir, name))
		}
	}
	return removed, nil
}

// .
// .
// .
// .
type entrypointSpec struct {
	Name   string
	Bytes  []byte
	Digest string
}

// .
// .
// .
// .
func RuntimeRootKey(variantID, inventorySHA256, entryName, entryDigest string) string {
	sum := sha256.Sum256([]byte("aii-runtime-root\n" + variantID + "\n" + inventorySHA256 + "\n" + entryName + "\n" + entryDigest + "\n"))
	return hex.EncodeToString(sum[:])
}

// .
// .
// .
// .
type rootRecord struct {
	VariantID        string `json:"variant_id"`
	InventorySHA256  string `json:"inventory_sha256"`
	ArchiveSHA256    string `json:"archive_sha256"`
	Entrypoint       string `json:"entrypoint"`
	EntrypointSHA256 string `json:"entrypoint_sha256"`
	InstalledAt      string `json:"installed_at"`
	Files            int    `json:"files"`
	InstalledBytes   int64  `json:"installed_bytes"`
	Inventory        []byte `json:"inventory"`
}

const (
	recordSuffix  = ".record.json"
	archivesDir   = "archives"
	archiveSuffix = ".tar.gz"
)

func recordPath(root string) string { return root + recordSuffix }

func readRecord(root string) (*rootRecord, error) {
	raw, err := os.ReadFile(recordPath(root))
	if err != nil {
		return nil, err
	}
	var rec rootRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// .
// .
// .
// .
var ensureLocks sync.Map

func lockRoot(root string) func() {
	v, _ := ensureLocks.LoadOrStore(root, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
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
// .
// .
func EnsureRuntime(ctx context.Context, pluginID string, d *RuntimeDecl, dir string, fetch ModelFetcher, limits packagefmt.TreeLimits, entry entrypointSpec, logf func(string, ...interface{})) (string, error) {
	if entry.Name == "" || strings.ContainsAny(entry.Name, "/\\") {
		return "", &RuntimeError{PluginID: pluginID, Detail: fmt.Sprintf("entrypoint name %q is not one file name", entry.Name)}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("pluginhost: runtime dir: %w", err)
	}
	key := RuntimeRootKey(d.VariantID, d.InventorySHA256, entry.Name, entry.Digest)
	root := filepath.Join(dir, key)
	unlock := lockRoot(root)
	defer unlock()
	if _, err := os.Stat(root); err == nil {
		if verr := VerifyRuntimeRoot(root, d, entry, limits); verr != nil {
			return "", &RuntimeError{PluginID: pluginID, Detail: fmt.Sprintf("the installed runtime does not verify: %v", verr)}
		}
		return root, nil
	}
	archive, err := ensureArchive(ctx, pluginID, d, dir, fetch, logf)
	if err != nil {
		return "", err
	}
	partial, err := os.MkdirTemp(dir, key+".partial-")
	if err != nil {
		return "", fmt.Errorf("pluginhost: runtime root: %w", err)
	}
	abandon := func() { _ = os.RemoveAll(partial) }
	f, err := os.Open(archive)
	if err != nil {
		abandon()
		return "", err
	}
	rep, xerr := packagefmt.ExtractTree(f, "sha256:"+d.InventorySHA256, limits, partial)
	f.Close()
	if xerr != nil {
		abandon()
		_ = os.Remove(archive)
		return "", &RuntimeError{PluginID: pluginID, Detail: fmt.Sprintf("the runtime archive was refused: %v", xerr)}
	}
	if rep.Files != d.Files || rep.InstalledBytes != d.InstalledBytes {
		abandon()
		_ = os.Remove(archive)
		return "", &RuntimeError{PluginID: pluginID, Detail: fmt.Sprintf("the archive holds %d files / %d bytes, the declaration says %d / %d", rep.Files, rep.InstalledBytes, d.Files, d.InstalledBytes)}
	}
	inv, perr := packagefmt.ParseInventory(rep.InventoryRaw, limits)
	if perr != nil {
		abandon()
		return "", &RuntimeError{PluginID: pluginID, Detail: fmt.Sprintf("inventory: %v", perr)}
	}
	if err := placeEntrypoint(partial, inv, entry); err != nil {
		abandon()
		return "", &RuntimeError{PluginID: pluginID, Detail: err.Error()}
	}
	rec := rootRecord{VariantID: d.VariantID, InventorySHA256: d.InventorySHA256, ArchiveSHA256: d.SHA256,
		Entrypoint: entry.Name, EntrypointSHA256: entry.Digest, InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Files: rep.Files, InstalledBytes: rep.InstalledBytes, Inventory: rep.InventoryRaw}
	raw, err := json.Marshal(rec)
	if err != nil {
		abandon()
		return "", err
	}
	if err := os.WriteFile(recordPath(root), raw, 0o600); err != nil {
		abandon()
		return "", err
	}
	if err := os.Rename(partial, root); err != nil {
		abandon()
		_ = os.Remove(recordPath(root))
		return "", fmt.Errorf("pluginhost: publish runtime root: %w", err)
	}
	if logf != nil {
		logf("plugin %s: runtime root %s installed — %d files, %d bytes, verified against the pinned inventory", pluginID, key[:12], rep.Files, rep.InstalledBytes)
	}
	return root, nil
}

// .
// .
// .
// .
// .
func ensureArchive(ctx context.Context, pluginID string, d *RuntimeDecl, dir string, fetch ModelFetcher, logf func(string, ...interface{})) (string, error) {
	adir := filepath.Join(dir, archivesDir)
	if err := os.MkdirAll(adir, 0o700); err != nil {
		return "", fmt.Errorf("pluginhost: runtime archives: %w", err)
	}
	path := filepath.Join(adir, d.SHA256+archiveSuffix)
	if st, err := os.Stat(path); err == nil {
		if sum, n, herr := hashFile(path); herr == nil && n == st.Size() && n == d.Size && sum == d.SHA256 {
			return path, nil
		}
		// .
		_ = os.Remove(path)
	}
	if fetch == nil {
		return "", &RuntimeMissingError{PluginID: pluginID, Cause: fmt.Errorf("offline: no download path")}
	}
	partial := path + ".partial"
	if err := fetchArchive(ctx, pluginID, d, partial, fetch, logf); err != nil {
		return "", &RuntimeMissingError{PluginID: pluginID, Cause: err}
	}
	if err := os.Rename(partial, path); err != nil {
		return "", err
	}
	return path, nil
}

// .
// .
func fetchArchive(ctx context.Context, pluginID string, d *RuntimeDecl, path string, fetch ModelFetcher, logf func(string, ...interface{})) error {
	var offset int64
	if st, err := os.Stat(path); err == nil {
		offset = st.Size()
		if offset > d.Size {
			_ = os.Remove(path)
			offset = 0
		}
	}
	if offset < d.Size {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		lw := &limitedWriter{w: f, remaining: d.Size - offset}
		_, ferr := fetch(ctx, d.URL, offset, lw)
		cerr := f.Close()
		if lw.overflow {
			_ = os.Remove(path)
			return fmt.Errorf("the archive is larger than its declared %d bytes", d.Size)
		}
		if ferr != nil {
			if strings.Contains(ferr.Error(), "does not resume") {
				_ = os.Remove(path)
			}
			return ferr
		}
		if cerr != nil {
			return cerr
		}
	}
	sum, n, err := hashFile(path)
	if err != nil {
		return err
	}
	if n != d.Size {
		if n < d.Size {
			return fmt.Errorf("the archive is incomplete (%d of %d bytes); resume later", n, d.Size)
		}
		_ = os.Remove(path)
		return fmt.Errorf("the archive is %d bytes, the declaration says %d", n, d.Size)
	}
	if sum != d.SHA256 {
		_ = os.Remove(path)
		return fmt.Errorf("the archive does not match its declared digest")
	}
	if logf != nil {
		logf("plugin %s: runtime archive verified (%d bytes)", pluginID, n)
	}
	return nil
}

// .
// .
// .
// .
func placeEntrypoint(root string, inv *packagefmt.Inventory, entry entrypointSpec) error {
	for _, e := range inv.Files {
		if e.Path == entry.Name || strings.HasPrefix(e.Path, entry.Name+"/") {
			return fmt.Errorf("the runtime tree already holds %s; the carrier is the bundle's, not the archive's", e.Path)
		}
	}
	target := filepath.Join(root, entry.Name)
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("the runtime tree already holds %s; the carrier is the bundle's, not the archive's", entry.Name)
	}
	sum := sha256.Sum256(entry.Bytes)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != entry.Digest {
		return fmt.Errorf("the carrier's bytes do not match the bundle's verified digest")
	}
	return os.WriteFile(target, entry.Bytes, 0o700)
}

// .
// .
// .
// .
// .
func VerifyRuntimeRoot(root string, d *RuntimeDecl, entry entrypointSpec, limits packagefmt.TreeLimits) error {
	rec, err := readRecord(root)
	if err != nil {
		return fmt.Errorf("root record: %w", err)
	}
	if rec.VariantID != d.VariantID || rec.InventorySHA256 != d.InventorySHA256 || rec.Entrypoint != entry.Name || rec.EntrypointSHA256 != entry.Digest {
		return fmt.Errorf("the root record names another release (variant %s, carrier %s)", rec.VariantID, rec.Entrypoint)
	}
	if got := packagefmt.InventoryDigest(rec.Inventory); got != "sha256:"+d.InventorySHA256 {
		return fmt.Errorf("the inventory record digests %s, the declaration pins sha256:%s", got, d.InventorySHA256)
	}
	inv, err := packagefmt.ParseInventory(rec.Inventory, limits)
	if err != nil {
		return fmt.Errorf("inventory record: %w", err)
	}
	expected := map[string]packagefmt.InventoryEntry{}
	for _, e := range inv.Files {
		expected[e.Path] = e
	}
	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, de fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if de.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a link", rel)
		}
		if de.IsDir() {
			return nil
		}
		if !de.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		if rel == entry.Name {
			if _, clash := expected[rel]; clash {
				return fmt.Errorf("%s is both the carrier and an inventory file", rel)
			}
			sum, _, err := hashFile(path)
			if err != nil {
				return err
			}
			if "sha256:"+sum != entry.Digest {
				return fmt.Errorf("the carrier at %s does not match the bundle's digest", rel)
			}
			seen[rel] = true
			return nil
		}
		e, ok := expected[rel]
		if !ok {
			return fmt.Errorf("%s is not in the inventory", rel)
		}
		if info.Size() != e.Size {
			return fmt.Errorf("%s is %d bytes, the inventory says %d", rel, info.Size(), e.Size)
		}
		if exec := info.Mode().Perm()&0o100 != 0; exec != (e.Mode == "exec") {
			return fmt.Errorf("%s has the wrong mode", rel)
		}
		sum, _, err := hashFile(path)
		if err != nil {
			return err
		}
		if "sha256:"+sum != e.SHA256 {
			return fmt.Errorf("%s does not match the inventory's digest", rel)
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	if !seen[entry.Name] {
		return fmt.Errorf("the carrier %s is missing", entry.Name)
	}
	for path := range expected {
		if !seen[path] {
			return fmt.Errorf("%s is missing", path)
		}
	}
	return nil
}

// .
// .
// .
// .
func runtimeSpawnCheck(root string, d *RuntimeDecl, entry entrypointSpec) func() error {
	return func() error {
		rec, err := readRecord(root)
		if err != nil {
			return fmt.Errorf("runtime root: record: %w", err)
		}
		if got := packagefmt.InventoryDigest(rec.Inventory); got != "sha256:"+d.InventorySHA256 {
			return fmt.Errorf("runtime root: the inventory record changed")
		}
		var inv packagefmt.Inventory
		if err := json.Unmarshal(rec.Inventory, &inv); err != nil {
			return fmt.Errorf("runtime root: inventory record: %w", err)
		}
		for _, e := range inv.Files {
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(e.Path)))
			if err != nil {
				return fmt.Errorf("runtime root: %s: %w", e.Path, err)
			}
			if !info.Mode().IsRegular() || info.Size() != e.Size || (info.Mode().Perm()&0o100 != 0) != (e.Mode == "exec") {
				return fmt.Errorf("runtime root: %s changed", e.Path)
			}
		}
		sum, _, err := hashFile(filepath.Join(root, entry.Name))
		if err != nil {
			return fmt.Errorf("runtime root: carrier: %w", err)
		}
		if "sha256:"+sum != entry.Digest {
			return fmt.Errorf("runtime root: the carrier's bytes changed")
		}
		return nil
	}
}
