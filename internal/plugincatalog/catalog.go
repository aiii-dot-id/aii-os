package plugincatalog

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

const (
	DefaultMaxIndexBytes int64 = 4 << 20
	MinMaxIndexBytes     int64 = 64 << 10
	MaxMaxIndexBytes     int64 = 64 << 20

	DefaultMaxSignatureBytes int64 = 64 << 10
	MinMaxSignatureBytes     int64 = 56 << 10
	MaxMaxSignatureBytes     int64 = 1 << 20

	DefaultFetchTimeoutSeconds = 120
	MinFetchTimeoutSeconds     = 5
	MaxFetchTimeoutSeconds     = 3600

	DefaultRefreshTimeoutSeconds = 180
	MinRefreshTimeoutSeconds     = 10
	MaxRefreshTimeoutSeconds     = 3600

	DefaultFirstRefreshSeconds = 15
	MinFirstRefreshSeconds     = 1
	MaxFirstRefreshSeconds     = 3600
)

type Limits struct {
	MaxIndexBytes, MaxSignatureBytes           int64
	FetchTimeout, RefreshTimeout, FirstRefresh time.Duration
}

type LimitsConfig struct {
	MaxIndexBytes, MaxSignatureBytes                                int64
	FetchTimeoutSeconds, RefreshTimeoutSeconds, FirstRefreshSeconds int
}

func (l LimitsConfig) Held() Limits {
	seconds := func(n, def, lo, hi int) time.Duration {
		return time.Duration(held(int64(n), int64(def), int64(lo), int64(hi))) * time.Second
	}
	return Limits{
		MaxIndexBytes:     held(l.MaxIndexBytes, DefaultMaxIndexBytes, MinMaxIndexBytes, MaxMaxIndexBytes),
		MaxSignatureBytes: held(l.MaxSignatureBytes, DefaultMaxSignatureBytes, MinMaxSignatureBytes, MaxMaxSignatureBytes),
		FetchTimeout:      seconds(l.FetchTimeoutSeconds, DefaultFetchTimeoutSeconds, MinFetchTimeoutSeconds, MaxFetchTimeoutSeconds),
		RefreshTimeout:    seconds(l.RefreshTimeoutSeconds, DefaultRefreshTimeoutSeconds, MinRefreshTimeoutSeconds, MaxRefreshTimeoutSeconds),
		FirstRefresh:      seconds(l.FirstRefreshSeconds, DefaultFirstRefreshSeconds, MinFirstRefreshSeconds, MaxFirstRefreshSeconds),
	}
}

func held(n, def, lo, hi int64) int64 {
	switch {
	case n == 0:
		return def
	case n < lo:
		return lo
	case n > hi:
		return hi
	}
	return n
}

const (
	DefaultRefreshMinutes = 60
	MinRefreshMinutes     = 15
	MaxRefreshMinutes     = 1440
)

func HeldRefreshMinutes(n int) int {
	switch {
	case n == 0:
		return DefaultRefreshMinutes
	case n < MinRefreshMinutes:
		return MinRefreshMinutes
	case n > MaxRefreshMinutes:
		return MaxRefreshMinutes
	}
	return n
}

