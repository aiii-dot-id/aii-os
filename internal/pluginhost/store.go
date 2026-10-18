package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

type StoreWork struct {
	Entry string    `json:"entry"`
	Root  *RootWork `json:"root,omitempty"`
	View  *ViewWork `json:"view,omitempty"`
}

type RootWork struct {
	Runtime RuntimeDecl           `json:"runtime"`
	Package string                `json:"package"`
	Member  string                `json:"member"`
	Name    string                `json:"name"`
	Digest  string                `json:"digest"`
	Limits  packagefmt.TreeLimits `json:"limits"`
}

type ViewWork struct {
	ModelsSHA256 string      `json:"models_sha256"`
	Names        []string    `json:"names"`
	Models       []ModelDecl `json:"models"`
}

type Adoption struct {
	ModelDirs   []string `json:"model_dirs,omitempty"`
	ArchiveDirs []string `json:"archive_dirs,omitempty"`
	Stores      []string `json:"stores,omitempty"`
}

type MaterialWait struct {
	Cause    string
	Remedy   string
	Fetching bool
	After    time.Duration
}

func (e *MaterialWait) Error() string {
	if e.Remedy != "" {
		return e.Cause + " — " + e.Remedy
	}
	return e.Cause
}

type MaterialEnded struct {
	Cause  string
	Remedy string
}

func (e *MaterialEnded) Error() string { return e.Cause }

func (e *MaterialEnded) Final() string { return e.Remedy }

func (e *MaterialEnded) Momentary() error { return &MaterialWait{Cause: e.Cause} }

type MaterialRefused struct {
	Cause  string
	Remedy string
}

func (e *MaterialRefused) Error() string {
	if e.Remedy != "" {
		return e.Cause + " — " + e.Remedy
	}
	return e.Cause
}

func (e *MaterialRefused) WaitsOnPackage() bool { return true }

type MaterialDamageError struct {
	PluginID string
	Entry    string
	Detail   string
	Cause    error
}

func (e *MaterialDamageError) Error() string {
	return fmt.Sprintf("plugin %s: the plugin store's %s is damaged (%s) — refused and left as it is; the machine service repairs it once no activation holds it", e.PluginID, e.Entry, e.Detail)
}

func (e *MaterialDamageError) Unwrap() error { return e.Cause }

var errNotInStore = errors.New("not in the plugin store yet")

func storeEntryAt(store, path string) (plugstore.Entry, bool) {
	if store == "" {
		return plugstore.Entry{}, false
	}
	rel, err := filepath.Rel(store, path)
	if err != nil {
		return plugstore.Entry{}, false
	}
	kind, id, ok := strings.Cut(filepath.ToSlash(rel), "/")
	if !ok || (kind != plugstore.DirRoots && kind != plugstore.DirViews) || strings.Contains(id, "/") {
		return plugstore.Entry{}, false
	}
	e, err := plugstore.ParseEntry(kind + "/" + id)
	return e, err == nil
}

func isDir(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir()
}

func (m Material) modelStatuses(decls []ModelDecl) []ModelStatus {
	s := plugstore.Store{Dir: m.store}
	out := make([]ModelStatus, 0, len(decls))
	for _, d := range decls {
		st := ModelStatus{Name: d.Name, Path: d.Dest(), Size: d.Size}
		if m.ModelsDir != "" {
			if fi, err := os.Stat(modelFile(m.ModelsDir, d)); err == nil && fi.Mode().IsRegular() && fi.Size() == d.Size {
				st.Present = true
			}
		}
		if blob, err := s.Blob(d.SHA256); err == nil && m.store != "" {
			if fi, err := os.Stat(blob); err == nil && fi.Mode().IsRegular() && fi.Size() == d.Size {
				st.Stored = true
			} else if fi, err := os.Stat(blob + plugstore.PartialSuffix); err == nil && fi.Mode().IsRegular() {
				st.Partial = fi.Size()
			}
		}
		out = append(out, st)
	}
	return out
}

