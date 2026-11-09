//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

// shellDriver is what the shell tests share: the page's own start, a wait
// for a condition, and the place and tab the page shows.
const shellDriver = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
const until = (cond, ms) => new Promise(r => { const end = Date.now() + (ms || 5000); const tick = () => (cond() || Date.now() > end) ? r() : setTimeout(tick, 15); tick(); });
const viewOn = () => (document.querySelector('.view.on') || {}).id || '';
const placeOn = () => [...document.querySelectorAll('#nav [aria-current="page"]')].map(a => a.dataset.view).join();
// The open tab of About's tabs, or the open item of Settings' list.
const tabOn = () => { const t = document.querySelector('#place-tabs [role="tab"][aria-selected="true"], #place-tabs [aria-current="page"] > span:first-child'); return t ? t.textContent : ''; };
`

// EVERY OLD ADDRESS LANDS ON ITS NEW PLACE (spec section 3 and step 3).
// Memory, Record and a row of it, and Plugins move to About {name}'s and
// Settings' tabs, the address saying where they are now; the places' own
// addresses open their first tab; Chat is Home's; and a tab, chosen, is an
// address. An old address with a row of the record in it, opened cold (a
// link in a notice, a reload), lands on that row's new address. (A Settings
// section keeping its address, cold and warm, is settings_address's.)
func TestEveryOldRouteLandsOnItsNewPlace(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	runDashboardInEngines(t, s, "/", shellDriver+`
const routes = [
  ['#/memory', '#/identity/beliefs', 'view-memory', 'identity', 'Beliefs'],
  ['#/record', '#/identity/record', 'view-record', 'record', 'Record'],
  ['#/record/7', '#/identity/record/7', 'view-record', 'record', 'Record'],
  ['#/plugins', '#/settings/permissions', 'view-plugins', 'plugins', 'Plugins and permissions'],
  ['#/identity', '#/identity', 'view-identity', 'identity', 'Who they are'],
  ['#/chat', '#/chat', 'view-chat', 'chat', ''],
  ['#/home', '#/chat', 'view-chat', 'chat', ''],
  ['#/projects', '#/projects', 'view-projects', 'projects', ''],
  ['#/settings', '#/settings', 'view-status', 'settings', 'Status'],
  // Phone and other devices is its own entry, never read as a group.
  ['#/settings/phone', '#/settings/phone', 'view-phone', 'settings', 'Phone and other devices'],
  ['#/settings/all', '#/settings/model', 'view-settings', 'settings', 'Model'],
  // Each group is an address, marked in the list; each old section lands
  // on its group's address; a Technical form lands on its panel.
  ['#/settings/voice', '#/settings/voice', 'view-settings', 'settings', 'Voice'],
  // An old name for the section shown moves its address too.
  ['#/settings/speech', '#/settings/voice', 'view-settings', 'settings', 'Voice'],
  ['#/settings/substrate', '#/settings/model', 'view-settings', 'settings', 'Model'],
  ['#/settings/providers', '#/settings/model/providers', 'view-settings', 'settings', 'Model'],
  ['#/settings/speech', '#/settings/voice', 'view-settings', 'settings', 'Voice'],
  ['#/settings/dashboard', '#/settings/access', 'view-settings', 'settings', 'Access'],
  ['#/settings/messages', '#/settings/people', 'view-settings', 'settings', 'People and messages'],
  ['#/settings/held-mail', '#/settings/people', 'view-settings', 'settings', 'People and messages'],
  ['#/settings/updates', '#/settings/app', 'view-settings', 'settings', 'App'],
  ['#/settings/backups', '#/settings/backups', 'view-settings', 'settings', 'Backups'],
  // Technical lists its panels under its own entry in the sidebar and marks
  // the panel shown (technical.js), so the tab read here is the panel's
  // name (step 19).
  ['#/settings/witness', '#/settings/technical/verification', 'view-technical', 'settings', 'Verification'],
];
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  if (sessionStorage.getItem('routes-cold') === '1') {
    await until(() => location.hash === '#/identity/record/9' && viewOn() === 'view-record' && tabOn() === 'Record');
    if (location.hash !== '#/identity/record/9') return fail('#/record/9 opened cold is at ' + location.hash);
    if (viewOn() !== 'view-record' || tabOn() !== 'Record') return fail('#/record/9 opened cold shows ' + viewOn() + ' under the tab "' + tabOn() + '"');
    return report('OK');
  }
  const at = now => now instanceof RegExp ? now.test(location.hash) : location.hash === now;
  for (const [old, now, view, place, tab] of routes) {
    location.hash = old;
    await until(() => at(now) && viewOn() === view && placeOn() === place && tabOn() === tab);
    if (!at(now)) return fail(old + ' is at ' + location.hash + ', not ' + now);
    if (viewOn() !== view) return fail(old + ' shows ' + viewOn() + ', not ' + view);
    if (placeOn() !== place) return fail(old + ' marks the place ' + placeOn() + ', not ' + place);
    if (tabOn() !== tab) return fail(old + ' selects the tab "' + tabOn() + '", not "' + tab + '"');
  }
  location.hash = '#/identity';
  await until(() => tabOn() === 'Who they are');
  document.querySelector('#place-tabs [data-tab="record"]').click();
  await until(() => location.hash === '#/identity/record' && viewOn() === 'view-record');
  if (location.hash !== '#/identity/record' || viewOn() !== 'view-record') return fail('choosing the Record tab went to ' + location.hash);
  const panel = document.getElementById('view-record');
  if (panel.getAttribute('role') !== 'tabpanel' || !document.getElementById(panel.getAttribute('aria-labelledby'))) return fail('the view is not its tab\'s panel');
  location.hash = '#/chat';
  await until(() => viewOn() === 'view-chat');
  if (panel.hasAttribute('role')) return fail('a view no longer shown is still a tab\'s panel');
  sessionStorage.setItem('routes-cold', '1');
  history.replaceState(null, '', '#/record/9');
  location.reload();
})().catch(e => fail((e && e.message) || String(e)));
`)
}

// THE BAR HOLDS FOUR AT A PHONE'S WIDTH (spec step 3): Home, Projects, About
// and Settings along the bottom, each at least 44 px tall, the name above;
// at a desktop's, three places and the name.
func TestTheBarHoldsFourAtPhoneWidth(t *testing.T) {
	for _, size := range []struct {
		w, h  int
		want  string
		phone string
	}{{390, 844, "Chat|Projects|About them|Settings", "true"}, {1280, 800, "Chat|Projects|About them|Record|Plugins|Settings", "false"}} {
		s := New("127.0.0.1", 0, &WSHandler{})
		runDashboardInEnginesAtSize(t, s, "/", shellDriver+`
