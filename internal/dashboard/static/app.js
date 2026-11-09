
import { S } from './state.js';
import { $ } from './util.js';
import { announce } from './announce.js';
import { connect } from './ws.js';
import { fetchVoice } from './shell/voice-later.js';
import { initSections, sectionTitle } from './sections.js';
import { TITLES, requestView, renderView, navigated, showProject } from './views.js';
import { later } from './shell/later.js';
import { renderTitle } from './waiting.js';
import { restoreDraft } from './overlay.js';
import { route, addressOf } from './shell/router.js';
import { VIEWS, TABS, ownsThread, railOf } from './registry.js';
import * as modelSwitch from './components/model-switch/model-switch.js';
import { keepThroughReload } from './shell/reload-keep.js';
import { placeThread } from './shell/thread-place.js';
import * as topBar from './components/top-bar/top-bar.js';
import * as tabs from './components/tabs/tabs.js';
import * as osNote from './components/os-note/os-note.js';
import * as button from './components/button/button.js';
import * as sideNav from './components/side-nav/side-nav.js';
import { h, fill } from './util.js';
import { renderPageNote } from './shell/page-note.js';
import { renderMarks } from './shell/mark-place.js';
import { openSearch, fetchMenu } from './shell/keys.js';
import W from './strings/shell.js';
import WI from './strings/identity.js';
import WS from './strings/status.js';
const renderSettings = later('renderSettings'), forgetBackupsSecrets = later('forgetBackupsSecrets');

