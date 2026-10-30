package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheWebFetchCeilingIsASettingAppliedFromTheNextFetch(t *testing.T) {
	page := "<p>" + strings.Repeat("word ", 1000) + "</p><p>LAST-WORDS</p>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, page) }))
	defer srv.Close()
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Fetcher")
	cfg := safebootConfig(t, dir, "Fetcher", keyPath, ledgerPath, dbPath)
	cfg.Tools.LocalHosts = []string{strings.TrimPrefix(srv.URL, "http://")}
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	fetch := func() string {
		t.Helper()
		res, err := a.toolReg.ExecuteModel(context.Background(), "web_fetch", map[string]any{"url": srv.URL})
		if err != nil || res.Error != "" {
			t.Fatalf("web_fetch: %+v %v", res, err)
		}
		return res.Text()
	}
	if got := fetch(); !strings.Contains(got, "LAST-WORDS") || strings.Contains(got, "web_fetch reads") {
		t.Fatalf("at the default the page is read whole: %.200q", got)
	}
	if _, err := a.applyConfigChange(map[string]any{"tools.webfetch_max_bytes": float64(1000)}); err != nil {
		t.Fatal(err)
	}
	if got := fetch(); strings.Contains(got, "LAST-WORDS") || !strings.Contains(got, "[This page is longer than the 1000 bytes web_fetch reads, so its text past them cannot be read; the operator's setting tools.webfetch_max_bytes sets that size.]") {
		t.Fatalf("the next fetch reads 1000 bytes and says so: %.300q", got)
	}
	if _, err := a.applyConfigChange(map[string]any{"tools.webfetch_max_bytes": nil}); err != nil {
		t.Fatal(err)
	}
	if got := fetch(); !strings.Contains(got, "LAST-WORDS") {
		t.Fatalf("cleared, the default reads the page whole again: %.200q", got)
	}
	if _, err := a.applyConfigChange(map[string]any{"tools.webfetch_max_bytes": float64(0.5)}); err == nil {
		t.Fatal("the door took a ceiling that is no whole number")
	}
}