const phone = `+size.phone+`;
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  await until(() => document.querySelector('.c-rail nav'));
  const shown = [...document.querySelectorAll('#nav a')].filter(a => a.getClientRects().length);
  const words = shown.map(a => a.innerText).join('|');
  if (words !== '`+size.want+`') return fail('the bar holds ' + words);
  if (phone) {
    for (const a of shown) { const r = a.getBoundingClientRect(); if (r.height < 44) return fail(a.textContent + ' is ' + r.height + ' px tall'); }
    const nav = document.getElementById('nav').getBoundingClientRect();
    if (Math.abs(nav.bottom - innerHeight) > 2) return fail('the places are not along the bottom: ' + nav.bottom + ' of ' + innerHeight);
  }
  const who = [...document.querySelectorAll('.c-top-bar__who, .c-rail__who')].find(e => e.getClientRects().length).getBoundingClientRect();
  if (!(who.height > 0 && who.top < 60)) return fail('the name is not at the top');
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`, size.w, size.h)
	}
}

// A PLUGIN'S PAGE IS DRAWN UNDER SETTINGS (step 16b, R14-12): while a page
// its plugin's card opens is the view, the bar marks Settings, as the frames
// say (TestAPluginsPageOpensFromItsCardAndKeepsAWayIn); another section's
// page marks no place.
func TestAPluginsPageIsDrawnUnderSettings(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	runDashboardInEnginesAtSize(t, s, "/", shellDriver+`
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  await until(() => document.querySelector('header.c-top-bar nav'));
  const { S } = await import('/state.js');
  S.framePlace = v => v === 'section:owned' ? 'settings' : '';
  S.view = 'section:owned'; S.renderTopBar();
  if (placeOn() !== 'settings') return fail('a plugin\'s page marks ' + (placeOn() || 'no place'));
  S.view = 'section:free'; S.renderTopBar();
  if (placeOn() !== '') return fail('a section no card opens marks ' + placeOn());
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`, 1280, 800)
}

// AN IDENTITY'S COPY OF app.js, AND OF A VIEW, FROM BEFORE THIS STEP STILL
// START (spec step 3, "wrong if"; section 6b). Their copies of 0.2.0's
// app.js and views/identity.js, approved, in the overlay, against the new
// shell: with an identity, the page connects and says the name, the shell
// holds its links to the four places, and the copy's own navigation moves
// to each of them and to the conversation; with none yet, first run shows.
func TestAStaleAppAndViewInTheOverlayStillStart(t *testing.T) {
	for _, name := range []string{"Mira", "(not born)"} {
		dir := filepath.Join(t.TempDir(), "ui")
		for _, file := range []string{"app.js", "views/identity.js"} {
			b, err := os.ReadFile(filepath.Join("testdata", "overlay-0.2.0", file))
			if err != nil {
				t.Fatal(err)
			}
			writeOverlay(t, dir, file, string(b))
		}
		name := name
		s := New("127.0.0.1", 0, &WSHandler{
			GetStats: func() (*StatsResponse, error) { return &StatsResponse{Name: name, Version: "0.2.1-test"}, nil },
		})
		s.SetUIOverlay(dir)
		approveOverlay(t, s, dir, "app.js", "views/identity.js")
		born := "true"
		if name == "(not born)" {
			born = "false"
		}
		runDashboardInEngines(t, s, "/", shellDriver+`
