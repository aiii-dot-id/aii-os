import { ready } from '/section-api.js';
import { keepInSection } from './built-in/keep.js';
import { S } from './state.js';
import { useBridge, act, here } from './ws.js';
import { hearPageKeys } from './built-in/page-keys.js';
import { renderIdentity } from './views/identity.js';
import { renderMemory } from './views/memory.js';
import { renderBetween } from './views/identity/between.js';
import { renderExperiences } from './views/identity/experiences.js';
import './views/identity/beliefs-show.js';

const api = await ready();
useBridge(api);
hearPageKeys(api);
api.tokens(tokens => { for (const [name, value] of Object.entries(tokens)) document.documentElement.style.setProperty(name, value); });

const DRAW = { identity: () => renderIdentity(), memory: renderMemory, between: renderBetween, experiences: renderExperiences };
here.redraw = () => DRAW[S.view]?.();
const on = (topic, take, views) => api.data.subscribe(topic, data => { take(data); if (!views || views.includes(S.view)) here.redraw(); });

const pageLinks = () => { if (here.base) document.querySelectorAll('a[href^="#/"]').forEach(a => { a.href = here.base + a.getAttribute('href'); }); };
new MutationObserver(pageLinks).observe(document.body, { childList: true, subtree: true });
const go = to => act('page.go', { to }).catch(() => {});
document.addEventListener('click', e => {
  const a = e.target.closest?.('a[href]');
  if (a && a.hash.startsWith('#/')) { e.preventDefault(); go(a.hash); }
});
window.addEventListener('hashchange', () => { if (location.hash.startsWith('#/')) { go(location.hash); location.hash = ''; } });

function show(v) {
  document.querySelectorAll('.view').forEach(el => {
    const shown = el.id === 'view-' + v;
    el.classList.toggle('on', shown); el.inert = !shown;
    el.setAttribute('aria-hidden', String(!shown));
  });
}

await Promise.all([
  on('connection', d => { S.connected = d; if (!d) S.beliefTrailLost?.(); }, ['memory']),
  on('status', d => { S.stats = d; }, ['identity', 'between', 'experiences', 'memory']),
  on('continuity', d => { S.cont = d; }, ['identity', 'between']),
  on('identity', d => { S.identity = d; }),
  on('work', d => { S.work = d; }, ['identity']),
]);
keepInSection(api, 'about');
await api.data.subscribe('route', d => {
  here.base = d.base;
  S.view = d.view;
  show(d.view);
  pageLinks();
  const belief = /^#\/identity\/beliefs\/(.+)$/.exec(d.hash);
  const moved = d.hash !== here.hash;
  here.hash = d.hash;
  if (belief && moved) S.openBelief(decodeURIComponent(belief[1]));
  if (d.view === 'identity') renderIdentity(true); else here.redraw();
});
