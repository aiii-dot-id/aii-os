package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
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
	if err := canonicaljson.DecodeStrict(raw, &doc); err != nil {
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

type entrypointSpec struct {
	Name   string
	Bytes  []byte
	Digest string
}

const (
	archivesDir   = "archives"
	archiveSuffix = ".tar.gz"
)

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

func ceilingNamed(detail string, cause error) string {
	var over *packagefmt.MemberAllowanceError
	if !errors.As(cause, &over) {
		return detail
	}
	return fmt.Sprintf("%s: its %d files and %d directories are more than the operator's files ceiling of %d admits; set %s to %d or more",
		detail, over.Files, over.Directories, over.MaxFiles, runtimeFilesKey, over.Least)
}

func declarationMismatch(d *RuntimeDecl, inv *packagefmt.Inventory) string {
	if len(inv.Files) != d.Files || inv.InstalledBytes != d.InstalledBytes {
		return fmt.Sprintf("the inventory holds %d files / %d bytes, the declaration says %d / %d", len(inv.Files), inv.InstalledBytes, d.Files, d.InstalledBytes)
	}
	return extentMismatch(d, inv)
}

func verifyRootTree(ctx context.Context, root string, rec plugstore.RootRecord, d *RuntimeDecl, entry entrypointSpec, limits packagefmt.TreeLimits) (*packagefmt.Inventory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limits = runtimeLimits(*d, limits)
	if rec.VariantID != d.VariantID || rec.InventorySHA256 != d.InventorySHA256 || rec.Entrypoint != entry.Name || rec.EntrypointSHA256 != entry.Digest {
		return nil, fmt.Errorf("the root record names another release (variant %s, carrier %s)", rec.VariantID, rec.Entrypoint)
	}
	if got := packagefmt.InventoryDigest(rec.Inventory); got != "sha256:"+d.InventorySHA256 {
		return nil, damage("the inventory record digests %s, the declaration pins sha256:%s", got, d.InventorySHA256)
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
			return damage("%s is a link", rel)
		}
		if de.IsDir() {
			return nil
		}
		if !de.Type().IsRegular() {
			return damage("%s is not a regular file", rel)
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
				return damage("the carrier at %s does not match the bundle's digest", rel)
			}
			seen[rel] = true
			return nil
		}
		e, ok := expected[rel]
		if !ok {
			return damage("%s is not in the inventory", rel)
		}
		if info.Size() != e.Size {
			return damage("%s is %d bytes, the inventory says %d", rel, info.Size(), e.Size)
		}
		if exec := info.Mode().Perm()&0o100 != 0; exec != (e.Mode == "exec") {

			platform := packagefmt.HostPlatform()
			if e.Mode == "exec" && platform == "windows" {
				return wrongMode(rel, e.Mode, platform)
			}
			return &rootDamageError{wrongMode(rel, e.Mode, platform)}
		}
		sum, _, err := hashFileContext(ctx, path)
		if err != nil {
			return err
		}
		if "sha256:"+sum != e.SHA256 {
			return damage("%s does not match the inventory's digest", rel)
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !seen[entry.Name] {
		return nil, damage("the carrier %s is missing", entry.Name)
	}
	for path := range expected {
		if !seen[path] {
			return nil, damage("%s is missing", path)
		}
	}
	return inv, nil
}

type rootDamageError struct{ err error }

func (e *rootDamageError) Error() string { return e.err.Error() }
func (e *rootDamageError) Unwrap() error { return e.err }

func damage(format string, args ...any) error {
	return &rootDamageError{fmt.Errorf(format, args...)}
}

func wrongMode(rel, mode, platform string) error {
	if mode == "exec" && platform == "windows" {
		return fmt.Errorf(`%s has the wrong mode: the runtime's inventory lists it as "exec", and Windows keeps no executable mark on a file, so that row can never match here; the package's author must list the file as "file" in this Windows runtime's inventory when packing it`, rel)
	}
	return fmt.Errorf("%s has the wrong mode", rel)
}

func runtimeSpawnCheck(pin *plugstore.Pin, root string, d *RuntimeDecl, entry entrypointSpec) func() error {
	return func() error {
		raw, err := pin.Read()
		if err != nil {
			return fmt.Errorf("runtime root: record: %w", err)
		}
		rec, err := plugstore.DecodeRootRecord(root+plugstore.RecordSuffix, raw)
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
