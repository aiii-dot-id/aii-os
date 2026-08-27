/* app.js — boot, view routing, and top-level frame wiring: the
   nav rail, the crumb, firstboot visibility, the toast, the restore-
   last-view boot path. Entry module — everything else is reached
   from here. R66 (UP2): the frame shell (UI_FRAME.md §2). The slot/
   layout renderer lives in sections.js; go() routes the dynamic
   "section:<id>" views it mounts exactly like the built-in views —
   frame stays a complete dashboard alone (the lifeboat invariant). */
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

/* =========================================================
   AII OS UI v2 — state, socket, router, renderers.
   The wire contract is the dashboard WS protocol; everything
   shown is real state (R48).
   ========================================================= */

/* --- router ------------------------------------------------- */
const TITLES = { home:'Home', chat:'Chat', projects:'Projects', memory:'Memory', identity:'Identity', plugins:'Plugins', settings:'Settings' };
/* setHash — the URL is the router's state, written only when it
   differs (an identical assignment fires no event, but this also
   keeps go() from flattening "#/projects/<id>" to "#/projects" when
   viewProject calls go on its way to a deeper hash). */
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
  renderPanel(); // the default panel re-renders on view change — section mounts may claim or free the slot
}
document.querySelectorAll('.nav-item').forEach(el => { el.onclick = () => go(el.dataset.view); });
export function renderFirstbootVisibility() {
  $('firstboot').classList.toggle('on', !S.identityExists); // it lives inside the chat view; the view system owns the rest
  /* The composer shows whenever the chat view does. Before birth there
     is no one to type to — the substrate answers as itself, in a system
     line, pointing at the form (operator ruling 2026-08-20: there is no
     pre-birth conversation). It stays visible because that reply is how
     the operator discovers the form, not because anyone is listening. */
  $('composer-wrap').style.display = S.view === 'chat' ? '' : 'none';
}

/* --- toast -------------------------------------------------- */
let toastTimer = null;
export function toast(text) {
  const t = $('toast');
  t.textContent = text;
  t.classList.add('show');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), 4200);
}

/* --- boot --------------------------------------------------- */
initSections(); // slot containers ready before the socket can deliver a layout
restoreDraft(); // W3: a draft persisted across an overlay-driven reload lands back in the composer
wireMic();      // the composer's microphone; stays disabled until status says a speech endpoint exists
connect();
/* hashchange — the address IS the router. Back/forward and a pasted
   link land in routeFromHash, which performs the same act a click
   would: viewProject (select + view) for "#/projects/<id>", go(v) for
   the plain views. One gesture, one meaning — and its address. */
window.addEventListener('hashchange', function () {
  const route = parseHash(location.hash);
  /* Back/forward and pasted links land here — the same act a click
     performs. Guard: a hash echo of the state we just set (go wrote
     #/v, viewProject wrote #/projects/<id>) must not re-fire the act.
     The echo's destination always equals current state, so the guard
     is state equality, not a flag. */
  if (route.view === 'projects' && route.project) {
    if (S.view === 'projects' && S.viewedProject === route.project) return;
    import('./views/projects.js').then(m => m.viewProject(route.project));
  } else {
    if (S.view === route.view) return;
    go(route.view);
  }
});
export function parseHash(h) {
  const m = /^#\/([a-z]+)(?:\/(.+))?$/.exec(h || '');
  if (!m) return { view: '', project: null };
  return { view: m[1], project: m[2] ? decodeURIComponent(m[2]) : null };
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
    // The link is the gesture: the same select + view a click performs,
    // one paint later than the click path (the projects payload must
    // arrive before the page can render the named project).
    import('./views/projects.js').then(m => m.viewProject(route.project));
  }
})();