type heldEntries struct{ view, root *plugstore.Pin }

func (h heldEntries) release() {
	h.view.Close()
	h.root.Close()
}

type verifiedEntries struct{ view, root os.FileInfo }

func (h heldEntries) identities(m Material) (verifiedEntries, error) {
	var v verifiedEntries
	var err error
	if h.view != nil {
		if v.view, err = h.view.Stat(); err != nil {
			return verifiedEntries{}, &MaterialDamageError{PluginID: m.PluginID, Entry: "v/" + m.viewKey(), Detail: "its record does not read", Cause: err}
		}
	}
	if h.root != nil {
		if v.root, err = h.root.Stat(); err != nil {
			return verifiedEntries{}, &MaterialDamageError{PluginID: m.PluginID, Entry: "r/" + m.runtimeKey(), Detail: "its record does not read", Cause: err}
		}
	}
	return v, nil
}

var ErrMaterialReplaced = errors.New("the plugin store's entry changed after it was verified")

func sameRecord(seen, now os.FileInfo) bool {
	return os.SameFile(seen, now) && seen.ModTime().Equal(now.ModTime()) && seen.Size() == now.Size()
}

func (v verifiedEntries) pin(m Material) (heldEntries, error) {
	var h heldEntries
	store := plugstore.Store{Dir: m.store}
	again := func(seen os.FileInfo, entry string, pin func(string) (*plugstore.Pin, error), key string) (*plugstore.Pin, error) {
		if seen == nil {
			return nil, nil
		}
		p, err := pin(key)
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %s: %w: %w", m.PluginID, entry, ErrMaterialReplaced, err)
		}
		now, err := p.Stat()
		if err != nil || !sameRecord(seen, now) {
			p.Close()
			return nil, fmt.Errorf("plugin %s: %s: %w", m.PluginID, entry, ErrMaterialReplaced)
		}
		return p, nil
	}
	var err error
	if h.view, err = again(v.view, "v/"+m.viewKey(), store.PinView, m.viewKey()); err != nil {
		return heldEntries{}, err
	}
	if h.root, err = again(v.root, "r/"+m.runtimeKey(), store.PinRoot, m.runtimeKey()); err != nil {
		h.release()
		return heldEntries{}, err
	}
	return h, nil
}

func (m Material) verify(ctx context.Context) (root string, held heldEntries, err error) {
	defer func() {
		if err != nil {
			held.release()
			held = heldEntries{}
		}
	}()
	if m.view != nil {
		if held.view, err = m.verifyView(ctx); err != nil {
			return "", held, err
		}
	}
	if m.Runtime != nil {
		if held.root, err = m.verifyRoot(ctx); err != nil {
			return "", held, err
		}
		root = m.runtimeRoot()
	}
	return root, held, nil
}

func pinOrAbsent(pin func(string) (*plugstore.Pin, error), key string) (*plugstore.Pin, error) {
	p, err := pin(key)
	switch {
	case err == nil:
		return p, nil
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, plugstore.ErrCollecting), errors.Is(err, plugstore.ErrReplaced):
		return nil, fmt.Errorf("%w: %w", errNotInStore, err)
	}
	return nil, err
}

