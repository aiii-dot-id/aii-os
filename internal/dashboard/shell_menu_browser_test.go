//go:build !windows

package dashboard

import "testing"

// SEARCH OR JUMP TO (spec section 1, "Search"; step 20). On the real page:
// Ctrl K opens it with the line that nothing typed goes to them; every one
// of Settings' sections is reached through it by its label, and by the
// words people use for it; a belief the page holds and a record entry the
// record finds are reached in their groups; arrows and Enter choose, Esc
// closes and gives focus back; G then S, and G then A, go to a place; ?
// lists the keys; and typing in it sends nothing to the identity.
const shellMenuJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
import { SECTIONS } from '/views/settings.js';
const W = (await import('/strings/shell.js')).default;
const sockets = window.__sockets, sent = window.__sent;
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
// The record's answer to a search, over the page's own route.
const realFetch = window.fetch.bind(window);
let recordAsks = 0;
window.fetch = (url, init) => {
  const u = new URL(String(url), location.href);
  if (u.pathname === '/record' && u.searchParams.get('q')) {
    recordAsks++;
    return Promise.resolve(new Response(JSON.stringify({ version: 1, rows: [{ seq: '77', time: '2026-10-30T09:00:00Z', type: 'belief.upsert', ring: 2, bytes: 40, head: '{"statement":"A deploy goes to staging first."}' }], first_view_bytes: 0, range_bytes: 0 }), { status: 200 }));
  }
  return realFetch(url, init);
};
const key = (k, more) => document.dispatchEvent(new KeyboardEvent('keydown', Object.assign({ key: k, bubbles: true }, more)));
const dialog = () => document.querySelector('dialog.c-menu:not(.c-menu--keys)');
// Search is fetched once the shell has started (shell/keys.js), so a key
// that opens it is answered when it is in.
const open = async () => { key('k', { ctrlKey: true }); await until(() => dialog() && dialog().open, 3000).catch(() => {}); };
const field = () => dialog().querySelector('.c-menu__field');
const type = async text => { field().value = text; field().dispatchEvent(new Event('input', { bubbles: true })); await tick(); };
const rows = () => [...dialog().querySelectorAll('.c-menu__row')];
const groupOf = title => [...dialog().querySelectorAll('.c-menu__group')].find(g => norm(g.querySelector('.c-menu__title').textContent) === title);
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 1, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  sockets[0].say({ type: 'identity', identity: { beliefs: [{ id: 'b1', statement: 'A deploy goes to staging first.', ring: 3, status: 'confirmed', evidence_count: 2, confidence: 0.8 }], intentions: [], experiences: [] } });
  S.go('chat'); await tick();
  const search = document.querySelector('.c-top-bar__search');
  check(search, 'the bar has no Search');
  search.focus();
  await open();
  check(dialog() && dialog().open && document.activeElement === field(), 'Ctrl K does not open Search with its field focused');
  check(norm(dialog().querySelector('.c-menu__note').textContent) === 'Nothing typed here goes to Mira.', 'Search does not say nothing typed goes to them');
  const before = sent.length;

  // Every section of Settings, by its label: where it lands once an old
  // section id is redirected (a group's first takes the group's address, a
  // section after it adds its own; the folders and the tools keep theirs
  // until piece three), written from the design rather than read from the
  // page, and drawn there. Technical's forms left Settings' sections in step
  // 19; Search finds them below, as Technical's panels.
  const LANDS = { substrate: '#/settings/model', providers: '#/settings/model/providers', speech: '#/settings/voice', dashboard: '#/settings/access', backups: '#/settings/backups', sandbox: '#/settings/sandbox', tools: '#/settings/tools', messages: '#/settings/people', updates: '#/settings/app' };
  check(SECTIONS.length === Object.keys(LANDS).length && SECTIONS.every(([id]) => LANDS[id]), 'the sections are not the ones this test expects: ' + SECTIONS.map(s => s[0]).join(','));
  for (const [id, label] of SECTIONS) {
    if (!dialog().open) { await open(); }
    await type(label);
    // Search fetches Settings for its sections when it first opens.
    const find = () => { const go = groupOf(W.groupGoTo); return go && [...go.querySelectorAll('.c-menu__row')].find(r => norm(r.textContent) === W.settings + ' · ' + label); };
    await until(() => find(), 3000).catch(() => {});
    const row = find();
    check(row, 'Settings → ' + label + ' is not found by its label');
    if (!row) continue;
    row.click();
    // Settings' first opening waits for its modules and sheets.
    const section = () => document.querySelector('#settings-stack[data-section="' + id + '"], #settings-stack [data-section="' + id + '"]');
    const isDrawn = () => section() && norm(section().textContent) !== '';
    await until(() => location.hash === LANDS[id] && S.view === 'settings' && isDrawn(), 8000).catch(() => {});
    check(location.hash === LANDS[id] && S.view === 'settings', 'Settings → ' + label + ' is not reached: ' + location.hash + ' ' + S.view + ', not ' + LANDS[id]);
    check(isDrawn(), 'Settings → ' + label + ' is reached but not drawn');
  }
  // And by the words people use.
  for (const [words, id] of [['microphone', 'speech'], ['api key', 'providers'], ['theme', 'updates'], ['snapshot', 'backups']]) {
    await open();
    await type(words);
    const go = groupOf(W.groupGoTo);
    const want = SECTIONS.find(s => s[0] === id)[1];
    const hit = go && [...go.querySelectorAll('.c-menu__row')].some(r => norm(r.textContent) === W.settings + ' · ' + want);
    check(hit, '"' + words + '" does not find Settings → ' + want);
    dialog().close(); await tick();
  }

  // Technical's panels, by their labels and by the words they went by.
  for (const [words, id, label] of [['Verification', 'verification', 'Verification'], ['witness', 'verification', 'Verification'], ['Settings in force', 'inforce', 'Settings in force'], ['every setting', 'inforce', 'Settings in force'], ['ui-layout', 'layout', 'Layout'],
    ['dreaming', 'reflection', 'Reflection'], ['log files', 'logs', 'Logs'], ['compression', 'storage', 'Storage'], ['recent turns', 'limits', 'Turn and tool limits'], ['tokens', 'limits', 'Turn and tool limits']]) {
    if (!dialog().open) { await open(); }
    await type(words);
    const find = () => { const go = groupOf(W.groupGoTo); return go && [...go.querySelectorAll('.c-menu__row')].find(r => norm(r.textContent) === W.settings + ' · Technical · ' + label); };
    await until(() => find(), 3000).catch(() => {});
    const row = find();
    check(row, '"' + words + '" does not find Technical → ' + label);
    if (!row) continue;
    row.click();
    await until(() => location.hash === '#/settings/technical/' + id && S.view === 'technical', 8000).catch(() => {});
    check(location.hash === '#/settings/technical/' + id && S.view === 'technical', 'Technical → ' + label + ' is not reached: ' + location.hash + ' ' + S.view);
  }

  // Helpers is found when there are roles to list.
  if (!dialog().open) { await open(); }
  const helpers = () => { const go = groupOf(W.groupGoTo); return go && [...go.querySelectorAll('.c-menu__row')].find(r => norm(r.textContent).endsWith(' · Helpers')); };
  await type('agency');
  await tick();
  check(!helpers(), '"agency" finds Helpers with no roles to list');
  S.config = { agency: { roles: [{ name: 'critic' }] } };
  await type('agency ');
  await until(() => helpers(), 3000).catch(() => {});
  check(helpers(), '"agency" does not find Technical → Helpers');
  S.config = null;
  if (dialog().open) dialog().close();
  await tick();

  // About's tabs, Experiences among them.
  await open();
  await type('experiences');
  const exp = groupOf(W.groupGoTo) && [...groupOf(W.groupGoTo).querySelectorAll('.c-menu__row')].find(r => norm(r.textContent).endsWith(' · Experiences'));
  check(exp, 'About → Experiences is not in Go to');
  if (exp) { exp.click(); await tick(); await tick(); check(location.hash === '#/identity/experiences', 'About → Experiences is not reached: ' + location.hash); }
  if (dialog().open) dialog().close();
  await tick();

  // A belief the page holds, and the record's entry, in their groups;
  // arrows and Enter choose.
  await open();
  await type('staging');
  await until(() => groupOf(W.groupRecord), 3000).catch(() => {});
  const believes = groupOf('What Mira believes');
  check(believes && norm(believes.textContent).includes('A deploy goes to staging first.'), 'the belief the page holds is not found under what they believe');
  check(groupOf(W.groupRecord) && recordAsks > 0, 'the record is not searched for what was typed');
  // The entry is named by its kind and its first words, never its number or its type (first-session review, change 3).
  const found = norm((groupOf(W.groupRecord) || {}).textContent || '');
  check(found.includes('Came to believe · A deploy goes to staging first.') && !/#77|belief\.upsert/.test(found), 'the record\'s entry is not named by its kind and first words: ' + found);
  const firstRow = rows()[0];
  check(firstRow && firstRow.getAttribute('aria-selected') === 'true', 'the first result is not the one chosen');
  field().dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); await tick();
  check(rows()[1] && rows()[1].getAttribute('aria-selected') === 'true', 'ArrowDown does not move to the next result');
  field().dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true })); await tick();
  field().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); await tick();
  check(location.hash === '#/identity/beliefs/b1', 'Enter does not open the belief chosen: ' + location.hash);

  // Esc closes and gives focus back to what opened it.
  S.go('chat'); await tick();
  search.focus(); search.click(); await tick();
  check(dialog().open, 'the bar\'s Search does not open it');
  // A trusted Escape where the engine's driver can press one (WebKit);
  // elsewhere the dialog is closed as Esc would close it, by itself.
  if (window.__pressKey) await window.__pressKey('Escape');
  else { field().dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); dialog().close(); }
  await until(() => !dialog().open, 2000).catch(() => {});
  check(!dialog().open && document.activeElement === search, 'closing does not give focus back to Search: ' + (document.activeElement && document.activeElement.className));

  // G then S, G then A; ? lists the keys.
  document.activeElement && document.activeElement.blur();
  const settingsFirst = (await import('/registry.js')).TABS.settings[0][1];
  key('g'); key('s'); await until(() => S.view === settingsFirst, 2000).catch(() => {});
  check(location.hash === '#/settings' && S.view === settingsFirst, 'G then S does not open Settings at its first tab: ' + location.hash + ' ' + S.view);
  key('g'); key('a'); await until(() => S.view === 'identity', 2000).catch(() => {});
  check(location.hash.startsWith('#/identity') && S.view === 'identity', 'G then A does not go to About: ' + location.hash);
  key('?'); await until(() => document.querySelector('dialog.c-menu--keys[open]'), 3000).catch(() => {});
  const keys = document.querySelector('dialog.c-menu--keys');
  check(keys && keys.open && norm(keys.textContent).includes(W.keysTitle) && keys.querySelectorAll('dt').length >= 9, 'the keys are not listed by ?');
  keys && keys.close();

  // The board's actions, in its order; Restart asks first, in place; Stop
  // only while a turn runs; the footer's keys and Esc at the field's edge;
  // Recent, at rest, holds what was chosen in this browser.
  await open();
  const acts = () => [...((groupOf(W.groupActions) || document.createElement('div')).querySelectorAll('.c-menu__label'))].map(l => norm(l.textContent));
  check(acts().join('|') === [W.actionUpdates, W.actionRestart, W.actionKeys, W.actionAnotherDevice, W.actionModel, W.actionBackup, W.actionTheme].join('|'), 'the actions are not the board\'s, in its order: ' + acts().join('|'));
  const hints = [...dialog().querySelectorAll('.c-menu__foot .c-menu__hint')].map(x => norm(x.textContent)).join(' · ');
  check(norm((dialog().querySelector('.c-menu__esc') || {}).textContent) === 'Esc' && hints === '↑ ↓ ' + W.footMove + ' · Enter ' + W.footOpen, 'the footer or the Esc key is not the board\'s: ' + hints);
  const firstTitle = norm((dialog().querySelector('.c-menu__title') || {}).textContent);
  check(firstTitle === W.groupRecent && norm((groupOf(W.groupRecent) || {}).textContent).includes('A deploy goes to staging first.'), 'Recent is not first, at rest, with what was chosen: ' + firstTitle);
  const sentBefore = sent.length;
  const restart = rows().find(r => norm(r.textContent).startsWith(W.actionRestart));
  if (restart) { restart.click(); await tick(); }
  check(dialog().open && rows().some(r => norm(r.textContent) === W.actionRestartAsk) && !sent.slice(sentBefore).some(m => m.type === 'restart'), 'Restart did not ask first');
  const ask = rows().find(r => norm(r.textContent) === W.actionRestartAsk);
  if (ask) { ask.click(); await tick(); }
  check(sent.slice(sentBefore).some(m => m.type === 'restart') && !dialog().open, 'Restart, asked again, did not restart');
  S.thinking = true;
  await open();
  check(acts()[0] === W.actionStop, 'Stop the current turn is not first while a turn runs: ' + acts()[0]);
  const stop = rows().find(r => norm(r.textContent).startsWith(W.actionStop));
  if (stop) { stop.click(); await tick(); }
  check(sent.slice(sentBefore).some(m => m.type === 'cancel'), 'Stop the current turn did not stop it');
  S.thinking = false;
  if (dialog().open) dialog().close();

  // Nothing typed in it went to the identity.
  const said = JSON.stringify(sent.slice(before)).toLowerCase();
  // The free words typed; a section's label is also the name of the read
  // its page asks for on arrival, which is navigation, not what was typed.
  const leaked = ['staging', 'microphone', 'api key', 'snapshot'].filter(t => said.includes(t));
  check(!leaked.length, 'typing in Search sent the identity what was typed: ' + leaked.join(', '));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestSearchReachesEverySettingAndSendsNothing(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, shellMenuJS, append([]string{"shell/menu.css", "components/top-bar/top-bar.css"}, homeStyles...))
}

