
import { S } from './state.js';
import { $ } from './util.js';
import { connect, query } from './ws.js';
import { wireMic } from './voice.js';
import { initSections, sectionTitle } from './sections.js';
import { scrollThread } from './views/chat.js';
import { renderHome } from './views/home.js';
import { renderProjects } from './views/projects.js';
import { renderMemory } from './views/memory.js';
import { renderIdentity } from './views/identity.js';
import { renderPlugins } from './views/plugins.js';
import { renderSettings } from './views/settings.js';
import { renderPanel } from './panel.js';
import { restoreDraft } from './overlay.js';

const TITLES = { home:'Home', chat:'Chat', projects:'Projects', memory:'Memory', identity:'Identity', plugins:'Plugins', settings:'Settings' };

function setHash(h) {
  try { if (location.hash !== h) location.hash = h; } catch (err) {}
}
export function go(v) {
  S.view = v;
  const cur = location.hash || '';
  if (!(v === 'projects' && cur.indexOf('#/projects/') === 0)) setHash('#/' + v);
  document.querySelectorAll('.nav-item').forEach(el => el.classList.toggle('active', el.dataset.view === v));
  document.querySelectorAll('.view').forEach(el => el.classList.toggle('on', el.id === 'view-' + v));
  $('crumb').textContent = TITLES[v] || sectionTitle(v) || v;
  try { localStorage.setItem('aii.view', v); } catch (err) {}
  renderFirstbootVisibility();
  if (!S.connected) return;
  if (v === 'home') { query('status'); query('continuity'); query('projects'); query('work'); renderHome(); }
  if (v === 'projects') { query('projects'); renderProjects(); }
  if (v === 'memory') { query('identity'); renderMemory(); }
  if (v === 'identity') { query('identity'); query('continuity'); renderIdentity(); }
  if (v === 'settings') { query('config'); query('tools'); query('sandbox'); renderSettings(); }
  if (v === 'plugins') { query('config'); renderPlugins(); }
  if (v === 'chat') scrollThread(true);
  renderPanel();
}
// A view may move the page (the connect card lands on Plugins) without
// importing this module: the navigator rides the shared state.
S.go = go;

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
// click lands on Settings → Updates, where Relaunch is.
S.renderUpdateChip = () => {
  const el = document.getElementById('nav-update');
  if (!el) return;
  const u = S.update;
  let text = '';
  if (u && u.needs_restart) text = 'Restart to update';
  else if (u && u.available_version) text = 'Update ' + u.available_version;
  el.hidden = !text;
  const t = document.getElementById('nav-update-text');
  if (t) t.textContent = text;
  el.title = u && u.needs_restart ? 'Version ' + u.installed_version + ' is installed — relaunch to run it'
    : (text ? (u.stage_refusal ? 'Version ' + u.available_version + ' is available — install it with your package manager' : 'A newer release is available') : '');
};
const updateChip = document.getElementById('nav-update');
if (updateChip) updateChip.onclick = () => { go('settings'); if (S.openUpdates) S.openUpdates(); };
export function renderFirstbootVisibility() {
  $('firstboot').classList.toggle('on', !S.identityExists);

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
    import('./views/projects.js').then(m => m.viewProject(route.project));
  } else {
    if (S.view === route.view) return;
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
  go(v);
  if (route.view === 'projects' && route.project) {

    import('./views/projects.js').then(m => m.viewProject(route.project));
  }
})();

if (typeof S.initThemeSwitch === 'function') S.initThemeSwitch();
