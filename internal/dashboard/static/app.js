
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
import { restoreDraft } from './overlay.js';

function setHash(h) {
  try { if (location.hash !== h) location.hash = h; } catch (err) {}
}
export function go(v) {
  const asked = parseHash(location.hash);
  if (v === S.view && asked.view && asked.view !== v && TITLES[asked.view]) v = asked.view;
  if (S.view === 'chat' && v !== 'chat') S.saveInteractionPosition?.();
  if (S.view === 'settings' && v !== 'settings') forgetBackupsSecrets();
  S.view = v;
  const cur = location.hash || '';
  if (v === 'settings') setHash(S.settingsAddress?.() || (cur.startsWith('#/settings/') ? cur : '#/settings'));
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
  try { localStorage.setItem('aii.view', v); } catch (err) {}
  renderFirstbootVisibility();
  if (!S.connected) return;
  requestView(v);
  renderView(v);
  renderPanel();
}
S.go = go;
S.refreshCurrentView = read => { if (S.connected) requestView(S.view, read); };

document.querySelectorAll('.nav-item').forEach(el => { el.onclick = () => go(el.dataset.view); });
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
S.renderUpdateChip = () => {
  const el = document.getElementById('nav-update');
  if (!el) return;
  const u = S.update;
  let text = '';
  if (u && u.needs_restart) text = 'Restart to update';
  else if (u && u.available_version) text = 'Update ' + u.available_version;
  const t = document.getElementById('nav-update-text');
  if (text && (el.hidden || !t || t.textContent !== text)) announce(text);
  el.hidden = !text;
  if (t) t.textContent = text;
  el.title = u && u.needs_restart ? 'Version ' + u.installed_version + ' is installed — relaunch to run it'
    : (text ? (u.stage_refusal ? 'Version ' + u.available_version + ' is available — install it with your package manager' : 'A newer release is available') : '');
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
    viewProject(route.project);
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
  if (v === 'settings' && route.project) S.settingsSection?.(route.project);
  go(v);
  if (route.view === 'projects' && route.project) {

    import('./views/projects.js').then(m => m.viewProject(route.project));
  }
})();

if (typeof S.initThemeSwitch === 'function') S.initThemeSwitch();
