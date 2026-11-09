//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

// boardDriver is the shell's driver and what the board's tests read: what is
// drawn, Settings' list and its rows, and whether the board is what shows.
const boardDriver = shellDriver + `
const shown = el => !!el && el.getClientRects().length > 0;
const tabs = () => document.getElementById('place-tabs');
const views = () => document.getElementById('views');
// Settings' own rows: children of the list itself, never Technical's panels,
// which technical.js draws in a nav of their own inside it.
const rows = () => [...document.querySelectorAll('#place-tabs > .c-side-nav > .c-side-nav__item')];
const rowName = a => (a.querySelector(':scope > span:first-child') || {}).textContent || '';
const onBoard = () => shown(tabs()) && !shown(views()) && rows().length > 0;
// The groups: Status is the glance.
const GROUPS = ['Phone and other devices', 'Model', 'Voice', 'Plugins and permissions', 'People and messages', 'Access', 'Backups', 'App', 'Technical'];
const go = async (hash, cond) => { location.hash = hash; await until(cond); };
const start = async () => { const { ready } = await import('/recovery.js'); await ready; await until(() => document.querySelector('header.c-top-bar nav')); };
`

func runBoard(t *testing.T, w, h int, script string) {
	t.Helper()
	runBoardProviders(t, w, h, nil, script)
}

// runBoardProviders is runBoard with a server whose every "providers" answer
// carries dir: a test that draws providers gets them the way the page does,
// so no later answer (the page's own ask, the local-model part's) replaces
// them under it. A nil dir answers with no providers, as runBoard always has.
func runBoardProviders(t *testing.T, w, h int, dir *ProviderDirectory, script string) {
	t.Helper()
	pressed := 0
	hd := statusSectionHandler(&pressed)
	if dir != nil {
		hd.GetProviders = func() ProviderDirectory { return *dir }
	}
	s := New("127.0.0.1", 0, hd)
	runDashboardInEnginesAtSize(t, s, "/", boardDriver+`
(async () => {
  await start();
`+script+`
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`, w, h)
}

// (a) AT A PHONE'S WIDTH SETTINGS' OWN ADDRESS IS ITS LIST (the board
// PhoneSettings): the glance first, Status's answer above its name, then the
// groups as rows in order, names only, each at least the board's
// 56 px, set 12 px apart before Model and before Technical.
func TestSettingsAtPhoneWidthIsItsList(t *testing.T) {
	runBoard(t, 390, 844, `
  await go('#/settings', onBoard);
  if (!onBoard()) return fail('#/settings at 390 is not the list: list shown ' + shown(tabs()) + ', page shown ' + shown(views()));
  const all = rows().filter(shown);
  const glanceRow = all[0];
  if (!glanceRow || glanceRow.dataset.item !== 'status') return fail('the first row is ' + (glanceRow && glanceRow.dataset.item) + ', not Status');
  const glance = () => { const r = rows().filter(shown)[0]; return r && r.querySelector('.status__glance'); };
  await until(() => glance() && glance().textContent);
  const said = glance();
  if (!said || !shown(said) || !said.textContent) return fail('Status\'s row carries no answer');
  if (said.getBoundingClientRect().top >= glanceRow.querySelector(':scope > span:first-child').getBoundingClientRect().top) return fail('the answer is not above Status\'s name');
  const groups = all.slice(1);
  const names = groups.map(rowName).join('|');
  if (names !== GROUPS.join('|')) return fail('the rows are ' + names);
  for (const a of groups) {
    if (a.textContent !== rowName(a)) return fail(rowName(a) + ' carries more than its name: ' + a.textContent);
    const r = a.getBoundingClientRect();
    if (r.height < 56) return fail(rowName(a) + ' is ' + r.height + ' px tall');
  }
  for (const id of ['model', 'technical']) {
    const a = groups.find(x => x.dataset.item === id), before = a.previousElementSibling;
    const gap = a.getBoundingClientRect().top - before.getBoundingClientRect().bottom;
    if (gap < 11.5) return fail(rowName(a) + ' is ' + gap + ' px from the row above, not set apart');
  }
  const inner = groups.find(x => x.dataset.item === 'voice');
  if (inner.getBoundingClientRect().top - inner.previousElementSibling.getBoundingClientRect().bottom > 1) return fail('Voice is set apart from Model');
`)
}

