package app

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// THE RELEASE API IS HELD WHEN THE FILE IS READ (updates.api_url): one that
// is not https is refused by its key and its requirement, and a mirror's
// https address loads as written.
func TestTheLoaderHoldsTheReleaseAPIURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{"updates":{"api_url":"http://mirror.example:8443"}}`)
	if _, err := ReadConfig(path); err == nil || !strings.Contains(err.Error(), "updates.api_url") || !strings.Contains(err.Error(), "https") {
		t.Fatalf("a plaintext updates.api_url was not refused by its key and requirement: %v", err)
	}
	writeFile(t, path, `{"updates":{"api_url":"https://mirror.example:8443"}}`)
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("a mirror's https address was refused: %v", err)
	}
	if cfg.Updates.APIURL != "https://mirror.example:8443" {
		t.Fatalf("read %q", cfg.Updates.APIURL)
	}
}

// THE SETTING REACHES THE CHECKER THE BOOT WIRES, read at each request: on
// a booted identity whose updates.api_url names a mirror, the check asks
// the mirror. The context is cancelled, so nothing is sent; the URL it
// would have asked is what the failure records.
func TestTheBootsCheckerAsksTheConfiguredReleaseAPI(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	if app.updateChecker == nil {
		t.Fatal("rig: the live boot built no update checker")
	}
	app.cfgMu.Lock()
	app.cfg.Updates.APIURL = "https://mirror.example:8443"
	app.cfgMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app.updateChecker.Check(ctx)
	got := app.updateChecker.State().Snapshot(Current()).LastError
	if !strings.Contains(got, "https://mirror.example:8443/repos/") {
		t.Fatalf("the check did not ask the configured release API: %q", got)
	}
}

// ON A PHONE, A REFUSED RELEASE API IS NOT KEPT, AND IS SAID. The salvage
// of a config.json the loader refused keeps what still reads; a plaintext
// updates.api_url is not among it, so the inform-only check asks the
// default source, and the key is named among what is not in force.
func TestASalvageKeepsNoRefusedReleaseAPIAndSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := []byte(`{"updates":{"automatic":false,"api_url":"http://mirror.example:8443"}}`)
	cfg, lost, err := SalvageConfig(path, raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Updates.APIURL != "" {
		t.Fatalf("the salvage kept a refused api_url: %q", cfg.Updates.APIURL)
	}
	if !slices.Contains(lost, "updates.api_url") {
		t.Fatalf("the salvage did not name updates.api_url as lost: %v", lost)
	}
	if notKept, _ := notInForce(*cfg, raw); !slices.Contains(notKept, "updates.api_url") {
		t.Fatalf("the page would not name updates.api_url as not in force: %v", notKept)
	}
}
