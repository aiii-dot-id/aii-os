
import { S } from './state.js';
import { $ } from './util.js';
import { announce } from './announce.js';
import { connect } from './ws.js';
import { wireMic } from './voice.js';
import { initSections, sectionTitle } from './sections.js';
import { scrollThread } from './views/chat.js';
import { TITLES, requestView, renderView } from './views.js';
import { renderSettings } from './views/settings.js';
import { viewProject } from './views/projects.js';
import { forgetBackupsSecrets } from './views/backups.js';
import { renderPanel } from './panel.js';
import { renderTitle } from './waiting.js';
import { restoreDraft } from './overlay.js';

function setHash(h) {
  try { if (location.hash !== h) location.hash = h; } catch (err) {}
}
export function go(v) {
  // AN ADDRESS THE PAGE HAS NOT YET FOLLOWED IS THE NEWEST WISH. The bar can
  // name another view than the one shown only between a change of address
  // and its hashchange; a go to the view already shown in that moment (the
  // socket's, as it opens) went to the old view and wrote its address over
  // the new one, so #/record/<seq> or #/projects/<id> was lost. The address
  // is followed instead.
  const asked = parseHash(location.hash);
  if (v === S.view && asked.view && asked.view !== v && TITLES[asked.view]) v = asked.view;
  if (S.view === 'chat' && v !== 'chat') S.saveInteractionPosition?.();
  if (S.view === 'settings' && v !== 'settings') forgetBackupsSecrets();
  S.view = v;
  const cur = location.hash || '';
  if (v === 'settings') setHash(S.settingsAddress?.() || (cur.startsWith('#/settings/') ? cur : '#/settings'));
  // A project and a row of the record are addresses of their own: going to
  // the view they are in keeps them.
  else if (!(v === 'projects' && cur.indexOf('#/projects/') === 0) && !(v === 'record' && cur.indexOf('#/record/') === 0)) setHash('#/' + v);
  document.querySelectorAll('.nav-item').forEach(el => {
    const on = el.dataset.view === v;
    el.classList.toggle('active', on);
    if (on) el.setAttribute('aria-current', 'page'); else el.removeAttribute('aria-current');
  });
  document.querySelectorAll('.view').forEach(el => {
    const on = el.id === 'view-' + v;
    el.classList.toggle('on', on); el.inert = !on;
    el.setAttribute('aria-hidden', String(!on));
  });
  S.refreshMessageText?.();
  $('crumb').textContent = TITLES[v] || sectionTitle(v) || v;
  renderTitle();
  try { localStorage.setItem('aii.view', v); } catch (err) {}
  renderFirstbootVisibility();
  if (!S.connected) return;
  requestView(v);
  renderView(v);
  renderPanel();
}
// A view may move the page (the connect card lands on Plugins) without
// importing this module: the navigator rides the shared state.
S.go = go;
// A foreground probe asks for fresh data in the current view without
// rebuilding forms or moving focus while the operator is editing.
S.refreshCurrentView = read => { if (S.connected) requestView(S.view, read); };

document.querySelectorAll('.nav-item').forEach(el => { el.onclick = () => go(el.dataset.view); });
// The Plugins item counts catalog releases newer than what is installed.
S.renderNavBadges = () => {
  const el = document.querySelector('.nav-item[data-view="plugins"]');
  if (!el) return;
  const n = S.pluginUpdates || 0;
  let b = el.querySelector('.cnt');
  if (!n) { if (b) b.remove(); return; }
  if (!b) { b = document.createElement('span'); b.className = 'cnt'; el.appendChild(b); }
  b.textContent = n;
  b.title = n + ' plugin update' + (n === 1 ? '' : 's') + ' available';
};
// The sidebar says when a release is ready, the way a browser does; the
// click lands on Settings → Updates, where Relaunch is. It also says when the
// release this program came from was withdrawn by its publisher (the row 2.4
// design note's question 6): the one update state that asks the operator to
// act, which the Updates card alone said, on a Settings page. A relaunch that
// is ready, and then an available update, say themselves first, as each is
// the way out of a withdrawn release; the update's tooltip says the release
// was withdrawn.
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
  // A control that appears is news: its words are said when it appears or
  // they change, not at every push that leaves it as it was.
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
  $('firstboot').classList.toggle('on', !!S.stats && !S.identityExists);

  $('composer-wrap').style.display = S.view === 'chat' ? '' : 'none';
}

let toastTimer = null;
export function toast(text) {
  const t = $('toast');
  t.textContent = text;
  t.classList.add('show');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), 4200);
}

initSections();
restoreDraft();
wireMic();
connect();

window.addEventListener('hashchange', function () {
  const route = parseHash(location.hash);

  if (route.view === 'projects' && route.project) {
    if (S.view === 'projects' && S.viewedProject === route.project) return;
    // Shown in this event, not a task later: a route still on its way
    // when the operator went elsewhere put its own address back over
    // theirs. The view is part of this module's own graph; there was
    // nothing to wait for.
    viewProject(route.project);
  } else if (route.view === 'settings' && route.project) {
    // A Settings section is an address too: #/settings/speech.
    const moved = S.settingsSection?.(route.project);
    if (S.view !== 'settings') go('settings');
    else if (moved) { S.requestSettingsSection?.(); renderSettings(); }
  } else {
    // A row of the record (#/record/<seq>) is drawn again on the page that
    // shows it.
    if (S.view === route.view) { if (route.view === 'record') renderView('record'); return; }
    go(route.view);
  }
});

export function parseHash(h) {
  const m = /^#\/(section:[^/]+|[a-z]+)(?:\/(.+))?$/.exec(h || '');
  if (!m) return { view: '', project: null };
  let project = null;
  if (m[2]) {
    try { project = decodeURIComponent(m[2]); } catch (err) { project = m[2]; }
  }
  return { view: m[1], project: project };
}
(function restore() {
  const route = parseHash(location.hash);
  let v = 'chat';
  try { v = localStorage.getItem('aii.view') || 'chat'; } catch (err) {}
  if (route.view && TITLES[route.view] && route.view !== 'projects') v = route.view;
  if (route.view === 'projects') v = 'projects';
  if (!TITLES[v]) v = 'chat';
  // A reload lands on the Settings section it left, not the first one.
  if (v === 'settings' && route.project) S.settingsSection?.(route.project);
  go(v);
  if (route.view === 'projects' && route.project) {

    import('./views/projects.js').then(m => m.viewProject(route.project));
  }
})();

if (typeof S.initThemeSwitch === 'function') S.initThemeSwitch();