func (m Material) verifyView(ctx context.Context) (*plugstore.Pin, error) {
	key := m.viewKey()
	entry := "v/" + key
	pin, err := pinOrAbsent(plugstore.Store{Dir: m.store}.PinView, key)
	if err != nil {
		if errors.Is(err, errNotInStore) {
			return nil, &ModelsMissingError{PluginID: m.PluginID, Missing: modelNames(m.Models), Cause: err}
		}
		return nil, err
	}
	var missing []string
	for _, d := range m.Models {
		if err := ctx.Err(); err != nil {
			return pin, err
		}
		if err := noLinks(m.ModelsDir, d); err != nil {
			return pin, &MaterialDamageError{PluginID: m.PluginID, Entry: entry, Detail: err.Error()}
		}
		path := modelFile(m.ModelsDir, d)
		fi, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, d.Name)
			continue
		}
		if err != nil {
			return pin, err
		}
		if !fi.Mode().IsRegular() || fi.Size() != d.Size {
			return pin, &MaterialDamageError{PluginID: m.PluginID, Entry: entry, Detail: fmt.Sprintf("model %s is %d bytes, declared %d", d.Name, fi.Size(), d.Size)}
		}
		sum, _, err := hashFileContext(ctx, path)
		if err != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				return pin, cancelled
			}
			return pin, err
		}
		if sum != d.SHA256 {
			return pin, &MaterialDamageError{PluginID: m.PluginID, Entry: entry, Detail: fmt.Sprintf("model %s does not hash to its declared digest", d.Name)}
		}
	}
	if len(missing) > 0 {
		return pin, &ModelsMissingError{PluginID: m.PluginID, Missing: missing, Cause: errNotInStore}
	}
	return pin, nil
}

func (m Material) verifyRoot(ctx context.Context) (*plugstore.Pin, error) {
	key := m.runtimeKey()
	pin, err := pinOrAbsent(plugstore.Store{Dir: m.store}.PinRoot, key)
	if err != nil {
		if errors.Is(err, errNotInStore) {
			return nil, &RuntimeMissingError{PluginID: m.PluginID, Cause: err}
		}
		return nil, err
	}
	root := m.runtimeRoot()
	if !isDir(root) {

		return pin, &RuntimeMissingError{PluginID: m.PluginID, Cause: errNotInStore}
	}
	raw, rerr := pin.Read()
	inv, verr := verifyStoreRoot(ctx, root, raw, rerr, m.Runtime, m.entry, m.limits)
	if verr != nil {
		if cancelled := ctx.Err(); cancelled != nil {
			return pin, cancelled
		}
		if ownBound(verr, m.limits) {
			return pin, &RuntimeError{PluginID: m.PluginID, Detail: "the installed runtime's inventory holds more than the declaration says", Cause: verr, Declared: true}
		}
		var damaged *rootDamageError
		if errors.As(verr, &damaged) {
			return pin, &MaterialDamageError{PluginID: m.PluginID, Entry: "r/" + key, Detail: verr.Error(), Cause: verr}
		}
		return pin, &RuntimeError{PluginID: m.PluginID, Detail: ceilingNamed("the published runtime does not verify", verr), Cause: verr}
	}

	if detail := declarationMismatch(m.Runtime, inv); detail != "" {
		return pin, &RuntimeError{PluginID: m.PluginID, Detail: detail, Declared: true}
	}
	return pin, nil
}

func verifyStoreRoot(ctx context.Context, root string, raw []byte, readErr error, d *RuntimeDecl, entry entrypointSpec, limits packagefmt.TreeLimits) (*packagefmt.Inventory, error) {
	if readErr != nil {
		return nil, damage("root record: %w", readErr)
	}
	rec, err := plugstore.DecodeRootRecord(root+plugstore.RecordSuffix, raw)
	if err != nil {
		return nil, err
	}
	return verifyRootTree(ctx, root, rec, d, entry, limits)
}

func (o *Options) adoptionFor(id string) Adoption {
	var a Adoption
	if dir := absOrEmpty(o.AdoptModelsDir); dir != "" {
		a.ModelDirs = []string{pluginStorageDir(dir, id), filepath.Join(dir, sanitizeToken(id))}
	}
	if dir := absOrEmpty(o.AdoptRuntimeDir); dir != "" {
		a.ArchiveDirs = []string{filepath.Join(pluginStorageDir(dir, id), archivesDir), filepath.Join(dir, sanitizeToken(id), archivesDir)}
	}
	return a
}

func absOrEmpty(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return abs
}
