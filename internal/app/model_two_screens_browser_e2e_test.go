//go:build !windows

package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TWO SCREENS, ONE MODEL. On the real page, both open on the model in force. The first asks for
// another and is told it answered. The second has not heard, asks for the same one, and the host
// asks nothing: the second is told it saved, not that the model answered.
func TestASecondScreenAskingForTheSameModelIsNotToldItAnswered(t *testing.T) {
	chrome := needChrome(t)
	srv, asked := answeringProvider(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Screens")
	cfg := safebootConfig(t, dir, "Screens", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.LLM = withTestProvider(t, dir, "prov", srv.URL, "m-one", "sk-test-key-1234")
	t.Chdir(dir)
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })

	screens := []*cdp{openInChrome(t, chrome, a.dashboard.Origin(), "/#/settings/model"), openInChrome(t, chrome, a.dashboard.Origin(), "/#/settings/model")}
	for i, page := range screens {
		got, err := page.evalRetry(awaitJS(`
    const m = document.getElementById('cfg-model');
    last = m ? m.value : 'no field';
    return m && m.value === 'm-one' && document.getElementById('cfg-provider').value === 'prov' ? 'ready' : null;
  `, 20000), true, 30*time.Second, 5)
		if err != nil || got != "ready" {
			t.Fatalf("screen %d: %q %v", i+1, got, err)
		}
	}
	asks := func(page *cdp) string {
		t.Helper()
		if _, err := page.eval(`document.getElementById('cfg-model').value = 'm-two'; document.getElementById('cfg-model').dispatchEvent(new Event('input', { bubbles: true })); document.querySelector('[data-save="llm"]').click(); 'pressed'`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		got, err := page.evalRetry(awaitJS(`
    const said = document.querySelector('[data-said="llm"]');
    last = said ? said.textContent : 'nothing said';
    return said ? said.textContent : null;
  `, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := asks(screens[0]); !strings.HasPrefix(got, "Active. m-two answered") {
		t.Fatalf("the first screen is told: %s", got)
	}
	before := asked.Load()
	if before == 0 {
		t.Fatal("the provider was never asked: the first screen's answer proves nothing")
	}
	// THE SECOND SCREEN IS MADE STALE ON PURPOSE. The host's broadcast of
	// the saved configuration reaches every screen on its own time: under a
	// gate's load the second screen had heard before this test looked,
	// and a precondition that depends on a race proves
	// nothing either way. So the test waits until the second screen HAS
	// heard (no broadcast is left to race the save), then sets its knowledge
	// back to the model it opened on, through the page's own shared state
	// module (one instance per document): it asks for the model in force
	// believing it changes something, which is the case the host answers.
	if _, err := screens[1].eval(`(async () => { window.__state = (await import('/state.js')).S; return 'bound'; })()`, true, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	heard, err := screens[1].evalRetry(awaitJS(`
    const c = window.__state && window.__state.config;
    last = c && c.llm ? c.llm.model : 'no config';
    return c && c.llm && c.llm.model === 'm-two' && document.getElementById('cfg-model').value === 'm-two' ? 'heard' : null;
  `, 20000), true, 30*time.Second, 5)
	if err != nil || heard != "heard" {
		t.Fatalf("the second screen never heard the first's change: %q %v", heard, err)
	}
	stale, err := screens[1].eval(`window.__state.config.llm.model = 'm-one'; document.getElementById('cfg-model').value = 'm-one'; document.getElementById('cfg-model').value`, false, 10*time.Second)
	if err != nil || stale != "m-one" {
		t.Fatalf("the second screen could not be made stale (%q %v)", stale, err)
	}
	if got := asks(screens[1]); got != "Saved." {
		t.Errorf("the second screen is told: %s", got)
	}
	if n := asked.Load() - before; n != 0 {
		t.Errorf("the second screen's save asked the provider %d times", n)
	}
}
