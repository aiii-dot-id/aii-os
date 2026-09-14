
import { S } from '../state.js';
import { $, esc, hueOf } from '../util.js';
import { viewProject } from './projects.js';
import { workSectionHTML, wireWork } from './work.js';

function greeting() {
  const h = new Date().getHours();
  return h < 5 ? 'Working late' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening';
}
const ICO = {
  chat: '<path d="M3 4.5A1.5 1.5 0 0 1 4.5 3h7A1.5 1.5 0 0 1 13 4.5v5a1.5 1.5 0 0 1-1.5 1.5H7l-3 2.5V11A1.5 1.5 0 0 1 3 9.5z"/>',
  memory: '<ellipse cx="8" cy="4.3" rx="5" ry="2"/><path d="M3 4.3v7.4c0 1.1 2.2 2 5 2s5-.9 5-2V4.3M3 8c0 1.1 2.2 2 5 2s5-.9 5-2"/>',
  brief: '<path d="M4 2h5.5L13 5.5V14H4z"/><path d="M6 8h4M6 11h4"/>',
  projects: '<circle cx="8" cy="8" r="5.5"/><circle cx="8" cy="8" r="2"/>',
  ring: '<circle cx="8" cy="8" r="5.5"/>',
  chev: '<path d="m6 4 4 4-4 4"/>',
  lock: '<rect x="3.5" y="7" width="9" height="6.5" rx="1.5"/><path d="M5.8 7V5.2a2.2 2.2 0 0 1 4.4 0V7"/>',
  mic: '<rect x="6" y="1.8" width="4" height="7.2" rx="2"/><path d="M3.8 8a4.2 4.2 0 0 0 8.4 0M8 12.2v2"/>',
  shield: '<path d="M8 1.8 3 3.8v4c0 3 2.2 5.2 5 6.2 2.8-1 5-3.2 5-6.2v-4z"/><path d="m6 8 1.5 1.5L10.2 6.4"/>',
};
const ico = (n) => '<svg class="hico" viewBox="0 0 16 16" aria-hidden="true">' + ICO[n] + '</svg>';

function navTo(view) {
  const el = document.querySelector('.nav-item[data-view="' + view + '"]');
  if (el) el.click();
}

export function ago(iso, now) {
  const t = Date.parse(iso || '');
  if (isNaN(t)) return iso ? esc(iso) : '';
  const s = Math.max(0, Math.round(((now || Date.now()) - t) / 1000));
  if (s < 60) return 'just now';
  if (s < 3600) return Math.round(s / 60) + 'm ago';
  if (s < 86400) return Math.round(s / 3600) + 'h ago';
  return Math.round(s / 86400) + 'd ago';
}

export function ringRows(id, work) {
  const beliefs = (id && id.beliefs) || [];
  const loaded = !!id;
  const count = (ring) => beliefs.filter(b => Number(b.ring) === ring).length;
  const live = work && Array.isArray(work.live) ? work.live.length : 0;
  const queued = work && work.queued ? work.queued : 0;
  const n = (v, word) => loaded ? (v + ' ' + word) : '…';
  return [
    { ring: 0, name: 'Axioms', right: 'signed bundle', tail: 'lock', view: 'identity' },
    { ring: 1, name: 'Charter', right: loaded ? (id.charter ? 'minted' : 'not yet minted') : '…', tail: 'lock', view: 'identity' },
    { ring: 2, name: 'Core beliefs', right: n(count(2), 'promoted'), tail: 'chev', view: 'memory' },
    { ring: 3, name: 'Working truth', right: n(count(3), 'held'), tail: 'chev', view: 'memory' },
    { ring: 4, name: 'Working memory', right: live + ' live' + (queued ? ' · ' + queued + ' queued' : ''), tail: 'chev', view: 'projects' },
    { ring: 5, name: 'Firewall', right: 'signed bundle', tail: 'lock', view: 'identity' },
  ];
}
const RING_COLOR = ['var(--acc)', 'var(--acc2)', 'var(--good)', 'var(--warn)', 'var(--acc3)', 'var(--bad)'];

