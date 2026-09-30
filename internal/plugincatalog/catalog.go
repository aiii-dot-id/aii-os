package plugincatalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const (
	maxIndexBytes = 4 << 20
	maxSigBytes   = 64 << 10
	refreshEvery  = time.Hour
)

type Catalog struct {
	dir       string
	cache     string
	root      *sigenvelope.PublicKeyEnvelope
	url       func() string
	poke      chan struct{}
	fetch     func(ctx context.Context, url string, max int64) ([]byte, error)
	adopted   func()
	contended func()

	keep sync.Mutex

	mu      sync.RWMutex
	held    *pluginhost.Catalog
	at      string
	refusal string
	unkept  string
}

type KeepError struct {
	Dir string
	Err error
}

func (e *KeepError) Error() string {
	return fmt.Sprintf("the verified catalog is in use but could not be kept at %s for a boot without a network: %v", e.Dir, e.Err)
}

func (e *KeepError) Unwrap() error { return e.Err }

func (c *Catalog) Open(dir, cache string, root *sigenvelope.PublicKeyEnvelope, url func() string) {
	c.dir, c.cache, c.root, c.url = dir, cache, root, url
	c.poke = make(chan struct{}, 1)
	if c.dir != "" {
		cat, err := pluginhost.LoadCatalog(c.dir, root)
		if err != nil {
			c.refused(fmt.Errorf("catalog at %s unavailable: %w", c.dir, err))
			return
		}
		c.adopt(cat, "")
		logsink.Info("catalog.start", "catalog loaded from %s — %d plugin(s) available", c.dir, len(cat.Plugins))
		return
	}

	if c.cache == "" {
		return
	}
	cat, err := pluginhost.LoadCatalog(c.cache, root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || !cacheAbsent(c.cache) {

			c.refused(fmt.Errorf("the kept catalog at %s is unusable, so none is held until a refresh verifies: %w", c.cache, err))
		}
		return
	}
	c.adopt(cat, "")
	logsink.Info("catalog.start", "catalog loaded from the last verified fetch — %d plugin(s); the URL is refreshed when online", len(cat.Plugins))
}

func cacheAbsent(dir string) bool {

	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return false
	}
	for _, name := range []string{pluginhost.CatalogFile, pluginhost.CatalogSigFile} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}

func (c *Catalog) adopt(cat *pluginhost.Catalog, fetchedAt string) {
	c.mu.Lock()
	c.held = cat
	if fetchedAt != "" {
		c.at = fetchedAt
	}
	c.refusal = ""
	c.mu.Unlock()
}

func (c *Catalog) Refresh(ctx context.Context) error {
	if c.root == nil {
		return c.refused(errors.New("no platform_release root is pinned — a fetched catalog cannot be verified"))
	}
	url := c.url()
	fetch := c.fetch
	if fetch == nil {
		fetch = fetchSmall
	}
	md, err := fetch(ctx, url, maxIndexBytes)
	if err != nil {
		return c.refused(fmt.Errorf("fetching the index: %w", err))
	}
	sig, err := fetch(ctx, url+".sig", maxSigBytes)
	if err != nil {
		return c.refused(fmt.Errorf("fetching the index signature: %w", err))
	}
	cat, err := pluginhost.ParseCatalog(md, sig, c.root)
	if err != nil {
		return c.refused(err)
	}
	c.lockKeep()
	c.adopt(cat, time.Now().UTC().Format(time.RFC3339))
	if c.adopted != nil {
		c.adopted()
	}
	var kept *KeepError
	if c.cache != "" {
		if err := writeCache(c.cache, md, sig); err != nil {
			kept = &KeepError{Dir: c.cache, Err: err}
		}
	}
	c.mu.Lock()
	c.unkept = ""
	if kept != nil {
		c.unkept = kept.Error()
	}
	c.mu.Unlock()
	c.keep.Unlock()
	logsink.Info("catalog.end", "catalog refreshed from %s — %d plugin(s) available", url, len(cat.Plugins))
	if kept != nil {
		logsink.Warn("catalog.error", "%v", kept)
		return kept
	}
	return nil
}

func (c *Catalog) lockKeep() {
	if c.keep.TryLock() {
		return
	}
	if c.contended != nil {
		c.contended()
	}
	c.keep.Lock()
}

func (c *Catalog) refused(err error) error {
	c.mu.Lock()
	c.refusal = err.Error()
	c.mu.Unlock()
	logsink.Warn("catalog.error", "catalog: %v (any previously verified index remains in use)", err)
	return err
}

func writeCache(dir string, md, sig []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{pluginhost.CatalogFile, md}, {pluginhost.CatalogSigFile, sig}} {
		if err := keepFile(dir, f.name, f.data); err != nil {
			return fmt.Errorf("keep %s: %w", f.name, err)
		}
	}
	return nil
}

func keepFile(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "."+name+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	published, err := atomicfile.Replace(tmpPath, filepath.Join(dir, name))
	if err != nil && !published {
		os.Remove(tmpPath)
		return err
	}
	if err != nil {

		return fmt.Errorf("published but not synced: %w", err)
	}
	return nil
}

func fetchSmall(ctx context.Context, url string, max int64) ([]byte, error) {
	if err := tools.FetchGuard(ctx, url); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (plugin catalog)")
	client := tools.GuardedClient(2*time.Minute, nil, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the catalog host answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("%s exceeds the %d-byte bound", url, max)
	}
	return body, nil
}

func (c *Catalog) Run(ctx context.Context, changed func()) {
	first := time.NewTimer(15 * time.Second)
	defer first.Stop()
	tick := time.NewTicker(refreshEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-tick.C:
		case <-c.poke:
		}
		rctx, cancel := context.WithTimeout(ctx, 3*time.Minute)

		_ = c.Refresh(rctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		changed()
	}
}

func (c *Catalog) Poke() {
	if c.poke == nil {
		return
	}
	select {
	case c.poke <- struct{}{}:
	default:
	}
}

func (c *Catalog) State() (fetchedAt, problem string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	switch {
	case c.refusal != "" && c.unkept != "":
		return c.at, c.refusal + "; and " + c.unkept
	case c.refusal != "":
		return c.at, c.refusal
	default:
		return c.at, c.unkept
	}
}

func (c *Catalog) Current() *pluginhost.Catalog {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.held
}