// (b) EACH ROW REACHES ITS PAGE: at 390, pressing a group's row opens its
// group's address and page, the list gone; Status's opens Status's own
// page, #/settings/status.
func TestEachSettingsRowAtPhoneWidthOpensItsPage(t *testing.T) {
	runBoard(t, 390, 844, `
  const want = { status: 'view-status', phone: 'view-phone', model: 'view-settings', voice: 'view-settings', permissions: 'view-plugins',
    people: 'view-settings', access: 'view-settings', backups: 'view-settings', app: 'view-settings', technical: 'view-technical' };
  for (const id of Object.keys(want)) {
    await go('#/settings', onBoard);
    const a = rows().find(x => x.dataset.item === id);
    if (!shown(a)) return fail('no row for ' + id + ' on the list');
    a.click();
    const at = id === 'technical' ? /^#\/settings\/technical(\/|$)/ : new RegExp('^#/settings/' + id + '$');
    await until(() => at.test(location.hash) && viewOn() === want[id] && !onBoard() && shown(views()));
    if (!at.test(location.hash)) return fail(id + '\'s row went to ' + location.hash);
    if (viewOn() !== want[id]) return fail(id + '\'s row shows ' + viewOn());
    if (onBoard() || !shown(views())) return fail(id + '\'s row left the list showing over its page');
  }
`)
}

// (c) A GROUP'S PAGE AT A PHONE'S WIDTH HAS NO LIST ABOVE IT: the page is
// what comes first under the top bar.
func TestASettingsGroupAtPhoneWidthHasNoListAboveIt(t *testing.T) {
	runBoard(t, 390, 844, `
  for (const [hash, view] of [['#/settings/model', 'view-settings'], ['#/settings/voice', 'view-settings'], ['#/settings/phone', 'view-phone'], ['#/settings/status', 'view-status']]) {
    await go(hash, () => viewOn() === view && shown(views()));
    if (viewOn() !== view || !shown(views())) return fail(hash + ' shows ' + viewOn());
    if (shown(tabs())) return fail(hash + ' at 390 draws Settings\' list above its page');
  }
`)
}

// (h) A GROUP'S PAGE SAYS ITS NAME (host 1739 item 2; the boards
// SettingsModel, SettingsVoice and SettingsBackups): one page heading, the
// group's name as the Settings list names it, first on the page, at a phone's
// width, where the list is not drawn, and at the desktop's beside it. Access,
// App and People draw their own; the others are given theirs (2b). This rig
// serves no configuration, so Access stays on its loading card here and
// is given its name: the rule that a page with its own title is not given a
// second is proved on App (control 2b-c), not on Access. Its size
// is the page's largest, --t-xl (24/32 up to 1280 px; the boards' 28/36 is
// not on the scale).
const groupNameDriver = `
  const heads = () => [...document.querySelectorAll('#settings-stack h1, #settings-stack h2')];
  // Each page by its own first section, so the page before it cannot pass.
  for (const [hash, name, sec] of [['#/settings/model', 'Model', 'substrate'], ['#/settings/voice', 'Voice', 'speech'], ['#/settings/backups', 'Backups', 'backups'],
    ['#/settings/sandbox', 'Plugins and permissions', 'sandbox'], ['#/settings/access', 'Access', 'dashboard'], ['#/settings/app', 'App', 'updates'], ['#/settings/people', 'People and messages', 'messages']]) {
    const own = () => document.querySelector('#settings-stack [data-section="' + sec + '"]');
    await go(hash, () => viewOn() === 'view-settings' && shown(views()) && own() && heads().length > 0);
    if (!own()) return fail(hash + ' did not draw its ' + sec + ' section');
    const hs = heads();
    if (hs.length !== 1) return fail(hash + ' draws ' + hs.length + ' page headings: ' + hs.map(h => h.textContent.trim()).join(' | '));
    const h = hs[0], said = h.textContent.trim();
    if (h.tagName !== 'H2' || said !== name) return fail(hash + '\'s heading is ' + h.tagName + ' "' + said + '", not h2 "' + name + '"');
    if (!shown(h)) return fail(hash + '\'s heading "' + said + '" is not shown');
    const cs = getComputedStyle(h);
    if (cs.fontSize !== '24px' || cs.lineHeight !== '32px' || cs.fontWeight !== '600') return fail(hash + '\'s heading is ' + cs.fontSize + '/' + cs.lineHeight + ' weight ' + cs.fontWeight);
    const first = [...document.querySelectorAll('#settings-stack [data-section]')].find(shown);
    if (first && !first.contains(h) && !(h.getBoundingClientRect().bottom <= first.getBoundingClientRect().top + 1)) return fail(hash + '\'s heading is not above its page');
  }
`