const born = `+born+`;
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  const failed = document.getElementById('boot-failure');
  if (failed) return fail('the page with an old app.js did not start: ' + failed.textContent);
  const links = [...document.querySelectorAll('#nav a.nav-item[data-view]')].map(a => a.dataset.view).join();
  if (links !== 'chat,projects,identity,record,plugins,settings') return fail('the shell does not hold its links: ' + links);
  if (!born) {
    // First run's screen is drawn by its own module (ws.js fetches it),
    // whichever app.js shows it: Begin is there to press.
    await until(() => { const p = document.getElementById('fb-birth'); return p && p.getClientRects().length; });
    const p = document.getElementById('fb-birth');
    if (!p || !p.getClientRects().length) return fail('first run does not show under an old app.js');
    return report('OK');
  }
  await until(() => document.getElementById('p-name').textContent === 'Mira');
  if (document.getElementById('p-name').textContent !== 'Mira') return fail('the name is not said under an old app.js: ' + document.getElementById('p-name').textContent);
  for (const [place, view] of [['projects', 'view-projects'], ['identity', 'view-identity'], ['settings', 'view-settings']]) {
    document.querySelector('#nav a[data-view="' + place + '"]').click();
    // The link's own move to its address settles before the next one.
    await until(() => viewOn() === view && location.hash.indexOf('#/' + place) === 0);
    await new Promise(r => setTimeout(r, 50));
    if (viewOn() !== view) return fail('the old app.js did not open ' + place + ': ' + viewOn());
  }
  location.hash = '#/chat';
  await until(() => viewOn() === 'view-chat');
  if (viewOn() !== 'view-chat') return fail('the old app.js did not open the conversation: ' + viewOn());
  const composer = document.getElementById('composer-wrap');
  if (composer && !composer.getClientRects().length) return fail('the composer is hidden in the conversation under an old app.js');
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
	}
}