function ringsHTML() {
  const rows = ringRows(S.identity, S.work).map(r =>
    '<button class="ring-row" data-go="' + r.view + '" type="button">' +
    '<span class="ring-ico" style="color:' + RING_COLOR[r.ring] + '">' + ico('ring') + '</span>' +
    '<span class="ring-n" style="color:' + RING_COLOR[r.ring] + '">RING ' + r.ring + '</span>' +
    '<span class="ring-name">' + esc(r.name) + '</span>' +
    '<span class="ring-right">' + esc(r.right) + '</span>' +
    '<span class="ring-tail">' + ico(r.tail) + '</span></button>').join('');
  return '<div class="card home-card"><div class="home-card-h"><h3>THE RINGS</h3><button class="btn ghost sm" data-go="identity" type="button">Identity</button></div>' + rows + '</div>';
}

function briefHTML() {
  const id = S.identity;
  if (!id) return '';
  const text = (id.brief || '').trim();
  return '<div class="card home-card"><div class="home-card-h"><h3>WHO THEY ARE RIGHT NOW</h3>' +
    (text ? '<button class="btn ghost sm" data-go="identity" type="button">Read in full</button>' : '') + '</div>' +
    (text ? '<div class="home-brief">' + esc(text) + '</div>'
          : '<div class="home-empty">No brief yet — one is written after the first night\'s consolidation.</div>') + '</div>';
}

function recentlyHTML() {
  const id = S.identity;
  const xs = ((id && id.experiences) || []).slice(0, 4);
  let body;
  if (!id) body = '<div class="home-empty">…</div>';
  else if (!xs.length) body = '<div class="home-empty">Nothing recorded yet.</div>';
  else body = xs.map(x =>
    '<div class="recent-row"><span class="recent-ico">' + ico('shield') + '</span>' +
    '<div class="recent-body"><div class="recent-text">' + esc(x.content || '') + '</div>' +
    '<div class="recent-meta">' + esc(x.category || 'experience') + (x.provenance ? ' · ' + esc(x.provenance) : '') + '</div></div>' +
    '<span class="recent-when">' + ago(x.created_at) + '</span></div>').join('');
  return '<div class="card home-card"><div class="home-card-h"><h3>RECENTLY</h3><button class="btn ghost sm" data-go="memory" type="button">Memory</button></div>' + body + '</div>';
}

export function continuityLine(cont, stats) {
  if (!cont) return [];
  const mode = cont.mode || 'normal';
  const out = [];
  out.push({ cls: mode === 'safe' ? 'bad' : mode === 'degraded_witness' ? 'warn' : 'good',
    text: mode === 'safe' ? 'SAFE — record frozen' + (cont.safe_reason ? ': ' + cont.safe_reason : '')
        : mode === 'degraded_witness' ? 'degraded — witness dark' + (cont.degraded_since ? ' since ' + ago(cont.degraded_since) : '')
        : 'record sound' });
  if (cont.ledger_seq != null) out.push({ text: 'ledger seq ' + Number(cont.ledger_seq).toLocaleString() });
  if (cont.witnessed_at) out.push({ text: 'witnessed ' + ago(cont.witnessed_at) + (cont.unanchored ? ' · ' + cont.unanchored + ' unanchored' : '') });
  else out.push({ text: 'not yet witnessed' + (cont.unanchored ? ' · ' + cont.unanchored + ' unanchored' : '') });
  if (cont.review_status) out.push({ cls: cont.review_status === 'issues' ? 'warn' : '', text: 'review ' + (cont.review_status === 'issues' ? (cont.review_issues + ' issue' + (cont.review_issues === 1 ? '' : 's')) : 'clear') });
  if (stats && (stats.version || stats.build)) out.push({ text: 'build ' + esc(stats.version || 'dev') + (stats.build ? ' · ' + esc(stats.build) : '') });
  return out;
}
function continuityHTML() {
  const items = continuityLine(S.cont, S.stats);
  if (!items.length) return '';
  return '<div class="home-strip">' + items.map(i => '<span class="' + (i.cls || '') + '">' + i.text + '</span>').join('') + '</div>';
}