func TestASettingsGroupsPageSaysItsNameAtPhoneWidth(t *testing.T) {
	runBoard(t, 390, 844, groupNameDriver)
}

func TestASettingsGroupsPageSaysItsNameAtDesktopWidth(t *testing.T) {
	runBoard(t, 1280, 800, groupNameDriver)
}

// (d) EVERY OLD SETTINGS ADDRESS STILL LANDS at a phone's width: on its
// group's page, never on a list hidden there.
func TestOldSettingsAddressesAtPhoneWidthLandOnAPage(t *testing.T) {
	runBoard(t, 390, 844, `
  const routes = [
    ['#/plugins', '#/settings/permissions', 'view-plugins'],
    ['#/settings/all', '#/settings/model', 'view-settings'],
    ['#/settings/speech', '#/settings/voice', 'view-settings'],
    ['#/settings/substrate', '#/settings/model', 'view-settings'],
    ['#/settings/providers', '#/settings/model/providers', 'view-settings'],
    ['#/settings/dashboard', '#/settings/access', 'view-settings'],
    ['#/settings/messages', '#/settings/people', 'view-settings'],
    ['#/settings/held-mail', '#/settings/people', 'view-settings'],
    ['#/settings/updates', '#/settings/app', 'view-settings'],
    ['#/settings/witness', '#/settings/technical/verification', 'view-technical'],
  ];
  for (const [old, now, view] of routes) {
    await go(old, () => location.hash === now && viewOn() === view && shown(views()) && !onBoard());
    if (location.hash !== now) return fail(old + ' is at ' + location.hash + ', not ' + now);
    if (viewOn() !== view || !shown(views())) return fail(old + ' shows ' + viewOn() + ', page shown ' + shown(views()));
    if (onBoard()) return fail(old + ' landed on the list, not a page');
  }
`)
}

// (e) THE BAR HOLDS FOUR on Settings' list and on a group's page at a
// phone's width, Settings marked as where this is.
func TestTheBarHoldsFourOverSettingsAtPhoneWidth(t *testing.T) {
	runBoard(t, 390, 844, `
  for (const [hash, cond] of [['#/settings', onBoard], ['#/settings/model', () => viewOn() === 'view-settings']]) {
    await go(hash, () => cond() && placeOn() === 'settings');
    const bar = [...document.querySelectorAll('#nav a')].filter(shown);
    const words = bar.map(a => a.innerText).join('|');
    // The third is the identity's name ("About them" only while none is known).
    if (bar.length !== 4 || bar[0].innerText !== 'Chat' || bar[1].innerText !== 'Projects' || !bar[2].innerText.trim() || bar[3].innerText !== 'Settings') return fail(hash + ': the bar holds ' + words);
    for (const a of bar) { const r = a.getBoundingClientRect(); if (r.height < 44 || Math.abs(document.getElementById('nav').getBoundingClientRect().bottom - innerHeight) > 2) return fail(hash + ': ' + a.innerText + ' is ' + r.height + ' px, or the bar is off the bottom'); }
    if (placeOn() !== 'settings') return fail(hash + ' marks ' + placeOn());
  }
`)
}

