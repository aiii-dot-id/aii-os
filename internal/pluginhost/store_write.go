//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

type WorkError struct {
	Entry  string
	Detail string
}

func (e *WorkError) Error() string {
	return fmt.Sprintf("the store cannot bring %s as asked: %s", e.Entry, e.Detail)
}

func (e *WorkError) WaitsOnPackage() bool { return true }

func (w StoreWork) Check() (plugstore.Entry, error) {
	e, err := plugstore.ParseEntry(w.Entry)
	if err != nil {
		return plugstore.Entry{}, &WorkError{Entry: w.Entry, Detail: err.Error()}
	}
	refuse := func(format string, args ...any) (plugstore.Entry, error) {
		return plugstore.Entry{}, &WorkError{Entry: w.Entry, Detail: fmt.Sprintf(format, args...)}
	}
	switch e.Kind {
	case plugstore.DirRoots:
		r := w.Root
		if r == nil || w.View != nil {
			return refuse("a root's work names its runtime")
		}
		raw, _ := json.Marshal(map[string]any{"runtimes": []RuntimeDecl{r.Runtime}})
		if _, err := ParseRuntimes(raw); err != nil {
			return refuse("its runtime declaration: %v", err)
		}
		if r.Name == "" || strings.ContainsAny(r.Name, `/\`) || r.Name == "." || r.Name == ".." {
			return refuse("its carrier's name %q is not one file name", r.Name)
		}
		if !strings.HasPrefix(r.Digest, "sha256:") || !plugstore.ValidDigest(strings.TrimPrefix(r.Digest, "sha256:")) {
			return refuse("its carrier's digest %q is not sha256:<64 hex>", r.Digest)
		}
		if !filepath.IsAbs(r.Package) || r.Member == "" {
			return refuse("its carrier is not named by an absolute package path and a member")
		}
		if key := plugstore.RootKey(r.Runtime.VariantID, r.Runtime.InventorySHA256, r.Name, r.Digest); key != e.ID {
			return refuse("its contents make the root %s", key)
		}
	case plugstore.DirViews:
		v := w.View
		if v == nil || w.Root != nil {
			return refuse("a view's work names its models")
		}
		raw, _ := json.Marshal(v.Models)
		if _, err := ParseModels(raw); err != nil {
			return refuse("its models: %v", err)
		}
		if !strings.HasPrefix(v.ModelsSHA256, "sha256:") || !plugstore.ValidDigest(strings.TrimPrefix(v.ModelsSHA256, "sha256:")) {
			return refuse("its declaring member's digest %q is not sha256:<64 hex>", v.ModelsSHA256)
		}
		if key := plugstore.ViewKey(v.ModelsSHA256, v.Names); key != e.ID {
			return refuse("its contents make the view %s", key)
		}
	case plugstore.DirArchives:
		if w.Root != nil || w.View != nil {
			return refuse("an archive is kept by reference; its root's work fetches it")
		}
	default:
		return refuse("a blob is brought by the view that links it")
	}
	return e, nil
}

func EnsureEntry(ctx context.Context, w *plugstore.Writer, work StoreWork, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e, err := work.Check()
	if err != nil {
		return err
	}
	switch e.Kind {
	case plugstore.DirRoots:
		return ensureStoreRoot(ctx, w, e.ID, *work.Root, fetch, adopt, logf)
	case plugstore.DirViews:
		return ensureStoreView(ctx, w, e.ID, *work.View, fetch, adopt, logf)
	}
	return nil
}

func EntryPresent(w *plugstore.Writer, work StoreWork) bool {
	e, err := work.Check()
	if err != nil {
		return false
	}
	switch e.Kind {
	case plugstore.DirRoots:
		root, _ := w.Root(e.ID)
		rec, _ := w.RootRecord(e.ID)
		return isDir(root) && isRegular(rec)
	case plugstore.DirViews:
		view, _ := w.View(e.ID)
		rec, _ := w.ViewRecord(e.ID)
		if !isRegular(rec) {
			return false
		}
		for _, d := range work.View.Models {
			if fi, err := os.Lstat(modelFile(view, d)); err != nil || !fi.Mode().IsRegular() || fi.Size() != d.Size {
				return false
			}
		}
		return true
	case plugstore.DirArchives:
		path, _ := w.Archive(e.ID)
		return isRegular(path)
	}
	return false
}

func ensureStoreView(ctx context.Context, w *plugstore.Writer, key string, v ViewWork, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) error {
	view, err := w.View(key)
	if err != nil {
		return err
	}
	record, _ := w.ViewRecord(key)
	if err := os.MkdirAll(view, 0o700); err != nil {
		return fmt.Errorf("the plugin store: view %s: %w", key, err)
	}
	var missing []string
	var cause error
	for _, d := range v.Models {
		if err := ctx.Err(); err != nil {
			return err
		}
		blob, err := ensureStoreBlob(ctx, w, d, fetch, adopt, logf)
		if err != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				return cancelled
			}
			missing = append(missing, d.Name)
			if errors.Is(err, errNoDownloadPath) {
				continue
			}
			if !transientDownload(err) {
				return &ModelsMissingError{PluginID: "view " + key, Missing: missing, Cause: err}
			}
			if cause == nil {
				cause = err
			}
			continue
		}

		if err := plugstore.GrantViewFile(blob, key); err != nil {
			return fmt.Errorf("the plugin store: view %s: %w", key, err)
		}
		if err := linkInto(key, view, d, blob); err != nil {
			return err
		}
	}
	if len(missing) > 0 {
		return &ModelsMissingError{PluginID: "view " + key, Missing: missing, Cause: cause}
	}

	if err := plugstore.GrantView(view, key); err != nil {
		return fmt.Errorf("the plugin store: view %s: %w", key, err)
	}
	if isRegular(record) {
		return nil
	}
	names := append([]string(nil), v.Names...)
	raw, err := json.Marshal(plugstore.ViewRecord{ModelsSHA256: v.ModelsSHA256, Names: names, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	if err := writeStoreRecord(record, raw); err != nil {
		return fmt.Errorf("the plugin store: record view %s: %w", key, err)
	}
	sayTo(logf, "plugin store: view %s published — %d models", key, len(v.Models))
	return nil
}

func linkInto(key, view string, d ModelDecl, blob string) error {
	target := modelFile(view, d)
	if err := noLinks(view, d); err != nil {
		return fmt.Errorf("the plugin store: view %s: %w", key, err)
	}
	have, herr := os.Lstat(target)
	want, werr := os.Stat(blob)
	if werr != nil {
		return fmt.Errorf("the plugin store: blob %s: %w", d.SHA256, werr)
	}
	if herr == nil && os.SameFile(have, want) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("the plugin store: view %s: %w", key, err)
	}
	tmp := target + plugstore.LinkSuffix + randomSuffix()
	if err := os.Link(blob, tmp); err != nil {
		return fmt.Errorf("the plugin store: link %s into view %s: %w", d.Dest(), key, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("the plugin store: link %s into view %s: %w", d.Dest(), key, err)
	}
	return nil
}

func ensureStoreBlob(ctx context.Context, w *plugstore.Writer, d ModelDecl, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) (string, error) {
	blob, err := w.Blob(d.SHA256)
	if err != nil {
		return "", err
	}
	release, err := ownDestination(ctx, blob)
	if err != nil {
		return "", err
	}
	defer release()
	if fi, err := os.Lstat(blob); err == nil && fi.Mode().IsRegular() && fi.Size() == d.Size {
		return blob, nil
	}
	partial := blob + plugstore.PartialSuffix
	var candidates []string
	for _, dir := range adopt.ModelDirs {
		candidates = append(candidates, modelFile(dir, d))
	}
	for _, s := range adopt.Stores {
		if p, err := (plugstore.Store{Dir: s}).Blob(d.SHA256); err == nil {
			candidates = append(candidates, p)
		}
	}
	if adoptInto(ctx, candidates, partial, d.Size, d.SHA256, "model "+d.Name, logf) {
		return blob, publishFile(partial, blob)
	}
	if err := fetchVerified(ctx, "store", "model "+d.Name, d.URL, partial, d.Size, d.SHA256, fetch, logf); err != nil {
		return "", err
	}
	return blob, publishFile(partial, blob)
}

func publishFile(partial, final string) error {
	if err := os.Chmod(partial, 0o400); err != nil {
		return err
	}
	return os.Rename(partial, final)
}

func adoptInto(ctx context.Context, candidates []string, partial string, size int64, sha, what string, logf func(string, ...any)) bool {
	for _, src := range candidates {
		if ctx.Err() != nil {
			return false
		}
		fi, err := os.Lstat(src)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() != size {
			continue
		}
		if err := removeIfPresent(partial); err != nil {
			return false
		}
		sum, how, err := copyOrClone(ctx, src, partial, size)
		if err == nil && sum == sha {
			sayTo(logf, "plugin store: %s adopted from %s (%s, %d bytes, its digest held)", what, src, how, size)
			return true
		}
		_ = os.Remove(partial)
		if err == nil {
			sayTo(logf, "plugin store: %s at %s does not hash to its declared digest — not adopted", what, src)
		}
	}
	return false
}

func copyOrClone(ctx context.Context, src, dst string, size int64) (string, string, error) {
	if err := cloneFile(src, dst); err == nil {
		sum, n, err := hashFileContext(ctx, dst)
		if err == nil && n != size {
			err = fmt.Errorf("the clone holds %d bytes, not %d", n, size)
		}

		if err == nil {
			err = syncDownload(dst)
		}
		return sum, "cloned", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), acquisitionReader{ctx, io.LimitReader(in, size)})
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != size {
		err = fmt.Errorf("copied %d bytes, not %d", n, size)
	}
	return hexSum(h), "copied", err
}

func hexSum(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

func ensureStoreArchive(ctx context.Context, w *plugstore.Writer, d *RuntimeDecl, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) (string, error) {
	path, err := w.Archive(d.SHA256)
	if err != nil {
		return "", err
	}
	release, err := ownDestination(ctx, path)
	if err != nil {
		return "", err
	}
	defer release()
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() && fi.Size() == d.Size {
		return path, nil
	}
	partial := path + plugstore.PartialSuffix
	var candidates []string
	for _, dir := range adopt.ArchiveDirs {
		candidates = append(candidates, filepath.Join(dir, d.SHA256+archiveSuffix))
	}
	for _, s := range adopt.Stores {
		if p, err := (plugstore.Store{Dir: s}).Archive(d.SHA256); err == nil {
			candidates = append(candidates, p)
		}
	}
	if adoptInto(ctx, candidates, partial, d.Size, d.SHA256, "runtime archive", logf) {
		return path, publishFile(partial, path)
	}
	if err := fetchVerified(ctx, "store", "runtime archive", d.URL, partial, d.Size, d.SHA256, fetch, logf); err != nil {
		if cancelled := ctx.Err(); cancelled != nil {
			return "", cancelled
		}
		return "", &RuntimeMissingError{PluginID: "store", Cause: err}
	}
	return path, publishFile(partial, path)
}

func ensureStoreRoot(ctx context.Context, w *plugstore.Writer, key string, r RootWork, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) error {
	d := &r.Runtime
	root, _ := w.Root(key)
	record, _ := w.RootRecord(key)
	unlock, err := lockRoot(ctx, root)
	if err != nil {
		return err
	}
	defer unlock()
	if isDir(root) && isRegular(record) {
		return nil
	}
	if over := runtimeDeclarationRefusals(*d, r.Limits); len(over) > 0 {
		return &RuntimeError{PluginID: "root " + key, Detail: "the declaration exceeds the operator's ceilings: " + strings.Join(ceilingWords(over), ", ")}
	}
	carrier, err := packagefmt.ReadMember(r.Package, r.Member)
	if err != nil {
		return &RuntimeMissingError{PluginID: "root " + key, Cause: fmt.Errorf("its carrier %s in %s does not read: %w", r.Member, r.Package, err)}
	}
	entry := entrypointSpec{Name: r.Name, Bytes: carrier, Digest: r.Digest}
	archive, partial, rep, err := extractStoreArchive(ctx, w, key, r, fetch, adopt, logf)
	if errors.Is(err, errArchiveDamaged) {

		sayTo(logf, "plugin store: the cached runtime archive for root %s was no longer the declared bytes and was removed; bringing it again", key)
		archive, partial, rep, err = extractStoreArchive(ctx, w, key, r, fetch, adopt, logf)
	}
	if err != nil {
		return err
	}
	abandon := func() { _ = os.RemoveAll(partial) }
	inv, perr := packagefmt.ParseInventory(rep.InventoryRaw, runtimeLimits(*d, r.Limits))
	if perr != nil {
		abandon()
		return &RuntimeError{PluginID: "root " + key, Detail: ceilingNamed("inventory", perr), Cause: perr}
	}
	if detail := extentMismatch(d, inv); detail != "" {
		abandon()
		_ = os.Remove(archive)
		return &RuntimeError{PluginID: "root " + key, Detail: detail, Declared: true}
	}
	if err := placeEntrypoint("root "+key, partial, inv, entry); err != nil {
		abandon()
		return err
	}
	raw, err := json.Marshal(plugstore.RootRecord{VariantID: d.VariantID, InventorySHA256: d.InventorySHA256, ArchiveSHA256: d.SHA256,
		Entrypoint: entry.Name, EntrypointSHA256: entry.Digest, Files: rep.Files, InstalledBytes: rep.InstalledBytes,
		Inventory: rep.InventoryRaw, InstalledAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		abandon()
		return err
	}
	if err := writeStoreRecord(record, raw); err != nil {
		abandon()
		_ = os.Remove(record)
		return fmt.Errorf("the plugin store: record root %s: %w", key, err)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, os.RemoveAll(partial), os.Remove(record))
	}

	if err := plugstore.GrantRootAt(partial, key); err != nil {
		abandon()
		_ = os.Remove(record)
		return fmt.Errorf("the plugin store: root %s: %w", key, err)
	}
	if err := os.Rename(partial, root); err != nil {
		abandon()
		_ = os.Remove(record)
		return fmt.Errorf("the plugin store: publish root %s: %w", key, err)
	}
	sayTo(logf, "plugin store: root %s published — %d files, %d bytes, verified against the pinned inventory", key, rep.Files, rep.InstalledBytes)
	return nil
}

func extractStoreArchive(ctx context.Context, w *plugstore.Writer, key string, r RootWork, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) (archive, partial string, rep *packagefmt.TreeReport, err error) {
	d := &r.Runtime
	archive, err = ensureStoreArchive(ctx, w, d, fetch, adopt, logf)
	if err != nil {
		return "", "", nil, err
	}
	root, _ := w.Root(key)
	partial, err = os.MkdirTemp(filepath.Dir(root), key+".partial-")
	if err != nil {
		return "", "", nil, fmt.Errorf("the plugin store: root %s: %w", key, err)
	}
	abandon := func() { _ = os.RemoveAll(partial) }
	f, err := os.Open(archive)
	if err != nil {
		abandon()
		return "", "", nil, err
	}
	rep, verified, xerr := extractRuntimeArchive(ctx, f, d, r.Limits, partial)
	closeErr := f.Close()
	if cancelled := ctx.Err(); cancelled != nil {
		return "", "", nil, errors.Join(cancelled, os.RemoveAll(partial))
	}
	if xerr != nil || closeErr != nil {
		abandon()
		if closeErr == nil && errors.Is(xerr, errArchiveDamaged) {
			_ = os.Remove(archive)
			return "", "", nil, &RuntimeMissingError{PluginID: "root " + key, Cause: xerr}
		}
		own := verified && closeErr == nil && ownBound(xerr, r.Limits)
		declared := verified && closeErr == nil && (runtimeContentError(xerr) || own)
		if declared {
			_ = os.Remove(archive)
		}
		detail := ceilingNamed("the runtime archive was refused", xerr)
		if own {
			detail = "the runtime archive holds more than its declaration says"
		}
		return "", "", nil, &RuntimeError{PluginID: "root " + key, Detail: detail, Cause: errors.Join(xerr, closeErr), Declared: declared}
	}
	if rep.Files != d.Files || rep.InstalledBytes != d.InstalledBytes {
		abandon()
		_ = os.Remove(archive)
		return "", "", nil, &RuntimeError{PluginID: "root " + key, Detail: fmt.Sprintf("the archive holds %d files / %d bytes, the declaration says %d / %d", rep.Files, rep.InstalledBytes, d.Files, d.InstalledBytes), Declared: true}
	}
	return archive, partial, rep, nil
}

var ErrRepairWaits = errors.New("an activation holds what the repair would replace; it is repaired once none does")

func RepairEntry(ctx context.Context, w *plugstore.Writer, work StoreWork, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) error {
	e, err := work.Check()
	if err != nil {
		return err
	}
	switch e.Kind {
	case plugstore.DirViews:
		return repairView(ctx, w, e.ID, *work.View, fetch, adopt, logf)
	case plugstore.DirRoots:
		return repairRoot(ctx, w, e.ID, *work.Root, fetch, adopt, logf)
	}
	return nil
}

func repairView(ctx context.Context, w *plugstore.Writer, key string, v ViewWork, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) error {
	view, _ := w.View(key)
	held, err := w.HoldExclusive(plugstore.Entry{Kind: plugstore.DirViews, ID: key})
	if err != nil {
		return repairHeld(err)
	}
	defer held.Close()
	for _, d := range v.Models {
		path := modelFile(view, d)
		sum, n, err := hashFileContext(ctx, path)
		if err == nil && sum == d.SHA256 && n == d.Size {
			continue
		}
		if cancelled := ctx.Err(); cancelled != nil {
			return cancelled
		}
		if err := replaceBlob(ctx, w, key, path, d, fetch, adopt, logf); err != nil {
			return err
		}
	}
	return nil
}

func replaceBlob(ctx context.Context, w *plugstore.Writer, key, damaged string, d ModelDecl, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) error {
	blob, err := w.Blob(d.SHA256)
	if err != nil {
		return err
	}
	release, err := ownDestination(ctx, blob)
	if err != nil {
		return err
	}
	defer release()
	old, err := os.Lstat(damaged)
	if errors.Is(err, fs.ErrNotExist) {
		old, err = os.Lstat(blob)
	}
	var links []string
	if err == nil {
		others, err := linksOf(w, old, key)
		if err != nil {
			return err
		}
		for _, o := range others {
			h, err := w.HoldExclusive(plugstore.Entry{Kind: plugstore.DirViews, ID: o.view})
			if err != nil {
				return repairHeld(err)
			}
			defer h.Close()
			links = append(links, o.path)
		}
		if b, err := os.Lstat(blob); err == nil && os.SameFile(b, old) {
			links = append(links, blob)
		}
	}
	fresh := blob + ".repair-" + randomSuffix()
	defer os.Remove(fresh)
	if !adoptInto(ctx, adoptCandidates(adopt, d), fresh, d.Size, d.SHA256, "model "+d.Name, logf) {
		if err := fetchVerified(ctx, "store", "model "+d.Name, d.URL, fresh, d.Size, d.SHA256, fetch, logf); err != nil {
			return err
		}
	}
	if err := os.Chmod(fresh, 0o400); err != nil {
		return err
	}

	grantsFrom := damaged
	if _, err := os.Lstat(grantsFrom); err != nil {
		grantsFrom = blob
	}
	if err := plugstore.CopyCapabilities(grantsFrom, fresh); err != nil {
		return fmt.Errorf("the plugin store: model %s's grants: %w", d.Name, err)
	}
	links = append(links, damaged)
	seen := map[string]bool{}
	for _, path := range links {
		if seen[path] {
			continue
		}
		seen[path] = true
		if path == blob {
			continue
		}
		tmp := path + plugstore.LinkSuffix + randomSuffix()
		if err := os.Link(fresh, tmp); err != nil {
			return fmt.Errorf("the plugin store: relink %s: %w", path, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("the plugin store: relink %s: %w", path, plugstore.Busy(err))
		}
	}
	if err := os.Rename(fresh, blob); err != nil {
		return fmt.Errorf("the plugin store: replace blob %s: %w", d.SHA256, plugstore.Busy(err))
	}
	sayTo(logf, "plugin store: model %s was damaged in view %s — fetched again and relinked in %d place(s)", d.Name, key, len(seen))
	return nil
}

func adoptCandidates(adopt Adoption, d ModelDecl) []string {
	var out []string
	for _, dir := range adopt.ModelDirs {
		out = append(out, modelFile(dir, d))
	}
	return out
}

type viewLink struct{ view, path string }

func linksOf(w *plugstore.Writer, fi os.FileInfo, except string) ([]viewLink, error) {
	views, err := os.ReadDir(filepath.Join(w.Dir, plugstore.DirViews))
	if err != nil {
		return nil, err
	}
	var out []viewLink
	for _, v := range views {
		if !v.IsDir() || !plugstore.ValidKey(v.Name()) || v.Name() == except {
			continue
		}
		dir := filepath.Join(w.Dir, plugstore.DirViews, v.Name())
		err := filepath.WalkDir(dir, func(path string, de fs.DirEntry, err error) error {
			if err != nil || !de.Type().IsRegular() {
				return nil
			}
			if lf, err := os.Lstat(path); err == nil && os.SameFile(lf, fi) {
				out = append(out, viewLink{view: v.Name(), path: path})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func repairRoot(ctx context.Context, w *plugstore.Writer, key string, r RootWork, fetch ModelFetcher, adopt Adoption, logf func(string, ...any)) error {
	root, _ := w.Root(key)
	held, err := w.HoldExclusive(plugstore.Entry{Kind: plugstore.DirRoots, ID: key})
	if err != nil {
		return repairHeld(err)
	}
	carrier, err := packagefmt.ReadMember(r.Package, r.Member)
	if err != nil {
		held.Close()
		return &RuntimeMissingError{PluginID: "root " + key, Cause: fmt.Errorf("its carrier %s in %s does not read: %w", r.Member, r.Package, err)}
	}
	entry := entrypointSpec{Name: r.Name, Bytes: carrier, Digest: r.Digest}
	raw, rerr := held.Read()
	_, verr := verifyStoreRoot(ctx, root, raw, rerr, &r.Runtime, entry, r.Limits)
	if verr == nil {
		held.Close()
		return nil
	}
	if cancelled := ctx.Err(); cancelled != nil {
		held.Close()
		return cancelled
	}
	var damaged *rootDamageError
	if !errors.As(verr, &damaged) {
		held.Close()
		return &RuntimeError{PluginID: "root " + key, Detail: ceilingNamed("the published runtime does not verify, and it is not damage a rebuild mends", verr), Cause: verr}
	}
	if err := held.Discard(); err != nil {
		return repairHeld(err)
	}
	sayTo(logf, "plugin store: root %s does not verify (%v) and no activation holds it — moved aside, and built again from its archive", key, verr)
	return ensureStoreRoot(ctx, w, key, r, fetch, adopt, logf)
}

func repairHeld(err error) error {
	if errors.Is(err, plugstore.ErrPinned) {
		return fmt.Errorf("%w: %w", ErrRepairWaits, err)
	}
	return plugstore.Busy(err)
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeStoreRecord(path string, raw []byte) error {
	published, err := atomicfile.WriteReplace(path, append(raw, '\n'), 0o600)
	if err != nil && published {
		return fmt.Errorf("published but not durable: %w", err)
	}
	return err
}

func isRegular(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}
