package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

const RuntimesFile = "runtime.json"

const MaxRuntimes = 8

var reHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

type RuntimeDecl struct {
	VariantID       string `json:"variant_id"`
	URL             string `json:"url"`
	SHA256          string `json:"sha256"`
	Size            int64  `json:"size"`
	InstalledBytes  int64  `json:"installed_bytes"`
	Files           int    `json:"files"`
	InventorySHA256 string `json:"inventory_sha256"`

	LargestFileBytes *int64 `json:"largest_file_bytes,omitempty"`
	Depth            *int   `json:"depth,omitempty"`
}

type RuntimeError struct {
	PluginID string
	Detail   string
	Cause    error
	Declared bool
}

func (e *RuntimeError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("plugin %s: runtime: %s: %v", e.PluginID, e.Detail, e.Cause)
	}
	return fmt.Sprintf("plugin %s: runtime: %s", e.PluginID, e.Detail)
}

func (e *RuntimeError) Unwrap() error        { return e.Cause }
func (e *RuntimeError) WaitsOnPackage() bool { return e.Declared }

type RuntimeMissingError struct {
	PluginID string
	Cause    error
}

func (e *RuntimeMissingError) Unwrap() error { return e.Cause }

func (e *RuntimeMissingError) Error() string {
	return fmt.Sprintf("plugin %s: its runtime tree is not installed and cannot be fetched now (%v) — the activation refuses rather than run without it", e.PluginID, e.Cause)
}