const shellBar = $('topbar');
const bar = shellBar && topBar.render({
  start: [$('bar-mark')],
  beside: [$('p-state')],
  end: [...shellBar.querySelector('.c-top-bar__end').childNodes],
  below: [$('slot-rail')],
  onSearch: openSearch,
});
if (bar) { document.querySelector('.c-rail')?.remove(); shellBar.replaceWith(bar, bar.rail); }
const placeOf = v => (VIEWS[v] && VIEWS[v].place) || '';
const nameNow = () => { const n = (S.identityExists && S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
if (!bar) for (const el of document.querySelectorAll('#nav .nav-item[data-view]')) el.addEventListener('click', () => go(el.dataset.view));
S.renderModelSwitch = modelSwitch.render;
keepThroughReload();
S.renderTopBar = () => {
  if (bar) topBar.update(bar, { name: nameNow(), active: railOf(S.view) || S.framePlace?.(S.view) });
  for (const el of document.querySelectorAll(bar ? '.nav-item[data-view^="section:"]' : '.nav-item[data-view]')) {
    const on = el.dataset.view === S.view;
    if (!bar) el.classList.toggle('active', on);
    if (on) el.setAttribute('aria-current', 'page'); else el.removeAttribute('aria-current');
  }
  renderMarks(S.view);
  renderPlaceTabs(S.view);
  renderPageNote();
  if (S.view === 'status') S.renderStatus?.();
  noteIfMoved();
};

const TAB_LABELS = {
  who: WI.tabs.who, between: WI.tabs.between, beliefs: WI.tabs.beliefs, experiences: WI.tabs.experiences, record: WI.tabs.record,
  status: WS.title, phone: W.phoneAndDevices, model: W.model, voice: W.voice, permissions: W.pluginsAndPermissions, people: W.people,
  access: W.access, backups: W.backups, app: W.app, technical: W.technical,
};
function markBoard() {
  const stage = $('place-tabs')?.closest('.stage');
  if (stage) stage.toggleAttribute('data-board', stage.dataset.view === 'status' && /^#\/settings\/?$/.test(location.hash));
}
window.addEventListener('hashchange', markBoard);
let tabsShown = null;
function renderPlaceTabs(v) {
  const host = $('place-tabs');
  if (!host) return;
  for (const id of Object.keys(VIEWS)) {
    const el = VIEWS[id].tab && (id !== v || placeOf(id) === 'settings') && $('view-' + id);
    if (el) { el.removeAttribute('role'); el.removeAttribute('aria-labelledby'); }
  }
  const place = placeOf(v);
  const stage = host.closest('.stage');
  if (stage) { stage.dataset.place = place; stage.dataset.view = v; }
  markBoard();
  const list = TABS[place];
  if (!list) { host.replaceChildren(); tabsShown = null; return; }
  const selected = (v === 'settings' && S.settingsGroup?.()) || VIEWS[v].tab;
  const name = nameNow();
  const updates = place === 'settings' ? S.pluginUpdates || 0 : 0;
  const same = tabsShown && tabsShown.place === place && host.contains(tabsShown.el) &&
    (place === 'settings' || tabsShown.name === name);
  if (place === 'settings') {
    const items = list.map(([id]) => ({ id, label: TAB_LABELS[id], href: '#/settings/' + id, count: id === 'permissions' ? updates : 0, rule: id === 'model' || id === 'technical' }));
    if (same) { if (tabsShown.selected !== selected || tabsShown.updates !== updates) sideNav.update(tabsShown.el, { title: W.settingsTabs, items, selected }); tabsShown.updates = updates; }
    else {
      const focused = tabsShown && tabsShown.el.contains(document.activeElement) ? document.activeElement.dataset.item : null;
      const el = sideNav.render({ title: W.settingsTabs, items, selected });
      host.replaceChildren(el);
      tabsShown = { place, el, name, updates };
      if (focused) el.querySelector('[data-item="' + focused + '"]')?.focus();
    }
    tabsShown.selected = selected;
    return;
  }
  if (same) {
    tabs.update(tabsShown.el, { selected });
  } else {
    const focused = tabsShown && tabsShown.el.contains(document.activeElement) ? document.activeElement.dataset.tab : null;
    const el = tabs.render({
      tabs: list.map(([id]) => ({ id, label: TAB_LABELS[id] })),
      selected, label: name ? fill(W.aboutTabs, { name }) : W.aboutTabsNoName, idPrefix: 'place-tab',
      onSelect: id => setHash('#/' + place + '/' + id),
    });
    host.replaceChildren(el);
    tabsShown = { place, el, name, updates };
    if (focused) el.querySelector('[data-tab="' + focused + '"]')?.focus();
  }
  if ($('view-' + v)) tabs.panelFor(tabsShown.el, $('view-' + v));
}

const usedBefore = (() => { try { return localStorage.getItem('aii.view') !== null; } catch (err) { return false; } })();
let noteDecided = false;
function noteIfMoved() {
  if (noteDecided || !S.stats) return;
  let seen = null;
  try { seen = localStorage.getItem('aii.seenVersion'); } catch (err) { seen = null; }
  const version = S.stats.version || 'dev';
  if (!S.stats.name || S.stats.name === '(not born)' || !usedBefore) {
    noteDecided = true;
    try { localStorage.setItem('aii.seenVersion', version); } catch (err) {}
    return;
  }
  if (seen) { noteDecided = true; return; }
  const host = $('place-note');
  if (!host) return;
  noteDecided = true;
  const name = nameNow();
  const moved = [W.movedAbout, W.movedPlugins, W.movedChat, W.movedTheme].map(line => h('li', null, fill(line, { name: name || 'them' })));
  host.replaceChildren(osNote.render({
    tone: 'info',
    live: false,
    text: W.pageChanged,
    children: [h('details', null, h('summary', null, W.pageChangedOpen), h('ul', null, moved))],
    actions: [button.render({
      label: W.pageChangedGotIt,
      onPress: () => {
        try { localStorage.setItem('aii.seenVersion', version); } catch (err) {}
        host.replaceChildren();
      },
    })],
  }));
}

let ownHashes = 0;
function setHash(h) {
  try { const was = location.hash; if (was !== h) { location.hash = h; if (location.hash !== was) ownHashes++; } } catch (err) {}
}
export function go(v, how) {
  moveOldAddress();
  const asked = parseHash(location.hash);
  if (v === S.view && asked.view && asked.view !== v && TITLES[asked.view]) v = asked.view;
  if (how !== 'refresh' || v !== S.view) navigated();
  if (S.view === 'chat' && v !== 'chat') S.saveInteractionPosition?.(v);
  if (S.view === 'settings' && v !== 'settings') forgetBackupsSecrets();
  S.view = v;
  const cur = location.hash || '';
  if (v === 'settings') setHash(S.settingsAddress?.() || (/^#\/settings\/(?!(permissions|status|phone|technical)(\/|$))/.test(cur) ? cur : '#/settings/model'));
  else if (!(route(cur) && route(cur).view === v)) setHash(addressOf(v));
  S.renderTopBar();
  document.querySelectorAll('.view').forEach(el => {
    const on = el.id === 'view-' + v;
    el.classList.toggle('on', on); el.inert = !on;
    el.setAttribute('aria-hidden', String(!on));
  });
  placeThread(v);
  S.refreshMessageText?.();
  $('crumb').textContent = TITLES[v] || sectionTitle(v) || v;
  renderTitle();
  try { localStorage.setItem('aii.view', v); } catch (err) {}
  renderFirstbootVisibility();
  if (!S.connected) return;
  requestView(v);
  renderView(v);
  askForLaterParts();
}
S.go = go;
S.refreshCurrentView = read => { if (S.connected) requestView(S.view, read); };

S.renderNavBadges = () => { if (placeOf(S.view) === 'settings') renderPlaceTabs(S.view); };
const CHIP_WITHDRAWN = 'Release withdrawn';
const CHIP_WITHDRAWN_TITLE = 'The release this program came from has been withdrawn by its publisher — see Settings → Updates';
const CHIP_WITHDRAWN_BEFORE = 'The release this program came from has been withdrawn by its publisher. ';
S.renderUpdateChip = () => {
  const el = document.getElementById('nav-update');
  if (!el) return;
  const u = S.update;
  let text = '';
  if (u && u.needs_restart) text = 'Restart to update';
  else if (u && u.available_version) text = 'Update ' + u.available_version;
  else if (u && u.running_withdrawn) text = CHIP_WITHDRAWN;
  const t = document.getElementById('nav-update-text');
  if (text && (el.hidden || !t || t.textContent !== text)) announce(text);
  el.hidden = !text;
  if (t) t.textContent = text;
  el.title = u && u.needs_restart ? 'Version ' + u.installed_version + ' is installed — relaunch to run it'
    : text === CHIP_WITHDRAWN ? CHIP_WITHDRAWN_TITLE
    : (text ? (u.running_withdrawn ? CHIP_WITHDRAWN_BEFORE : '') + (u.stage_refusal ? 'Version ' + u.available_version + ' is available — install it with your package manager' : 'A newer release is available') : '');
};
const updateChip = document.getElementById('nav-update');
if (updateChip) updateChip.onclick = () => { go('settings'); if (S.openUpdates) S.openUpdates(); };
export function renderFirstbootVisibility() {
  const firstRun = !!S.stats && !S.identityExists;
  const screen = $('firstboot');
  screen.classList.toggle('on', firstRun);
  if (firstRun && screen.parentElement !== document.body) document.body.appendChild(screen);
  const app = document.querySelector('.app');
  if (app) app.inert = firstRun;
  document.body.classList.toggle('first-run', firstRun);
  $('composer-wrap').style.display = ownsThread(S.view) && !firstRun ? '' : 'none';
}

let toastTimer = null;
export function toast(text, kind) {
  const t = $('toast');
  t.textContent = text;
  t.classList.toggle('note', kind === 'note');
  t.classList.add('show');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), 4200);
}

initSections();
restoreDraft();
connect();

let laterPartsAsked = false;
function askForLaterParts() {
  if (laterPartsAsked) return;
  laterPartsAsked = true;
  queueMicrotask(() => { fetchVoice(); fetchMenu().catch(() => {}); });
}

function moveOldAddress() {
  const r = route(location.hash);
  if (r && r.redirect) { try { history.replaceState(null, '', r.redirect); } catch (err) {} }
}

window.addEventListener('hashchange', function () {
  if (ownHashes) ownHashes--; else navigated();
  moveOldAddress();
  const route = parseHash(location.hash);

  if (route.view === 'projects' && route.project) {
    if (S.view === 'projects' && S.viewedProject === route.project) return;
    showProject(route.project, go);
  } else if (route.view === 'memory' && route.project) {
    if (S.view !== 'memory') go('memory');
    S.openBelief?.(route.project);
  } else if (route.view === 'settings' && route.project) {
    const moved = S.settingsSection?.(route.project);
    if (S.view !== 'settings') go('settings');
    else if (moved) { S.requestSettingsSection?.(); renderSettings(); }
  } else {
    if (S.view === route.view) { if (route.view === 'record') renderView('record'); return; }
    go(route.view);
  }
});

export function parseHash(h) {
  let r = route(h);
  if (r && r.redirect) r = route(r.redirect);
  return r ? { view: r.view, project: r.project } : { view: '', project: null };
}
(function restore() {
  moveOldAddress();
  const route = parseHash(location.hash);
  let v = 'chat';
  try { v = localStorage.getItem('aii.view') || 'chat'; } catch (err) {}
  if (v === 'home') v = 'chat';
  if (route.view && TITLES[route.view] && route.view !== 'projects') v = route.view;
  if (route.view === 'projects') v = 'projects';
  if (!TITLES[v]) v = 'chat';
  if (v === 'settings' && route.project) S.settingsSection?.(route.project);
  go(v);
  if (route.view === 'projects' && route.project) showProject(route.project, go);
  if (route.view === 'memory' && route.project) S.openBelief?.(route.project);
})();

if (typeof S.initThemeSwitch === 'function') S.initThemeSwitch();