type Catalog struct {
	dir       string
	cache     string
	line      string
	root      *sigenvelope.PublicKeyEnvelope
	url       func() string
	poke      chan struct{}
	fetch     func(ctx context.Context, url string, max int64) ([]byte, error)
	adopted   func()
	contended func()
	limits    func() LimitsConfig

	keep sync.Mutex

	mu      sync.RWMutex
	held    *Index
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

type OlderIndexError struct {
	Held, Fetched string
	Kept          string
	Checkout      string
}

func (e *OlderIndexError) Error() string {
	fetched := e.Fetched
	if fetched == "" {
		fetched = "at no time it states"
	}
	msg := fmt.Sprintf("the fetched index was generated %s, before the index held (%s): a verified index is never replaced by an older one", fetched, e.Held)

	if e.Kept != "" {
		msg += fmt.Sprintf(". If the catalog URL was pointed at an older copy on purpose, remove the kept index at %s and restart the identity", e.Kept)
	}
	if e.Checkout != "" {
		msg += fmt.Sprintf("; the checkout at %s (plugins.catalog_dir) is what a start loads", e.Checkout)
	}
	return msg
}

func olderThanHeld(held, fetched *Index) bool {
	if held == nil {
		return false
	}
	return before(fetched.Generated, held.Generated) || before(fetched.Announced.At, held.Announced.At)
}

func before(fetched, held string) bool {
	h, err := time.Parse(time.RFC3339, held)
	if err != nil {
		return false
	}
	f, err := time.Parse(time.RFC3339, fetched)
	return err != nil || f.Before(h)
}

func (c *Catalog) Open(dir, cache string, root *sigenvelope.PublicKeyEnvelope, url func() string) {
	c.dir, c.cache, c.root, c.url = dir, cache, root, url
	if c.line == "" {
		c.line = OwnLine()
	}
	c.poke = make(chan struct{}, 1)
	if c.dir != "" {
		cat, err := LoadCatalogForLine(c.dir, root, c.line)
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

	cat, err := LoadCatalogForLine(c.cache, root, c.line)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || !cacheAbsent(c.cache, c.line) {

			c.refused(fmt.Errorf("the kept catalog at %s is unusable, so none is held until a refresh verifies: %w", c.cache, err))
		}
		return
	}
	c.adopt(cat, "")
	logsink.Info("catalog.start", "catalog loaded from the last verified fetch — %d plugin(s); the URL is refreshed when online", len(cat.Plugins))
}

func cacheAbsent(dir, line string) bool {

	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return false
	}
	for _, name := range []string{CatalogFileFor(line), CatalogFileFor(line) + ".sig"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}

func (c *Catalog) adopt(cat *Index, fetchedAt string) {
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
	lim := c.Limits()
	md, err := c.get(ctx, url, lim.MaxIndexBytes)
	if err != nil {
		return c.refused(fmt.Errorf("fetching the index: %w", notPublished(err, c.line, url)))
	}
	sig, err := c.get(ctx, url+".sig", lim.MaxSignatureBytes)
	if err != nil {
		return c.refused(fmt.Errorf("fetching the index signature: %w", err))
	}
	cat, err := ParseCatalogForLine(md, sig, c.root, c.line)
	if err != nil {
		return c.refused(err)
	}
	c.lockKeep()

	if held := c.Current(); olderThanHeld(held, cat) {
		c.keep.Unlock()
		h, f := held.Generated, cat.Generated
		if !before(f, h) {
			h, f = held.Announced.At, cat.Announced.At
		}
		return c.refused(&OlderIndexError{Held: h, Fetched: f, Kept: c.cache, Checkout: c.dir})
	}
	c.adopt(cat, time.Now().UTC().Format(time.RFC3339))
	if c.adopted != nil {
		c.adopted()
	}
	var kept *KeepError
	if c.cache != "" {
		if err := writeCache(c.cache, c.line, md, sig); err != nil {
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

type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("the catalog host answered %d", e.Code) }

type NotPublishedError struct {
	Line, URL string
}

func (e *NotPublishedError) Error() string { return fmt.Sprintf(catalogNotPublished, e.Line, e.URL) }

const catalogNotPublished = "no catalog for AII OS %s is published at %s yet"

func notPublished(err error, line, url string) error {
	var status *StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		return &NotPublishedError{Line: lineOrLegacy(line), URL: url}
	}
	return err
}

func (c *Catalog) FetchLine(ctx context.Context, line string) (*Index, error) {
	if c.root == nil {
		return nil, errors.New("no platform_release root is pinned — a fetched catalog cannot be verified")
	}
	if c.dir != "" {
		cat, err := LoadCatalogForLine(c.dir, c.root, line)
		if errors.Is(err, os.ErrNotExist) {
			return nil, &NotPublishedError{Line: lineOrLegacy(line), URL: filepath.Join(c.dir, CatalogFileFor(line))}
		}
		return cat, err
	}
	if c.url == nil {
		return nil, errors.New("no catalog is configured")
	}
	url := LineURL(c.url(), line)
	lim := c.Limits()
	md, err := c.get(ctx, url, lim.MaxIndexBytes)
	if err != nil {
		return nil, notPublished(err, line, url)
	}
	sig, err := c.get(ctx, url+".sig", lim.MaxSignatureBytes)
	if err != nil {
		return nil, err
	}
	return ParseCatalogForLine(md, sig, c.root, line)
}

func (c *Catalog) Line() string {
	if c.line == "" {
		return OwnLine()
	}
	return c.line
}

func (c *Catalog) FetchBeside(ctx context.Context, name string, max int64) ([]byte, error) {
	if c.url == nil {
		return nil, errors.New("no catalog is configured, so nothing is published beside it")
	}
	return c.get(ctx, PublishedBeside(c.url(), name), max)
}

func (c *Catalog) SetLimits(limits func() LimitsConfig) { c.limits = limits }

func (c *Catalog) Limits() Limits {
	if c.limits == nil {
		return LimitsConfig{}.Held()
	}
	return c.limits().Held()
}

func (c *Catalog) get(ctx context.Context, url string, max int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Limits().FetchTimeout)
	defer cancel()
	fetch := c.fetch
	if fetch == nil {
		fetch = fetchSmall
	}
	return fetch(ctx, url, max)
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

func writeCache(dir, line string, md, sig []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{CatalogFileFor(line), md}, {CatalogFileFor(line) + ".sig", sig}} {
		if err := keepFile(dir, f.name, f.data); err != nil {
			return fmt.Errorf("keep %s: %w", f.name, err)
		}
	}
	return nil
}

func keepFile(dir, name string, data []byte) error {
	published, err := atomicfile.WriteReplace(filepath.Join(dir, name), data, 0o600)
	if err != nil && published {

		return fmt.Errorf("published but not synced: %w", err)
	}
	return err
}

func fetchSmall(ctx context.Context, url string, max int64) ([]byte, error) {
	if err := firewall.FetchGuard(ctx, url); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (plugin catalog)")
	client := firewall.GuardedClient(0, nil, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Code: resp.StatusCode}
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

func (c *Catalog) Run(ctx context.Context, changed func(error), refreshEvery func() time.Duration) {
	wait := c.Limits().FirstRefresh
	for {
		next := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			next.Stop()
			return
		case <-next.C:
		case <-c.poke:
			next.Stop()
		}
		wait = refreshEvery()
		rctx, cancel := context.WithTimeout(ctx, c.Limits().RefreshTimeout)

		err := c.Refresh(rctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		changed(err)
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

func (c *Catalog) Current() *Index {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.held
}