func ParseRuntimes(raw []byte) ([]RuntimeDecl, error) {
	var doc struct {
		Runtimes []RuntimeDecl `json:"runtimes"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}

	var members struct {
		Runtimes []map[string]json.RawMessage `json:"runtimes"`
	}
	if err := json.Unmarshal(raw, &members); err != nil {
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
		if err := checkRuntimeExtent(d, members.Runtimes[i]); err != nil {
			return nil, fmt.Errorf("runtime %d: %w", i, err)
		}
	}
	return doc.Runtimes, nil
}

func checkRuntimeExtent(d RuntimeDecl, members map[string]json.RawMessage) error {
	for name, value := range members {
		if (strings.EqualFold(name, "largest_file_bytes") || strings.EqualFold(name, "depth")) && string(bytes.TrimSpace(value)) == "null" {
			return fmt.Errorf("%s is null: declare the extent or leave out both members", name)
		}
	}
	switch {
	case d.LargestFileBytes == nil && d.Depth == nil:
		return nil
	case d.LargestFileBytes == nil || d.Depth == nil:
		return errors.New("largest_file_bytes and depth are declared together or not at all")
	}
	mean := d.InstalledBytes / int64(d.Files)
	if d.InstalledBytes%int64(d.Files) != 0 {
		mean++
	}
	if largest := *d.LargestFileBytes; largest < mean || largest > d.InstalledBytes {
		return fmt.Errorf("largest_file_bytes %d is outside %d (the mean file) to %d (the whole tree)", largest, mean, d.InstalledBytes)
	}
	if depth := *d.Depth; depth < 1 || depth > packagefmt.MaxRuntimeDepth {
		return fmt.Errorf("depth %d is outside 1 to %d", depth, packagefmt.MaxRuntimeDepth)
	}
	return nil
}

func runtimeExtentFloor(decls []RuntimeDecl, minHost string) error {
	for _, d := range decls {
		if d.LargestFileBytes == nil {
			continue
		}
		if !packagefmt.ValidHostBound(minHost) || packagefmt.CompareHostBounds(minHost, packagefmt.RuntimeExtentMinHost) < 0 {
			return fmt.Errorf("runtime %s declares its extent, which requires aiios_min_version >= %s", d.VariantID, packagefmt.RuntimeExtentMinHost)
		}
	}
	return nil
}

func loadRuntime(pkgPath string, res *packagefmt.Result, held map[string][]byte, m *packagefmt.Manifest, variantID string) (*RuntimeDecl, error) {
	decls, err := loadRuntimes(pkgPath, res, held, m)
	if err != nil {
		return nil, err
	}
	for i := range decls {
		if decls[i].VariantID == variantID {
			return &decls[i], nil
		}
	}
	return nil, nil
}

func loadRuntimes(pkgPath string, res *packagefmt.Result, held map[string][]byte, m *packagefmt.Manifest) ([]RuntimeDecl, error) {
	if _, present := res.FileDigests[RuntimesFile]; !present {
		return nil, nil
	}
	raw, err := loadVerifiedMember(pkgPath, res, held, RuntimesFile)
	if err != nil {
		return nil, err
	}
	decls, err := ParseRuntimes(raw)
	if err == nil {
		err = runtimeExtentFloor(decls, m.AiiosMinVersion)
	}
	if err != nil {
		return nil, &RuntimeError{PluginID: m.ID, Detail: "runtime declaration", Cause: err, Declared: true}
	}
	return decls, nil
}

type RuntimeRoots struct {
	mu   sync.Mutex
	refs map[string]int
}

func NewRuntimeRoots() *RuntimeRoots { return &RuntimeRoots{refs: map[string]int{}} }

func (r *RuntimeRoots) Pin(root string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refs[root]++
}

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

func (r *RuntimeRoots) Refs(root string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refs[root]
}

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

type entrypointSpec struct {
	Name   string
	Bytes  []byte
	Digest string
}

func RuntimeRootKey(variantID, inventorySHA256, entryName, entryDigest string) string {
	sum := sha256.Sum256([]byte("aii-runtime-root\n" + variantID + "\n" + inventorySHA256 + "\n" + entryName + "\n" + entryDigest + "\n"))
	return hex.EncodeToString(sum[:])
}

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

var ensureLocks sync.Map

func lockRoot(ctx context.Context, root string) (func(), error) {
	v, _ := ensureLocks.LoadOrStore(root, make(chan struct{}, 1))
	gate := v.(chan struct{})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return nil, err
		}
		return func() { <-gate }, nil
	}
}

func EnsureRuntime(ctx context.Context, pluginID string, d *RuntimeDecl, dir string, fetch ModelFetcher, limits packagefmt.TreeLimits, entry entrypointSpec, logf func(string, ...interface{})) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if entry.Name == "" || strings.ContainsAny(entry.Name, "/\\") {
		return "", &RuntimeError{PluginID: pluginID, Detail: fmt.Sprintf("entrypoint name %q is not one file name", entry.Name), Declared: true}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("pluginhost: runtime dir: %w", err)
	}
	key := RuntimeRootKey(d.VariantID, d.InventorySHA256, entry.Name, entry.Digest)
	root := filepath.Join(dir, key)
	unlock, err := lockRoot(ctx, root)
	if err != nil {
		return "", err
	}
	defer unlock()
	if _, err := os.Stat(root); err == nil {
		inv, verr := verifyRuntimeRoot(ctx, root, d, entry, limits)
		if verr != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				return "", cancelled
			}
			return "", &RuntimeError{PluginID: pluginID, Detail: "the installed runtime does not verify", Cause: verr}
		}

		if detail := declarationMismatch(d, inv); detail != "" {
			return "", &RuntimeError{PluginID: pluginID, Detail: detail, Declared: true}
		}
		return root, nil
	}

	if over := runtimeDeclarationRefusals(*d, limits); len(over) > 0 {
		return "", &RuntimeError{PluginID: pluginID, Detail: "the declaration exceeds the operator's ceilings: " + strings.Join(ceilingWords(over), ", ")}
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
	rep, verified, xerr := extractRuntimeArchive(ctx, f, d, limits, partial)
	closeErr := f.Close()
	if cancelled := ctx.Err(); cancelled != nil {

		return "", errors.Join(cancelled, os.RemoveAll(partial))
	}
	if xerr != nil || closeErr != nil {
		abandon()
		declared := verified && closeErr == nil && runtimeContentError(xerr)

		if declared {
			_ = os.Remove(archive)
		}
		return "", &RuntimeError{PluginID: pluginID, Detail: "the runtime archive was refused", Cause: errors.Join(xerr, closeErr), Declared: declared}
	}
	if rep.Files != d.Files || rep.InstalledBytes != d.InstalledBytes {
		abandon()
		_ = os.Remove(archive)
		return "", &RuntimeError{PluginID: pluginID, Detail: fmt.Sprintf("the archive holds %d files / %d bytes, the declaration says %d / %d", rep.Files, rep.InstalledBytes, d.Files, d.InstalledBytes), Declared: true}
	}
	inv, perr := packagefmt.ParseInventory(rep.InventoryRaw, limits)
	if perr != nil {
		abandon()

		return "", &RuntimeError{PluginID: pluginID, Detail: "inventory", Cause: perr}
	}
	if detail := extentMismatch(d, inv); detail != "" {
		abandon()
		_ = os.Remove(archive)
		return "", &RuntimeError{PluginID: pluginID, Detail: detail, Declared: true}
	}
	if err := placeEntrypoint(pluginID, partial, inv, entry); err != nil {
		abandon()
		return "", err
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
	if err := ctx.Err(); err != nil {
		return "", errors.Join(err, os.RemoveAll(partial), os.Remove(recordPath(root)))
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

func ensureArchive(ctx context.Context, pluginID string, d *RuntimeDecl, dir string, fetch ModelFetcher, logf func(string, ...interface{})) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	adir := filepath.Join(dir, archivesDir)
	if err := os.MkdirAll(adir, 0o700); err != nil {
		return "", fmt.Errorf("pluginhost: runtime archives: %w", err)
	}
	path := filepath.Join(adir, d.SHA256+archiveSuffix)
	if st, err := os.Stat(path); err == nil {
		if sum, n, herr := hashFileContext(ctx, path); herr == nil && n == st.Size() && n == d.Size && sum == d.SHA256 {
			return path, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}

		_ = os.Remove(path)
	}

	partial := path + ".partial"
	if err := fetchVerified(ctx, pluginID, "runtime archive", d.URL, partial, d.Size, d.SHA256, fetch, logf); err != nil {
		if cancelled := ctx.Err(); cancelled != nil {
			return "", cancelled
		}
		return "", &RuntimeMissingError{PluginID: pluginID, Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Rename(partial, path); err != nil {
		return "", err
	}
	return path, nil
}

func extentMismatch(d *RuntimeDecl, inv *packagefmt.Inventory) string {
	if d.LargestFileBytes == nil || d.Depth == nil {
		return ""
	}
	var largest int64
	depth := 0
	for _, f := range inv.Files {
		largest = max(largest, f.Size)
		depth = max(depth, strings.Count(f.Path, "/")+1)
	}
	if largest == *d.LargestFileBytes && depth == *d.Depth {
		return ""
	}
	return fmt.Sprintf("the declaration's extent (largest %d, depth %d) is not its inventory's (largest %d, depth %d)", *d.LargestFileBytes, *d.Depth, largest, depth)
}

func declarationMismatch(d *RuntimeDecl, inv *packagefmt.Inventory) string {
	if len(inv.Files) != d.Files || inv.InstalledBytes != d.InstalledBytes {
		return fmt.Sprintf("the inventory holds %d files / %d bytes, the declaration says %d / %d", len(inv.Files), inv.InstalledBytes, d.Files, d.InstalledBytes)
	}
	return extentMismatch(d, inv)
}

func placeEntrypoint(pluginID, root string, inv *packagefmt.Inventory, entry entrypointSpec) error {
	for _, e := range inv.Files {
		if e.Path == entry.Name || strings.HasPrefix(e.Path, entry.Name+"/") {
			return &RuntimeError{PluginID: pluginID, Detail: fmt.Sprintf("the runtime tree already holds %s; the carrier is the bundle's, not the archive's", e.Path), Declared: true}
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

func verifyRuntimeRoot(ctx context.Context, root string, d *RuntimeDecl, entry entrypointSpec, limits packagefmt.TreeLimits) (*packagefmt.Inventory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rec, err := readRecord(root)
	if err != nil {
		return nil, fmt.Errorf("root record: %w", err)
	}
	if rec.VariantID != d.VariantID || rec.InventorySHA256 != d.InventorySHA256 || rec.Entrypoint != entry.Name || rec.EntrypointSHA256 != entry.Digest {
		return nil, fmt.Errorf("the root record names another release (variant %s, carrier %s)", rec.VariantID, rec.Entrypoint)
	}
	if got := packagefmt.InventoryDigest(rec.Inventory); got != "sha256:"+d.InventorySHA256 {
		return nil, fmt.Errorf("the inventory record digests %s, the declaration pins sha256:%s", got, d.InventorySHA256)
	}
	inv, err := packagefmt.ParseInventory(rec.Inventory, limits)
	if err != nil {
		return nil, fmt.Errorf("inventory record: %w", err)
	}
	expected := map[string]packagefmt.InventoryEntry{}
	for _, e := range inv.Files {
		expected[e.Path] = e
	}
	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, de fs.DirEntry, werr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
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
			sum, _, err := hashFileContext(ctx, path)
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
		sum, _, err := hashFileContext(ctx, path)
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
		return nil, err
	}
	if !seen[entry.Name] {
		return nil, fmt.Errorf("the carrier %s is missing", entry.Name)
	}
	for path := range expected {
		if !seen[path] {
			return nil, fmt.Errorf("%s is missing", path)
		}
	}
	return inv, nil
}

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
