//go:build !windows

package app

import (
	"slices"
	"strconv"
	"testing"
	"time"
)

// THE FOLDERS AND THE TOOLS ARE ON PLUGINS AND PERMISSIONS (A10, 0.2.2): a
// born identity's own dashboard in a real Chrome, opened on Model. From
// Settings' list, by its row and not by an address, the operator reaches
// Plugins and permissions, marked as where they are, and on it the folders
// their tools may reach with the sandbox's switch, and the tools. What is
// pressed there reaches the host: a folder added and removed, and a tool
// switched off and on, each kept by the host and drawn from its answer.
// The old addresses of the two (#/settings/sandbox, #/settings/tools)
// land on the same page.
//
// The rig asks for no access token, so the host holds the sandbox on and
// the switch is drawn held, with the host's reason above it. Ticking it
// (the warning, Allow and Cancel) is proved on this page by the dashboard's
// fixture test (TestTheSandboxSwitchWarnsBeforeItAsksAndShowsOnlyWhatTheHostConfirmed).
//
// COVERAGE LIMIT, NAMED: Blink only, as the other journeys.
func TestTheFoldersAndToolsAreReachedFromPluginsAndPermissionsInARealBrowser(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Journey")
	a := New(safebootConfig(t, dir, "Journey", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if !a.toolReg.ToolEnabled("read") {
		t.Fatal("rig: the read tool is not on before the journey")
	}
	work := t.TempDir() // a folder beside their home, theirs to be granted

	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/settings/model")
	click := func(what, selector string) {
		t.Helper()
		// A control is clicked once it is there and can be pressed: the page
		// draws it after the step before, and a loaded host clicks first.
		awaitOnPage(t, page, what, `const el = document.querySelector('`+selector+`');
    last = el ? 'disabled=' + !!el.disabled : 'absent';
    return el && !el.disabled ? 'ok' : null;`)
		if _, err := page.eval(`(() => { document.querySelector('`+selector+`').click(); return 'clicked'; })()`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}

	// ---- From Settings' list, by its row.
	awaitOnPage(t, page, "Model's page, with Settings' list beside it", `
    const on = document.getElementById('view-settings');
    last = 'hash=' + location.hash + ' settings=' + !!(on && on.classList.contains('on'));
    return location.hash === '#/settings/model' && on && on.classList.contains('on') ? 'ok' : null;`)
	click("Plugins and permissions in Settings' list", `#place-tabs [data-item="permissions"]`)
	const there = `
    const row = document.querySelector('#place-tabs [data-item="permissions"]'), on = document.getElementById('view-plugins');
    const folders = document.querySelector('#plugins-stack [data-folders]'), box = folders && folders.querySelector('#sbx-off');
    const tools = document.querySelector('#plugins-stack [data-tools]'), read = tools && tools.querySelector('[data-tool="read"]');
    last = 'hash=' + location.hash + ' marked=' + (row && row.getAttribute('aria-current')) + ' page=' + !!(on && on.classList.contains('on')) +
      ' folders=' + !!folders + ' switch=' + !!box + ' tools=' + !!tools + ' read=' + !!read;
    if (!(location.hash === '#/settings/permissions' && row && row.getAttribute('aria-current') === 'page' && on && on.classList.contains('on') && box && read)) return null;
    if (document.querySelector('#settings-stack [data-folders], #settings-stack [data-tools]')) return 'FAIL the folders or the tools are still drawn on another Settings page';
    // As the host holds it: on, and held on with its reason, here.
    const held = folders.querySelector('[data-sandbox-held]');
    if (box.checked || !box.disabled || !held || !held.textContent.trim()) return 'FAIL the switch is not drawn as the host holds it: checked=' + box.checked + ' disabled=' + box.disabled + ' held=' + (held && held.textContent);
    return read.checked ? 'ok' : 'FAIL the read tool is drawn off';`
	if got := awaitOnPage(t, page, "the folders and the tools on Plugins and permissions", there); got != "ok" {
		t.Fatal(got)
	}

	// ---- A folder, added and removed from here, kept by the host.
	if _, err := page.eval(`(() => { document.getElementById('sbx-add').value = `+strconv.Quote(work)+`; return 'typed'; })()`, false, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	click("the folder's Add", "#plugins-stack #sbx-grant")
	waitUntil(t, func() bool { _, roots := a.toolReg.Roots(); return len(roots) == 1 }, "the host granted the folder")
	_, roots := a.toolReg.Roots()
	if got := awaitOnPage(t, page, "the granted folder in the list, with its Remove", `
    const rows = [...document.querySelectorAll('#plugins-stack [data-folders] li')];
    const row = rows.find(r => r.textContent.includes(`+strconv.Quote(roots[0])+`));
    last = rows.map(r => r.textContent).join(' | ');
    return row && row.querySelector('[data-rmroot="0"]') && document.getElementById('sbx-add').value === '' ? 'ok' : null;`); got != "ok" {
		t.Fatal(got)
	}
	click("the folder's Remove", `#plugins-stack [data-rmroot="0"]`)
	waitUntil(t, func() bool { _, roots := a.toolReg.Roots(); return len(roots) == 0 }, "the host removed the folder")
	awaitOnPage(t, page, "the folder gone from the list", `
    last = document.querySelector('#plugins-stack [data-folders]').textContent;
    return document.querySelector('#plugins-stack [data-rmroot]') ? null : 'ok';`)

	// ---- A tool, switched off and on from here, kept by the host.
	const read = `#plugins-stack [data-tools] [data-tool="read"]`
	click("the read tool's switch", read)
	waitUntil(t, func() bool { return !a.toolReg.ToolEnabled("read") }, "the host switched the read tool off")
	awaitOnPage(t, page, "the read tool drawn off, from the host's answer", `const sw = document.querySelector('`+read+`');
    last = sw ? 'checked=' + sw.checked + ' disabled=' + sw.disabled : 'absent';
    return sw && !sw.checked && !sw.disabled ? 'ok' : null;`)
	if !slices.Contains(a.configSnapshot().Tools.Disabled, "read") {
		t.Fatalf("the saved configuration does not hold the read tool off: %v", a.configSnapshot().Tools.Disabled)
	}
	click("the read tool's switch again", read)
	waitUntil(t, func() bool { return a.toolReg.ToolEnabled("read") }, "the host switched the read tool on")
	awaitOnPage(t, page, "the read tool drawn on, from the host's answer", `const sw = document.querySelector('`+read+`');
    last = sw ? 'checked=' + sw.checked + ' disabled=' + sw.disabled : 'absent';
    return sw && sw.checked && !sw.disabled ? 'ok' : null;`)

	// ---- The old addresses land here.
	for _, old := range []string{"#/settings/sandbox", "#/settings/tools"} {
		if _, err := page.eval(`(() => { location.hash = '#/settings/model'; return 'moved'; })()`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		awaitOnPage(t, page, "back on Model before "+old, `
    const on = document.getElementById('view-settings');
    last = location.hash;
    return location.hash === '#/settings/model' && on && on.classList.contains('on') ? 'ok' : null;`)
		if _, err := page.eval(`(() => { location.hash = '`+old+`'; return 'moved'; })()`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if got := awaitOnPage(t, page, old+" lands on Plugins and permissions", there); got != "ok" {
			t.Fatalf("%s: %s", old, got)
		}
	}
}