// A RECORD READ IS DRAWN ONLY FOR THE QUERY AND THE OPENING THAT ASKED IT
// (step 20, round two). On the real page, with the record's reads held
// until the page lets each answer: a read for the current query is drawn;
// one held while the field went short, or empty and then short, is not
// drawn when it answers, success or refusal; nor is one held while the
// sheet was closed (by Esc where the engine can press it, otherwise as Esc
// closes it) and opened again; and a read still waiting on the pause in
// typing when the sheet closes is never asked.
const shellMenuHeldJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const W = (await import('/strings/shell.js')).default;
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
const realFetch = window.fetch.bind(window);
const held = [];
window.fetch = (url, init) => {
  const u = new URL(String(url), location.href);
  if (u.pathname === '/record' && u.searchParams.get('q')) return new Promise((ok, no) => held.push({ q: u.searchParams.get('q'), ok, no }));
  return realFetch(url, init);
};
const found = () => new Response(JSON.stringify({ version: 1, rows: [{ seq: '77', time: '2026-10-30T09:00:00Z', type: 'belief.upsert', ring: 2, bytes: 40, head: '{}' }], first_view_bytes: 0, range_bytes: 0 }), { status: 200 });
const key = (k, more) => document.dispatchEvent(new KeyboardEvent('keydown', Object.assign({ key: k, bubbles: true }, more)));
const dialog = () => document.querySelector('dialog.c-menu:not(.c-menu--keys)');
const field = () => dialog().querySelector('.c-menu__field');
const type = async text => { field().value = text; field().dispatchEvent(new Event('input', { bubbles: true })); await tick(); };
const groupOf = title => [...dialog().querySelectorAll('.c-menu__group')].find(g => norm(g.querySelector('.c-menu__title').textContent) === title);
const recordShown = () => !!groupOf(W.groupRecord);
const asked = async n => { await until(() => held.length >= n, 2000).catch(() => {}); return held[n - 1]; };
const answered = async () => { await tick(); await tick(); };
const open = async () => { key('k', { ctrlKey: true }); await until(() => dialog() && dialog().open, 2000).catch(() => {}); };
const closeAsEsc = async () => {
  if (window.__pressKey) await window.__pressKey('Escape'); else dialog().close();
  await until(() => !dialog().open, 2000).catch(() => {});
};
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  S.go('chat'); await tick();
  await open();

  // The current query's read is drawn: what follows is not passing for want of one.
  await type('staging'); let r = await asked(1); if (r) r.ok(found());
  await until(recordShown, 2000).catch(() => {});
  check(recordShown(), "the current query's record read is not drawn");

  const short = async (via, answer, what) => {
    await type('staging'); const n = held.length + 1; const read = await asked(n);
    for (const t of via) await type(t);
    if (read) { if (answer === 'ok') read.ok(found()); else read.no(new Error('refused')); }
    await answered();
    check(!recordShown(), what);
  };
  await short(['xy'], 'ok', 'a held read for a longer query was drawn under a short one');
  await short(['xy'], 'no', 'a held refusal for a longer query was drawn under a short one');
  await short(['', 'xy'], 'ok', 'a held read was drawn after the field was emptied and typed short');
  await short(['', 'xy'], 'no', 'a held refusal was drawn after the field was emptied and typed short');

  for (const answer of ['ok', 'no']) {
    await type('staging'); const read = await asked(held.length + 1);
    await closeAsEsc();
    await open(); await type('xy');
    if (read) { if (answer === 'ok') read.ok(found()); else read.no(new Error('refused')); }
    await answered();
    check(!recordShown(), answer === 'ok' ? 'a read held across closing and opening again was drawn under the new query' : 'a refusal held across closing and opening again was drawn under the new query');
  }

  const before = held.length;
  field().value = 'staging'; field().dispatchEvent(new Event('input', { bubbles: true }));
  await closeAsEsc();
  // waits by design: past the pause in typing (250 ms), when a read still
  // waiting on it would have been asked.
  await new Promise(r => setTimeout(r, 400));
  check(held.length === before, 'a read still waiting on the pause was asked after the sheet closed');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestSearchDrawsARecordReadOnlyForTheQueryAndOpeningThatAskedIt(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, shellMenuHeldJS, append([]string{"shell/menu.css", "components/top-bar/top-bar.css"}, homeStyles...))
}