// CHOOSING THE OTHER ENTRY OF SETTINGS' LIST KEEPS THE KEYBOARD WHERE IT
// IS (a review's read of step 3). Settings' list is links; a keyboard user who
// focuses one and presses it lands on its page with that link still
// focused, and back the other way, as a tab keeps its focus.
func TestSettingsListKeepsFocusWhenItsEntryIsChosen(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{Name: "Mira", Version: "0.2.1-test"}, nil },
	})
	runDashboardInEngines(t, s, "/", shellDriver+`
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  location.hash = '#/settings';
  await until(() => viewOn() === 'view-settings' && document.querySelector('#place-tabs [data-item="permissions"]'));
  // A wait that runs out says so, and what it saw: the view and the address
  // at every change while it waited (so a later frame moving the view back
  // shows as such), and where the focus and the link were at the end.
  const waitFor = (cond, ms) => new Promise(r => {
    const t0 = performance.now(), seen = [];
    let last = '';
    const tick = () => {
      const now = viewOn() + ' ' + location.hash;
      if (now !== last) { seen.push(Math.round(performance.now() - t0) + 'ms ' + now); last = now; }
      if (cond()) return r(null);
      if (performance.now() - t0 > ms) return r({ elapsed: Math.round(performance.now() - t0), seen, visibility: document.visibilityState });
      setTimeout(tick, 15);
    };
    tick();
  });
  for (const [item, view, address] of [['permissions', 'view-plugins', /^#\/settings\/permissions$/], ['model', 'view-settings', /^#\/settings\/model$/]]) {
    const link = document.querySelector('#place-tabs [data-item="' + item + '"]');
    if (!link) return fail('Settings\' list has no ' + item);
    link.focus();
    link.click();
    // The activation moves the address, hashchange fires, and go() draws the
    // view a frame later: the checks wait for all of it, within until's own
    // bound of 5 s.
    const late = await waitFor(() => viewOn() === view && address.test(location.hash) && link.getAttribute('aria-current') === 'page', 5000);
    if (late) {
      const current = [...document.querySelectorAll('#place-tabs [aria-current="page"]')].map(a => a.dataset.item).join() || 'none';
      return fail(item + ' did not open its page within ' + late.elapsed + ' ms: the view and address over the wait were [' + late.seen.join('; ') + ']; the current entry ' + current +
        '; the link connected ' + link.isConnected + ', focused ' + (document.activeElement === link) + '; the page ' + late.visibility);
    }
    if (!link.isConnected || document.activeElement !== link) return fail('choosing ' + item + ' lost the keyboard\'s place: the focus is on ' + (document.activeElement ? document.activeElement.outerHTML.slice(0, 80) : 'nothing'));
  }
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}

// A RENDER OF SETTINGS DOES NOT UNDO A MOVE AWAY FROM IT. Following a link
// out of Settings (Plugins and permissions) moves the address at once; its
// hashchange runs a task later. A render of Settings landing in between (a
// config frame, a status tick) rewrote the address to the section shown,
// by replaceState, which fires no hashchange, and the move was lost: in
// Firefox the address read #/settings/substrate for 15 s after the click.
// Here the render is put in between on purpose.
func TestARenderOfSettingsDoesNotUndoAMoveAway(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{Name: "Mira", Version: "0.2.1-test"}, nil },
	})
	runDashboardInEngines(t, s, "/", shellDriver+`
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  const { renderSettings } = await import('/views/settings.js');
  location.hash = '#/settings/all';
  await until(() => viewOn() === 'view-settings' && /^#\/settings\/[a-z-]+$/.test(location.hash));
  if (viewOn() !== 'view-settings') return fail('Settings did not open');
  location.hash = '#/settings/permissions';
  renderSettings();
  if (location.hash !== '#/settings/permissions') return fail('a render of Settings put its own address back over the move: ' + location.hash);
  await until(() => viewOn() === 'view-plugins');
  if (viewOn() !== 'view-plugins' || location.hash !== '#/settings/permissions') return fail('the move to Plugins and permissions was lost: ' + viewOn() + ' at ' + location.hash);
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}

// AN IDENTITY'S OWN index.html FROM BEFORE THIS STEP KEEPS ITS RAIL
// WORKING (spec section 0). 0.2.0's index.html, in the overlay, under the
// new app.js: the page starts with no top bar, and each rail button goes to
// the view it names, at that view's new address, marked as the open one.
func TestAnOldIndexKeepsItsRailWorking(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "overlay-0.2.0", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "index.html", string(b))
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetUIOverlay(dir)
	runDashboardInEngines(t, s, "/", shellDriver+`
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  const failed = document.getElementById('boot-failure');
  if (failed) return fail('the page under an old index.html did not start: ' + failed.textContent);
  if (document.querySelector('.c-top-bar') || !document.querySelector('.rail #nav')) return fail('the test is not running the old index.html');
  for (const [view, address] of [['memory', '#/identity/beliefs'], ['plugins', '#/settings/permissions'], ['record', '#/identity/record'], ['chat', '#/chat']]) {
    const button = document.querySelector('#nav .nav-item[data-view="' + view + '"]');
    if (!button) return fail('the old rail has no ' + view);
    button.click();
    await until(() => viewOn() === 'view-' + view && location.hash === address);
    if (viewOn() !== 'view-' + view || location.hash !== address) return fail('the rail\'s ' + view + ' went to ' + viewOn() + ' at ' + location.hash);
    if (button.getAttribute('aria-current') !== 'page' || !button.classList.contains('active')) return fail('the rail\'s ' + view + ' is not marked open');
    const others = [...document.querySelectorAll('#nav .nav-item[aria-current]')].filter(b => b !== button);
    if (others.length) return fail('more than one rail entry is marked open: ' + others.map(b => b.dataset.view).join());
  }
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}

// THE FIRST OPEN AFTER THE UPDATE SAYS WHAT MOVED, ONCE, TO A BROWSER THAT
// USED THE PAGE BEFORE (spec step 3; the walk, 2026-11-03). A new browser is
// told nothing, and remembers the version. One that kept a view from the
// earlier page and no version is told, at the top, that the page changed,
// with where each thing went and Got it; Got it remembers the version and the
// note is gone, and stays gone on reload. A browser whose identity is not born
// yet is told nothing, and remembers; its first run has no composer drawn
// over it.
func TestTheFirstOpenSaysWhatMoved(t *testing.T) {
	for _, born := range []bool{true, false} {
		name := "Mira"
		if !born {
			name = "(not born)"
		}
		s := New("127.0.0.1", 0, &WSHandler{
			GetStats: func() (*StatsResponse, error) { return &StatsResponse{Name: name, Version: "0.2.1-test"}, nil },
		})
		want := "true"
		if !born {
			want = "false"
		}
		runDashboardInEngines(t, s, "/", shellDriver+`
const born = `+want+`;
const note = () => document.querySelector('#place-note .c-os-note');
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  await until(() => document.getElementById('p-name').textContent !== '…' && document.getElementById('p-name').textContent !== '');
  const phase = sessionStorage.getItem('first-open') || 'new';
  if (!born) {
    await until(() => { try { return localStorage.getItem('aii.seenVersion'); } catch (e) { return true; } });
    if (note()) return fail('a browser with no identity yet was told the page changed');
    if (localStorage.getItem('aii.seenVersion') !== '0.2.1-test') return fail('the version was not remembered before birth');
    // First run has no conversation yet: no composer is drawn over its card.
    location.hash = '#/chat';
    await until(() => viewOn() === 'view-chat' && document.getElementById('firstboot').classList.contains('on'));
    if (document.getElementById('composer-wrap').getClientRects().length) return fail('the composer is drawn over first run');
    return report('OK');
  }
  if (phase !== 'used') {
    // The note is decided in the same call that says the name.
    await until(() => document.getElementById('p-name').textContent === 'Mira');
    if (note()) return fail(phase === 'new' ? 'a new browser was told the page changed' : 'the note came back after Got it');
    if (phase === 'again') return report('OK');
    if (localStorage.getItem('aii.seenVersion') !== '0.2.1-test') return fail('a new browser did not remember the version');
    // As a browser that used the earlier page: a view kept, and no version.
    if (localStorage.getItem('aii.view') === null) return fail('rig: the page kept no view');
    localStorage.removeItem('aii.seenVersion');
    sessionStorage.setItem('first-open', 'used');
    // Firefox commits storage at the end of the task: a reload in the same
    // task opened with the version still kept.
    await new Promise(r => setTimeout(r, 0));
    return location.reload();
  }
  await until(() => note());
  const n = note();
  if (!n) return fail('no note on the first open of a browser that used the earlier page: it kept view ' + localStorage.getItem('aii.view') + ', version ' + localStorage.getItem('aii.seenVersion'));
  if (!n.textContent.includes('This page has changed')) return fail('the note does not say the page changed: ' + n.textContent);
  // It is said by the region it arrives in (#place-note, in the page before
  // its words), and is no region of its own: one region, said once.
  if (n.hasAttribute('role') || n.parentElement.getAttribute('role') !== 'status') return fail('the note is not said by the region it arrives in: role=' + n.getAttribute('role') + ' in ' + n.parentElement.id);
  const items = [...n.querySelectorAll('details li')].map(li => li.textContent);
  if (!items.some(t => t.includes('About Mira')) || !items.some(t => t.includes('Plugins and permissions'))) return fail('the note does not say where things went: ' + items.join(' | '));
  const got = [...n.querySelectorAll('button')].find(b => b.textContent === 'Got it');
  if (!got) return fail('the note has no Got it');
  got.click();
  if (note()) return fail('Got it did not take the note away');
  if (localStorage.getItem('aii.seenVersion') !== '0.2.1-test') return fail('Got it did not remember the version');
  sessionStorage.setItem('first-open', 'again');
  location.reload();
})().catch(e => fail((e && e.message) || String(e)));
`)
	}
}
