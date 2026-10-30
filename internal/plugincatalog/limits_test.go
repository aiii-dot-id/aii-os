package plugincatalog

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTheCatalogFetchsBoundsAreItsSettings(t *testing.T) {
	if got := (LimitsConfig{}).Held(); got != (Limits{MaxIndexBytes: 4 << 20, MaxSignatureBytes: 64 << 10,
		FetchTimeout: 2 * time.Minute, RefreshTimeout: 3 * time.Minute, FirstRefresh: 15 * time.Second}) {
		t.Fatalf("with nothing set the bounds are %+v", got)
	}
	if got := (LimitsConfig{MaxIndexBytes: 1, MaxSignatureBytes: 1 << 40, FetchTimeoutSeconds: -3, RefreshTimeoutSeconds: 1 << 30, FirstRefreshSeconds: 1}).Held(); got != (Limits{
		MaxIndexBytes: MinMaxIndexBytes, MaxSignatureBytes: MaxMaxSignatureBytes, FetchTimeout: MinFetchTimeoutSeconds * time.Second,
		RefreshTimeout: MaxRefreshTimeoutSeconds * time.Second, FirstRefresh: time.Second}) {
		t.Fatalf("values outside the bounds are held to %+v", got)
	}

	plat := platformRole(t)
	var mu sync.Mutex
	set := LimitsConfig{MaxIndexBytes: 100 << 10, MaxSignatureBytes: 70 << 10, FetchTimeoutSeconds: 3600, RefreshTimeoutSeconds: 10, FirstRefreshSeconds: 1}
	type ask struct {
		url  string
		max  int64
		left time.Duration
	}
	asked := make(chan ask, 16)
	c := &Catalog{fetch: func(ctx context.Context, u string, max int64) ([]byte, error) {
		dl, _ := ctx.Deadline()
		asked <- ask{u, max, time.Until(dl)}
		return nil, errors.New("fixture fetch unavailable")
	}}
	c.SetLimits(func() LimitsConfig { mu.Lock(); defer mu.Unlock(); return set })
	c.Open("", "", plat.Env, func() string { return "https://catalog.example.test/aiios-plugins.md" })

	// The first refresh after a start, at the second the setting gives
	// (the default would be fifteen), its index read to 100 KiB within the
	// ten seconds a refresh is given (one fetch is given an hour here).
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx, func(error) {}, func() time.Duration { return time.Hour }) }()
	select {
	case a := <-asked:
		if a.max != 100<<10 || a.left > 10*time.Second || a.left < 8*time.Second {
			t.Fatalf("the index was asked bounded at %d bytes with %v left, want 102400 and the refresh's ten seconds", a.max, a.left)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first refresh did not come at the second the setting gives")
	}
	cancel()
	<-done

	// Changed, the next fetch takes the new bounds: one fetch is given the
	// seven seconds set, and the signature is read to 70 KiB.
	served := map[string][]byte{"https://catalog.example.test/aiios-plugins.md": []byte("index")}
	c.fetch = func(ctx context.Context, u string, max int64) ([]byte, error) {
		dl, _ := ctx.Deadline()
		asked <- ask{u, max, time.Until(dl)}
		if b, ok := served[u]; ok {
			return b, nil
		}
		return nil, errors.New("fixture fetch unavailable")
	}
	mu.Lock()
	set.FetchTimeoutSeconds = 7
	mu.Unlock()
	_ = c.Refresh(context.Background())
	if a := <-asked; a.left > 7*time.Second || a.left < 5*time.Second {
		t.Fatalf("one fetch was given %v, want the seven seconds set", a.left)
	}
	if a := <-asked; a.url != "https://catalog.example.test/aiios-plugins.md.sig" || a.max != 70<<10 {
		t.Fatalf("the signature was asked %+v, want 71680 bytes", a)
	}
}