// A KEY PRESSED BEFORE SEARCH IS IN HOLDS ONLY ITS MOMENT'S INTENT, as a
// Settings door does (step 22, round one). On the real page, with the
// shipped Search held from arriving until the page lets it: Ctrl K with
// nothing after it opens Search when it arrives; Ctrl K and then a move to
// Projects does not, the page stays on Projects, and Ctrl K once Search is
// in opens it at once; ? and then away and back does not open the keys.
const searchHeldJS = `export * from './menu-real.js';
await new Promise(r => { window.__releaseSearch = r; });
`

func searchDoorJS(mode string) string {
	return `import { report, until } from '/__harness.js';
import { S } from '/state.js';
import { fetchMenu } from '/shell/keys.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const key = (k, more) => document.dispatchEvent(new KeyboardEvent('keydown', Object.assign({ key: k, bubbles: true }, more)));
const isOpen = sel => !!document.querySelector(sel + '[open]');
const SEARCH = 'dialog.c-menu:not(.c-menu--keys)', KEYS = 'dialog.c-menu--keys';
// Search in, and every step its arrival set off taken: a task later.
const arrived = async () => { window.__releaseSearch(); await fetchMenu(); await new Promise(r => setTimeout(r, 0)); };
const mode = '` + mode + `';
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  await until(() => S.view === 'chat' && window.__releaseSearch, 4000);
  if (mode === 'kept') {
    key('k', { ctrlKey: true });
    await arrived();
    await until(() => isOpen(SEARCH), 4000).catch(() => {});
    check(isOpen(SEARCH), 'Search, asked for with nothing after it, did not open when it arrived');
  }
  if (mode === 'left') {
    key('k', { ctrlKey: true });
    S.go('projects');
    await arrived();
    check(!isOpen(SEARCH), 'Search, asked for before a move to Projects, opened over Projects when it arrived');
    check(S.view === 'projects', 'the page left Projects when Search arrived: ' + S.view);
    key('k', { ctrlKey: true });
    await until(() => isOpen(SEARCH), 4000).catch(() => {});
    check(isOpen(SEARCH), 'Ctrl K, once Search was in, did not open it');
  }
  if (mode === 'keys') {
    key('?');
    S.go('projects'); S.go('chat');
    await arrived();
    check(!isOpen(KEYS), 'the keys, asked for before leaving the conversation and coming back, opened when Search arrived');
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`
}

func TestAKeyPressedBeforeSearchIsInHoldsOnlyItsMomentsIntent(t *testing.T) {
	real, err := staticFS.ReadFile("static/shell/menu.js")
	if err != nil {
		t.Fatal(err)
	}
	served := map[string][]byte{"/shell/menu.js": []byte(searchHeldJS), "/shell/menu-real.js": real}
	styles := append([]string{"shell/menu.css", "components/top-bar/top-bar.css"}, homeStyles...)
	for _, c := range []struct{ name, mode string }{
		{"with nothing after it", "kept"},
		{"then a move to Projects", "left"},
		{"the keys, then away and back", "keys"},
	} {
		t.Run(c.name, func(t *testing.T) {
			runRealPageServing(t, statusFakeSocketJS, searchDoorJS(c.mode), styles, served)
		})
	}
}
