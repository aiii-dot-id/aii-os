package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
)

var catalogBoundKeys = []string{"catalog_max_index_bytes", "catalog_max_signature_bytes", "catalog_fetch_timeout_seconds",
	"catalog_refresh_timeout_seconds", "catalog_first_refresh_seconds", "trust_list_max_bytes"}

func TestTheCatalogAndListBoundsAreSettingsNeverWrittenUnset(t *testing.T) {
	saved, err := json.Marshal(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range catalogBoundKeys {
		if strings.Contains(string(saved), `"`+key+`"`) {
			t.Errorf("plugins.%s is written with nothing set: 0.1.23 refuses the file", key)
		}
	}

	cfg, err := decodeConfig(filepath.Join(t.TempDir(), "config.json"), []byte(`{"plugins":{"catalog_max_index_bytes":100000,
		"catalog_max_signature_bytes":70000,"catalog_fetch_timeout_seconds":7,"catalog_refresh_timeout_seconds":11,
		"catalog_first_refresh_seconds":2,"trust_list_max_bytes":200000}}`))
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if got := a.catalogLimits().Held(); got != (plugincatalog.Limits{MaxIndexBytes: 100000, MaxSignatureBytes: 70000,
		FetchTimeout: 7 * time.Second, RefreshTimeout: 11 * time.Second, FirstRefresh: 2 * time.Second}) {
		t.Fatalf("the catalog fetch is given %+v", got)
	}
	for set, want := range map[int64]int64{0: 1 << 20, 1: 64 << 10, 1 << 40: 64 << 20, 200000: 200000} {
		a.cfg.Plugins.TrustListMaxBytes = set
		if got := a.statusListBytes(); got != want {
			t.Errorf("plugins.trust_list_max_bytes %d is held to %d, want %d", set, got, want)
		}
	}

	h := newListsHost(t, map[string]int64{"platform_release": 2})
	h.app.cfg.Plugins.TrustListMaxBytes = 200000
	var bound int64
	h.app.listFetch = func(_ context.Context, name string, max int64) ([]byte, error) {
		bound = max
		return nil, errors.New("fixture: not served")
	}
	_, _ = h.app.bringTrustLists(context.Background())
	if bound != 200000 {
		t.Fatalf("an announced list was fetched bounded at %d bytes, want the 200000 set", bound)
	}

	for _, edit := range []func(*Config){
		func(c *Config) { c.Plugins.CatalogMaxIndexBytes = 1 },
		func(c *Config) { c.Plugins.CatalogMaxSignatureBytes = 1 },
		func(c *Config) { c.Plugins.CatalogFetchTimeoutSeconds = 1 },
		func(c *Config) { c.Plugins.CatalogRefreshTimeoutSeconds = 1 },
		func(c *Config) { c.Plugins.CatalogFirstRefreshSeconds = 1 },
		func(c *Config) { c.Plugins.TrustListMaxBytes = 1 },
	} {
		x, y := *defaultConfig(), *defaultConfig()
		edit(&y)
		blankLiveAppliable(&x)
		blankLiveAppliable(&y)
		if !reflect.DeepEqual(x, y) {
			t.Errorf("a bound read at each fetch survives blankLiveAppliable, so a reload would call it saved for next boot")
		}
	}
}