function heroHTML(name) {
  let sub, field = '';
  if (!S.connected) sub = 'Connecting to your identity…';
  else if (!S.identityExists) sub = 'No identity lives here yet — the first conversation, in Chat, is where one begins.';
  else sub = (name && name !== 'Unnamed' ? name : 'Your identity') + ' is here. What shall we work on together?';
  if (S.connected && S.identityExists) {
    field = '<div class="home-field"><input type="text" id="home-input" placeholder="Say what you need — it carries into Chat" autocomplete="off">' +
      '<button class="home-talk" id="home-talk" type="button" title="Hold to talk, in Chat" aria-label="Talk in Chat">' + ico('mic') + '</button></div>';
  }
  const intents = [['chat', 'chat', 'Talk'], ['memory', 'memory', 'Recall'], ['identity', 'brief', 'Read the brief'], ['projects', 'projects', 'Open the workroom']]
    .map(([view, icon, label]) => '<button class="home-intent" data-go="' + view + '" type="button"' + (S.identityExists ? '' : ' disabled') + '>' + ico(icon) + '<span>' + label + '</span></button>').join('');
  return '<div class="card home-hero"><div class="greet">' + greeting() + '.</div><div class="greet-sub">' + esc(sub) + '</div>' + field +
    '<div class="home-intents">' + intents + '</div></div>';
}

function projCardHTML(p) {
  return '<div class="proj-card' + (p.active ? ' focused' : '') + '" data-id="' + esc(p.id) + '" style="--hue:' + hueOf(p.id) + '">' +
    '<h4><span class="dot' + (p.state === 'closed' ? ' closed' : '') + '"></span>' + esc(p.name) + '</h4>' +
    '<div class="desc">' + esc(p.description || '') + '</div>' +
    (p.focus ? '<div class="proj-focus">' + esc(p.focus) + '</div>' : '') +
    '<div class="meta"><span>' + esc(p.state) + '</span><span>' + (p.active ? 'focused' : '') + '</span></div></div>';
}

export function renderHome() {
  const root = $('home-inner');
  if (!root) return;
  const name = S.identityExists && S.stats ? S.stats.name : '';
  let main = heroHTML(name);
  // Born without a name in the first words, an identity stays "Unnamed"
  // until it records one; the mechanism is theirs, the ask is the
  // operator's.
  if (S.connected && S.identityExists && (!name || name === 'Unnamed')) {
    main += '<div class="card" data-name-hint><h3>NO NAME YET</h3><div style="font-size:13px;line-height:1.6;color:var(--dim)">' +
      'Ask them what they\u2019d like to be called \u2014 the name is theirs to record (one line in <code>data/ui/name</code>), and the greeting follows on the next turn.</div></div>';
  }
  if (S.identityExists) {
    main += workSectionHTML();
    main += briefHTML();
    main += '<div class="home-h">PROJECTS</div>';
    const shownProjects = S.projects.filter(p => p.state !== 'archived'); // the shelf is not the home page
    main += shownProjects.length
      ? '<div class="cards-grid">' + shownProjects.map(projCardHTML).join('') + '</div>'
      : '<div class="empty">No projects yet — open Projects to create the first workroom you\'ll share.</div>';
  }
  let side = '';
  if (S.identityExists) side = ringsHTML() + recentlyHTML();
  root.innerHTML = '<div class="home-main">' + main + '</div>' + (side ? '<div class="home-side">' + side + '</div>' : '') + (S.identityExists ? continuityHTML() : '');

  wireWork(root);
  root.querySelectorAll('.proj-card').forEach(el => { el.onclick = () => viewProject(el.dataset.id); });
  root.querySelectorAll('[data-go]').forEach(el => { el.onclick = () => {
    navTo(el.dataset.go);
    if (el.dataset.go === 'memory') setTimeout(() => { const ms = $('mem-search'); if (ms) ms.focus(); }, 0);
  }; });
  const input = $('home-input');
  if (input) {
    input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); handOff(input.value); } });
  }
  const talk = $('home-talk');
  if (talk) talk.onclick = () => { navTo('chat'); const m = $('mic'); if (m) m.focus(); };
}

export function handOff(text) {
  navTo('chat');
  const box = $('msg-input');
  if (!box) return;
  box.value = (text || '').trim();
  box.dispatchEvent(new Event('input', { bubbles: true }));
  box.focus();
}