// (f) AT A DESKTOP'S WIDTH NOTHING OF THE BOARD SHOWS: Settings' list beside
// the page at #/settings and on a group's, no glance; Status's entry is
// Status's own address, which draws the same page.
func TestSettingsAtDesktopWidthKeepsItsListBesideThePage(t *testing.T) {
	runBoard(t, 1280, 800, `
  for (const [hash, view] of [['#/settings', 'view-status'], ['#/settings/status', 'view-status'], ['#/settings/model', 'view-settings']]) {
    await go(hash, () => viewOn() === view && shown(tabs()) && shown(views()));
    if (!shown(tabs()) || !shown(views())) return fail(hash + ' at 1280: list shown ' + shown(tabs()) + ', page shown ' + shown(views()));
    const t = tabs().getBoundingClientRect(), v = views().getBoundingClientRect();
    if (!(t.right <= v.left + 1)) return fail(hash + ' at 1280 draws the list at ' + t.left + '–' + t.right + ', not beside the page from ' + v.left);
    // Status's entry carries its answer at every width (status.js); on Status's
    // own addresses wait for it, so a glance not yet drawn cannot pass as hidden.
    const gl = () => document.querySelector('#place-tabs [data-item="status"] .status__glance');
    if (view === 'view-status') {
      await until(() => gl() && gl().textContent);
      if (!gl() || !gl().textContent) return fail(hash + ' at 1280: Status\'s entry carries no answer, so its hiding proves nothing');
    }
    if (shown(gl())) return fail(hash + ' at 1280 shows the glance');
    const r = rows().find(x => x.dataset.item === 'model').getBoundingClientRect();
    if (r.height >= 56) return fail(hash + ' at 1280 draws the board\'s rows: Model is ' + r.height + ' px');
  }
  const st = rows().find(x => x.dataset.item === 'status');
  if (st.getAttribute('href') !== '#/settings/status') return fail('Status\'s entry is ' + st.getAttribute('href'));
`)
}

// (g) THE WAY BACK: at a phone's width, on a group's page, pressing the
// bar's Settings, already marked as where this is, lands on #/settings and
// draws the list. Between #/settings and #/settings/status the page stays
// Status's, so the router draws nothing: the list must come and go when the
// address moves, read as that hashchange settles, not when Status next
// redraws on a frame seconds later (renderPlaceTabs also marks the board).
// So it is read in that hashchange itself, after the page's own listeners
// (registered when it loaded, so they run first): no clock to fall back on.
func TestTheBarsSettingsAtPhoneWidthLeadsBackToTheList(t *testing.T) {
	runBoard(t, 390, 844, `
  const settle = act => new Promise(r => { addEventListener('hashchange', () => r(), { once: true }); act(); });
  for (const [hash, view] of [['#/settings/model', 'view-settings'], ['#/settings/status', 'view-status'], ['#/settings/technical', 'view-technical']]) {
    const stays = view === 'view-status';
    if (stays) await settle(() => { location.hash = hash; });
    else await go(hash, () => viewOn() === view && placeOn() === 'settings' && !onBoard());
    if (onBoard()) return fail(hash + ' shows the list');
    const back = () => document.querySelector('#nav a[data-view="settings"]').click();
    if (stays) await settle(back);
    else { back(); await until(() => location.hash === '#/settings' && onBoard()); }
    if (location.hash !== '#/settings') return fail('the bar\'s Settings from ' + hash + ' went to ' + location.hash);
    if (!onBoard()) return fail('the bar\'s Settings from ' + hash + ' did not draw the list: list shown ' + shown(tabs()) + ', page shown ' + shown(views()));
  }
`)
}

// (h) TECHNICAL'S PANELS STAY REACHABLE at a phone's width: its one line of
// panels shows above its page, alone of the list, and a panel pressed there
// opens.
func TestTechnicalsPanelsAtPhoneWidthShowAlone(t *testing.T) {
	runBoard(t, 390, 844, `
  await go('#/settings/technical', () => viewOn() === 'view-technical' && shown(document.querySelector('#place-tabs .technical__nav a')));
  const panels = [...document.querySelectorAll('#place-tabs .technical__nav a')].filter(shown);
  if (panels.length < 2) return fail('Technical at 390 shows ' + panels.length + ' panels in its line');
  const groups = rows().filter(shown);
  if (groups.length) return fail('Technical at 390 shows Settings\' rows too: ' + groups.map(rowName).join('|'));
  const next = panels[1];
  next.click();
  await until(() => location.hash === next.getAttribute('href') && viewOn() === 'view-technical');
  if (location.hash !== next.getAttribute('href')) return fail('the panel ' + next.textContent + ' went to ' + location.hash);
`)
}
