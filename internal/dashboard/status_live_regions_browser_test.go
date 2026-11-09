//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const statusFakeSocketJS = `(function () {
  var sockets = [], sent = [];
  window.__sockets = sockets; window.__sent = sent; window.__turnActive = false;
  function Fake(url) { this.url = url; this.readyState = 0; this.binaryType = ''; sockets.push(this); }
  Fake.CONNECTING = 0; Fake.OPEN = 1; Fake.CLOSING = 2; Fake.CLOSED = 3;
  Fake.prototype.send = function (data) {
    if (typeof data !== 'string') return;
    var m = JSON.parse(data), self = this; sent.push(m);
    if (m.type === 'probe') Promise.resolve().then(function () { self.say({ type: 'probe', request_id: m.request_id, turn_active: window.__turnActive }); });
  };
  Fake.prototype.close = function () { if (this.readyState === 3) return; this.readyState = 3; if (this.onclose) this.onclose({}); };
  Fake.prototype.drop = Fake.prototype.close;
  Fake.prototype.open = function () { this.readyState = 1; if (this.onopen) this.onopen({}); };
  Fake.prototype.say = function (m) { if (this.readyState === 1 && this.onmessage) this.onmessage({ data: JSON.stringify(m) }); };
  window.WebSocket = Fake;
})();
`

const statusMeasureJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
import { wake } from '/ws.js';
import { announce } from '/announce.js';
import { renderInteractionPages } from '/views/chat.js';

const $ = id => document.getElementById(id);
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
const sockets = window.__sockets, sent = window.__sent;
const live = () => sockets[sockets.length - 1];
const say = m => live().say(m);
const idOf = (type, pick) => sent.filter(f => f.type === type && (!pick || pick(f))).at(-1).request_id;
const click = sel => document.querySelector(sel).click();
const rows = [], chatter = [], quiet = [], checks = [], focuses = [], problems = [];
const task = () => new Promise(resolve => setTimeout(resolve, 0));

const IMPLIED = { alert: 'assertive', status: 'polite', log: 'polite' };
function levelOf(el) {
  const explicit = el.getAttribute('aria-live');
  if (explicit !== null) return explicit === 'polite' || explicit === 'assertive' ? explicit : 'off';
  return IMPLIED[el.getAttribute('role')] || null;
}
function rootOf(node) {
  for (let el = node instanceof Element ? node : node && node.parentElement; el; el = el.parentElement) {
    const level = levelOf(el);
    if (level !== null) return level === 'off' ? null : el;
  }
  return null;
}
const atomicOf = el => el.getAttribute('aria-atomic') ?? '(default)';
const inTree = el => !!el && el.isConnected && el.checkVisibility({ checkVisibilityCSS: true, visibilityProperty: true }) && !el.closest('[inert],[aria-hidden="true"]');
const allRoots = () => [...document.querySelectorAll('[aria-live],[role="status"],[role="alert"],[role="log"]')].filter(el => rootOf(el) === el);
const snapshot = () => new Map(allRoots().map(el => [el, { text: norm(el.textContent), tree: inTree(el) }]));
function describe(el) {
  if (!el) return 'none';
  const data = el.getAttributeNames().find(n => n.startsWith('data-'));
  const name = el.id ? '#' + el.id : el.tagName.toLowerCase() + (el.classList[0] ? '.' + el.classList[0] : '') + (data ? '[' + data + ']' : '');
  return name + (el.getAttribute('role') ? ' role=' + el.getAttribute('role') : '') + (el.getAttribute('aria-live') ? ' live=' + el.getAttribute('aria-live') : '');
}
const VERDICT = { announced: 'ANNOUNCED', 'insert-alert': 'ANNOUNCED on insert (alert)', created: 'SILENT: region created with its text',
  hidden: 'SILENT: region not in the tree until its text', unchanged: 'SILENT: it already said this', none: 'SILENT: no live region', absent: 'NOT SHOWN' };
const RANK = ['announced', 'insert-alert', 'unchanged', 'hidden', 'created'];

async function probe(name, o) {
  const each = o.checks || [{ name, at: o.at, msg: o.msg, want: o.want, atomic: o.atomic, voice: o.voice }];
  await task(); // what was set up is over and said; the act is a task of its own, as an event is
  const before = snapshot();
  const seen = [];
  const watch = new MutationObserver(list => seen.push(...list));
  watch.observe(document.body, { subtree: true, childList: true, characterData: true });
  try { await o.act(); } catch (e) { problems.push(name + ': the driver threw: ' + ((e && e.message) || e)); }
  seen.push(...watch.takeRecords()); watch.disconnect();
  const written = new Map();
  for (const rec of seen) {
    const root = rootOf(rec.type === 'characterData' ? rec.target.parentElement : rec.target);
    if (root) written.set(root, (written.get(root) || '') + (rec.type === 'characterData' ? rec.target.data : [...rec.addedNodes].map(n => n.textContent).join('')));
  }
  for (const c of each) {
    const carried = el => norm(el.textContent).includes(c.msg);
    const hits = [];
    for (const [root, words] of written) {
      if (!norm(words).includes(c.msg) || !carried(root)) continue; // written into it, and still there
      const b = before.get(root);
      if (!b) hits.push({ code: levelOf(root) === 'assertive' ? 'insert-alert' : 'created', root });
      else if (!b.tree) hits.push({ code: 'hidden', root });
      else if (b.text.includes(c.msg)) hits.push({ code: 'unchanged', root });
      else hits.push({ code: 'announced', root });
    }
    for (const rec of seen) {
      for (const node of rec.addedNodes || []) {
        if (!(node instanceof Element)) continue;
        for (const r of [node, ...node.querySelectorAll('*')]) {
          if (rootOf(r) === r && carried(r)) hits.push({ code: levelOf(r) === 'assertive' ? 'insert-alert' : 'created', root: r });
        }
      }
    }
    const best = hits.sort((a, b) => RANK.indexOf(a.code) - RANK.indexOf(b.code))[0];
    const shown = norm(document.body.textContent).includes(c.msg);
    const line = c.at ? document.querySelector(c.at) : null;
    const b = best && before.get(best.root);
    const row = {
      name: c.name, want: c.want, code: best ? best.code : shown ? 'none' : 'absent',
      region: best ? describe(best.root) : 'none (words in ' + describe(line) + ')',
      level: best ? levelOf(best.root) : '-', atomic: best ? atomicOf(best.root) : '-',
      before: !best ? '-' : !b ? 'made by the change' : b.tree ? 'present' : 'present, not in the tree',
      text: best ? norm(best.root.textContent) : line ? norm(line.textContent) : '(' + c.at + ' is not there)',
    };
    const w = c.want;
    const met = w === 'polite' || w === 'assertive' ? row.code === 'announced' && row.level === w
      : w === 'alert' ? row.code === 'insert-alert'
      : w === 'silent' ? row.code === 'none' : false;
    if (!met) problems.push(c.name + ': wanted ' + w + ', measured ' + VERDICT[row.code] + ' (' + row.region + ')');
    if (c.atomic !== undefined && row.atomic !== c.atomic) problems.push(c.name + ': wanted aria-atomic=' + c.atomic + ', measured ' + row.atomic);
    if (c.voice === 'announce') {
      if (!hits.some(h => h.code === 'announced' && h.root.id === 'announce')) problems.push(c.name + ': wanted #announce to say it, measured ' + row.region);
      if (line && rootOf(line)) problems.push(c.name + ': its own line is a live region (' + describe(rootOf(line)) + ') beside #announce: said twice, or again at every rebuild');
    }
    rows.push(row);
  }
}

async function writes(name, sel, steps, want) {
  const el = document.querySelector(sel);
  if (!el) { problems.push(name + ': ' + sel + ' is not in the page'); return; }
  const seen = [];
  const watch = new MutationObserver(list => seen.push(...list));
  watch.observe(el, { subtree: true, childList: true, characterData: true });
  let last = norm(el.textContent), changed = 0, repeated = 0, total = 0;
  for (const step of steps) {
    await step();
    seen.push(...watch.takeRecords());
    const n = seen.length; seen.length = 0; total += n;
    const now = norm(el.textContent);
    if (n && now !== last) changed++; else if (n) repeated++;
    last = now;
  }
  watch.disconnect();
  chatter.push({ name, steps: steps.length, writes: total, changed, repeated });
  if (changed !== want || repeated !== 0) problems.push(name + ': wanted ' + want + ' changes and no repeated write, measured ' + changed + ' changes and ' + repeated + ' repeated writes');
}

function silent(name, sel, why) {
  const el = document.querySelector(sel);
  const root = el && rootOf(el);
  quiet.push({ name, region: root ? describe(root) : 'none', why });
  if (!el || root) problems.push(name + ': wanted silent, ' + (el ? 'sits in ' + describe(root) : 'is not there'));
}
function check(name, ok, detail) { checks.push({ name, ok, detail }); if (!ok) problems.push(name + ': ' + detail); }

const focusName = () => {
  const el = document.activeElement;
  if (!el || el === document.body) return 'the page';
  return describe(el) + (el.isConnected ? '' : ' (gone)') + (typeof el.selectionStart === 'number' ? ' [' + el.selectionStart + '..' + el.selectionEnd + ']' : '');
};
async function focusProbe(name, o) {
  await task();
  if (o.focus) {
    const el = document.querySelector(o.focus);
    if (!el) { problems.push(name + ': ' + o.focus + ' is not in the page'); return; }
    el.focus();
    if (o.select) el.setSelectionRange(o.select[0], o.select[1]);
  }
  const before = focusName();
  try { await o.act(); } catch (e) { problems.push(name + ': the driver threw: ' + ((e && e.message) || e)); }
  await task();
  const now = document.activeElement, target = o.want === 'page' ? document.body : document.querySelector(o.want);
  const ok = !!target && now === target && (!o.select || (now.selectionStart === o.select[0] && now.selectionEnd === o.select[1])) && (o.want === 'page' || !target.disabled);
  focuses.push({ name, before, after: focusName(), want: o.want === 'page' ? 'the page' : o.want + (o.select ? ' [' + o.select.join('..') + ']' : ''), ok });
  if (!ok) problems.push(name + ': wanted focus on ' + (o.want === 'page' ? 'the page' : o.want) + ', measured ' + focusName());
}

const pad = (s, n) => { s = String(s); return s.length > n ? s.slice(0, n - 1) + '…' : s.padEnd(n); };
function table() {
  const out = ['what the frame says                              | carried by                            | live      | atomic    | region before             | verdict                                      | the words'];
  for (const r of rows) out.push(pad(r.name, 48) + ' | ' + pad(r.region, 37) + ' | ' + pad(r.level, 9) + ' | ' + pad(r.atomic, 9) + ' | ' + pad(r.before, 25) + ' | ' + pad(VERDICT[r.code], 44) + ' | ' + pad(r.text, 60));
  out.push('', 'writes into a region                                          | steps | writes | changed | repeated');
  for (const c of chatter) out.push(pad(c.name, 61) + ' | ' + pad(c.steps, 5) + ' | ' + pad(c.writes, 6) + ' | ' + pad(c.changed, 7) + ' | ' + c.repeated);
  out.push('', 'silent by design');
  for (const q of quiet) out.push(pad(q.name, 48) + ' | ' + pad(q.region, 12) + ' | ' + q.why);
  out.push('', 'the announcer');
  for (const c of checks) out.push(pad(c.name, 62) + ' | ' + (c.ok ? 'holds' : 'BROKEN') + ' | ' + c.detail);
  out.push('', 'focus across a Settings rebuild                                | focused before                     | focused after                      | wanted');
  for (const f of focuses) out.push(pad(f.name, 62) + ' | ' + pad(f.before, 34) + ' | ' + pad(f.after, 34) + ' | ' + f.want + (f.ok ? '' : ' — NOT MET'));
  return out.join('\n');
}

const stats = over => Object.assign({ name: 'Example', version: 'test', build: 'test', ledger_seq: 7, lifetime_ticks: 3, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0, voice_state: 'setup' }, over || {});
const status = over => say({ type: 'status', stats: stats(over) });
const config = over => Object.assign({ llm: { provider: 'Anthropic', model: 'm1' }, speech: {}, prompt: { max_tokens: 8000, recent_turns: 20 }, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [],
  dashboard: { host: '127.0.0.1', port: 8181, tls: false, require_token: false }, secrets_ok: true }, over || {});
const providers = [{ name: 'Anthropic', chat: true, models: ['m1', 'm2'], default_model: 'm1', status: 'ok', endpoint: 'https://api.example.test/v1', preselect: true }];
const backupsView = { enabled: true, secrets_ok: true, safe: false, snapshots: [], backup_keep: 7, encrypting: true };
let authorized = true, tokenRefused = '';
const nativeFetch = window.fetch.bind(window);
window.fetch = async (url, opt = {}) => {
  if (url === '/auth/require' || url === '/auth/rotate') {
    if (tokenRefused) return { ok: false, status: 403, text: async () => tokenRefused, headers: new Headers() };
    return { ok: true, status: 204, headers: new Headers() };
  }
  if (url !== '/auth/token') return nativeFetch(url, opt);
  if ((opt.method || 'GET') === 'GET') return { status: authorized ? 204 : 401, ok: authorized, headers: new Headers() };
  if (opt.body === 'right-token') { authorized = true; return { status: 204, ok: true, headers: new Headers() }; }
  return { status: 401, ok: false, headers: new Headers() };
};
window.confirm = () => true;
// The places are links (the top bar); the measure moves by the page's own
// go(), which is what a place's link reaches, so each step stays synchronous.
// Settings (step 21) and Projects (step 12a) are fetched when first opened;
// the first visit waits for the view to draw, and later ones draw at once.
const goTo = async view => { S.go(view); if (view === 'settings') await until(() => document.querySelector('#settings-stack [data-section]'), 4000);
  if (view === 'projects') { await until(() => document.getElementById('projects-delivered'), 4000);
    if (!document.getElementById('projects-delivered')) problems.push('Projects did not draw within 4 s of being opened'); } };
// A section by the way in from elsewhere on the page (the section buttons
// went in 2b).
const section = name => S.openSettings(name);
// A stretch of work with n finished steps and no reply yet, as the reader draws it.
const showSteps = n => renderInteractionPages(new Map([['recorded', { identity: 'i1', incarnation: 'a1', has_older: false, has_newer: false,
  rows: Array.from({ length: n }, (_, i) => [
    { id: 'c' + i, sequence: String(2 * i + 1), kind: 'tool_call', turn_id: 't1', recorded_at: '2026-09-29T10:00:0' + i + 'Z', details: { tool: 'read' } },
    { id: 'r' + i, sequence: String(2 * i + 2), kind: 'tool_result', related_id: 'c' + i, turn_id: 't1', outcome: 'succeeded', recorded_at: '2026-09-29T10:00:0' + i + 'Z' }]).flat() }]]), () => {}, false);
const words = (n, role, content) => ({ id: 'w' + n, sequence: String(n), kind: 'message', role, content, turn_id: 't' + n, recorded_at: '2026-09-29T10:00:0' + n + 'Z' });
const answered = new Set();
function readBack(held) {
  for (let q; (q = sent.filter(f => f.type === 'query' && f.query === 'interaction').at(-1)) && !answered.has(q.request_id);) {
    answered.add(q.request_id);
    const w = q.interaction_window, at = id => held.findIndex(r => r.id === id), source = q.interaction_query.source;
    const rows = source !== 'recorded' ? [] : w.mode === 'range' ? held.slice(at(w.anchor_id), at(w.end_id) + 1)
      : w.mode === 'newer' ? held.slice(at(w.anchor_id) + 1) : w.mode === 'older' ? held.slice(0, Math.max(0, at(w.anchor_id))) : held;
    const fingerprint = 'fp-' + q.request_id;
    say({ type: 'interaction_page', request_id: q.request_id, interaction_window: { fingerprint, has_older: false, has_newer: false },
      interaction_page: { version: 1, source, identity: 'i1', incarnation: source, fingerprint, cursor: '', rows } });
  }
}

async function announcerChecks() {
  const region = $('announce');
  if (!region) { problems.push('#announce is not in the page'); return; }
  const seen = [];
  const watch = new MutationObserver(list => seen.push(...list));
  watch.observe(region, { subtree: true, childList: true, characterData: true });
  announce('The same words.'); await task(); announce('The same words.'); await task();
  seen.push(...watch.takeRecords()); watch.disconnect();
  check('the same words in two tasks are two additions, not one edit', seen.length === 2 && seen.every(r => r.type === 'childList' && r.addedNodes.length === 1 && r.addedNodes[0].nodeType === 3),
    seen.length + ' writes: ' + seen.map(r => r.type + '+' + (r.addedNodes ? r.addedNodes.length : 0)).join(', '));
  announce('First.'); announce('Second.'); announce('First.');
  check('everything announced in one task is said, once', norm(region.textContent) === 'First. Second.', 'the region reads: ' + norm(region.textContent));
  await task(); announce('Third.');
  check('a later task replaces them', norm(region.textContent) === 'Third.', 'the region reads: ' + norm(region.textContent));
  const last = region.firstChild; announce('');
  check('nothing to say writes nothing', region.firstChild === last, 'an empty announcement leaves the region as it was');
  check('the announcer is in the tree and drawn nowhere', inTree(region) && region.getBoundingClientRect().width <= 1, 'display none or a size would remove or show it');
}

async function run() {
  await until(() => sockets.length > 0);
  if (sockets.length !== 1) throw new Error('the frame did not ask for its socket at load: ' + sockets.length);

  await probe('presence: connecting, then present', { at: '#p-state', msg: 'present', want: 'polite', atomic: 'true', act: () => { sockets[0].open(); status(); } });
  say({ type: 'providers', providers });
  say({ type: 'config', config: config() });
  // The layout places one section, which SAFE holds back, so the rail's
  // note has something to say when SAFE comes.
  say({ type: 'layout', layout: { profiles: { desktop: { rail: ['held'] }, mobile: { rail: ['held'] } } } });
  await probe('a turn begins', { at: '#p-state', msg: 'thinking', want: 'polite', act: () => say({ type: 'response', stream: true }) });
  silent('the thinking indicator', '.thinking-row', 'dots, no words: the state is what #p-state says');
  showSteps(1);
  await writes('the receipt line, "Working · n steps", per step', '.receipt-label', [2, 3, 4, 5].map(n => () => showSteps(n)), 4);
  silent('the receipt line', '.receipt-label', 'changes with every step; said per step it would talk through the turn');
  await writes('#p-state across a streamed reply and three status pushes', '#p-state', [
    ...Array.from({ length: 20 }, () => () => say({ type: 'response', stream: true })),
    ...Array.from({ length: 3 }, () => () => status()),
  ], 0);
  await probe('a turn ends', { at: '#p-state', msg: 'present', want: 'polite', act: () => say({ type: 'response', role: 'identity', message: 'Done.' }) });
  // AII OS's note at the top of every page is said when it first arrives:
  // its region (#page-note) is in the page, empty, before any words.
  await probe('SAFE is entered', { checks: [
    { name: 'SAFE is entered', at: '#p-state', msg: 'SAFE — record frozen', want: 'polite', atomic: 'true' },
    { name: 'the page-wide note arrives', at: '#page-note', msg: 'AII OS has put Example in safe mode: the record failed verification.', want: 'polite' }],
    act: () => say({ type: 'continuity', continuity: { mode: 'safe', safe_reason: 'the record failed verification' } }) });
  await probe('the witness goes dark', { at: '#p-state', msg: 'degraded — witness dark', want: 'polite', act: () => say({ type: 'continuity', continuity: { mode: 'degraded_witness' } }) });
  say({ type: 'continuity', continuity: { mode: 'normal' } });
  silent('their mark', '#bar-mark', 'no state; #p-state names it');
  // The note comes with the sections' frames, fetched when the layout placed one.
  for (let i = 0; i < 200 && !document.querySelector('.sections-safe'); i++) await new Promise(r => setTimeout(r, 15));
  silent('sections suspended note', '.sections-safe', 'the same SAFE, said once');

  await probe('the connection is lost', { at: '#p-state', msg: 'offline', want: 'polite', act: () => live().drop() });
  await writes('#p-state across five failed reconnects', '#p-state', Array.from({ length: 5 }, () => () => { wake(); live().drop(); }), 0);
  await probe('the connection is restored', { at: '#p-state', msg: 'present', want: 'polite', act: () => { wake(); live().open(); } });

  // ── sign-in required ──
  await probe('sign-in is required', { at: '#dashboard-signin p', msg: 'Sign in to use this page.', want: 'polite',
    act: async () => { authorized = false; live().drop(); await until(() => $('dashboard-signin')); } });
  await probe('the token is refused', { at: '#dashboard-signin p', msg: 'That was not accepted.', want: 'polite',
    act: async () => { const box = $('dashboard-signin'); box.querySelector('input').value = 'wrong-token'; box.querySelector('form').dispatchEvent(new Event('submit', { cancelable: true }));
      await until(() => /not accepted/.test(box.textContent)); } });
  { const box = $('dashboard-signin'); box.querySelector('input').value = 'right-token'; box.querySelector('form').dispatchEvent(new Event('submit', { cancelable: true }));
    await until(() => !$('dashboard-signin') && live().readyState === 0); live().open(); }

  await probe('a provider error in Chat', { at: '#chat-error span', msg: 'The provider refused the turn: HTTP 429.', want: 'alert', act: () => say({ type: 'error', message: 'The provider refused the turn: HTTP 429.' }) });
  await probe('the next provider error, in place', { at: '#chat-error span', msg: 'The provider is unreachable.', want: 'assertive', act: () => say({ type: 'error', message: 'The provider is unreachable.' }) });
  await writes('#announce as a turn runs under a notice: the reply, once', '#announce', [
    () => say({ type: 'response', stream: true }), () => status(), () => say({ type: 'response', role: 'identity', message: 'Ready.' })], 1);
  click('#chat-error button');
  $('msg-input').value = 'Hello?'; click('#send-btn');
  await probe('a message the host did not accept', { at: '#chat-pending summary', msg: 'Not accepted — your text is kept below', want: 'polite', voice: 'announce',
    act: () => say({ type: 'chat_ack', request_id: idOf('chat'), message: 'refused' }) });
  click('#chat-pending button:last-of-type');
  $('msg-input').value = 'Still there?'; click('#send-btn');
  await probe('a message the connection was lost under', { at: '#chat-pending summary', msg: 'Connection lost — delivery unconfirmed', want: 'polite', voice: 'announce', act: () => live().drop() });
  wake(); live().open(); click('#chat-pending button:last-of-type');

  say({ type: 'error', request_id: 'nobody', message: 'The host refused the change.' });
  await probe('an error beyond Chat (toast)', { at: '#toast', msg: 'The host refused the next change.', want: 'polite', act: () => say({ type: 'error', request_id: 'nobody', message: 'The host refused the next change.' }) });

  $('msg-input').value = 'And this?'; click('#send-btn');
  await goTo('settings'); section('dashboard'); click('[data-save="dashboard"]'); goTo('chat');
  await probe('a lost connection with a message and a save unconfirmed', { checks: [
    { name: 'a lost connection: the unconfirmed message', at: '#chat-pending summary', msg: 'Connection lost — delivery unconfirmed', want: 'polite', voice: 'announce' },
    { name: 'a lost connection: the unconfirmed save', at: '[data-said="dashboard"]', msg: 'Connection lost before configuration confirmation', want: 'polite', voice: 'announce' }],
    act: () => live().drop() });
  wake(); live().open(); click('#chat-pending button:last-of-type');

  // The model they think with is chosen in Settings alone (step 4): its
  // check, its verdict and a refusal are said through #announce.
  await goTo('settings'); section('substrate');
  await probe('the substrate is being checked', { at: '.savebar .savesay', msg: 'Checking that m2 answers', want: 'polite', atomic: 'true', voice: 'announce',
    act: () => { $('cfg-model').value = 'm2'; click('[data-save="llm"]'); } });
  await probe('the substrate is verified', { at: '[data-said="llm"]', msg: 'Active. m2 answered', want: 'polite', voice: 'announce',
    act: () => say({ type: 'config', request_id: idOf('config_set'), config: config({ llm: { provider: 'Anthropic', model: 'm2', answered: true } }) }) });
  await probe('the substrate change is refused', { at: '[data-said="llm"]', msg: 'llm.model: the provider refused it', want: 'polite', voice: 'announce',
    act: () => { $('cfg-model').value = 'm1'; click('[data-save="llm"]'); say({ type: 'error', request_id: idOf('config_set'), message: 'llm.model: the provider refused it' }); } });
  goTo('chat');

  const earlier = [words(1, 'operator', 'An earlier question, from before this page.'), words(2, 'resident', 'An earlier reply, from before this page.')];
  await probe('the conversation is read back as the page loads', { checks: [
    { name: 'read back: an earlier reply', at: '#thread-inner', msg: 'An earlier reply, from before this page.', want: 'silent' },
    { name: 'read back: the operator’s earlier words', at: '#thread-inner', msg: 'An earlier question, from before this page.', want: 'silent' }],
    act: () => { say({ type: 'interaction_changed' }); readBack(earlier); } });
  const asked = words(3, 'operator', 'Is the report ready?');
  await probe('the operator’s own message, sent and recorded', { at: '#thread-inner', msg: 'Is the report ready?', want: 'silent', act: () => {
    $('msg-input').value = 'Is the report ready?'; click('#send-btn');
    say({ type: 'chat_ack', request_id: idOf('chat'), message: 'accepted' });
    say({ type: 'chat_receipt', request_id: idOf('chat'), chat_recording: { ref: { identity: 'i1', source: 'recorded', incarnation: 'recorded', id: 'w3' } } });
    readBack([...earlier, asked]); } });
  await writes('#announce across a streamed reply, each chunk carrying words', '#announce',
    Array.from({ length: 20 }, (_, i) => () => say({ type: 'response', stream: true, message: 'chunk ' + i + ' of the reply' })), 0);
  const reply = words(4, 'resident', 'The report is drafted and in Projects.');
  await probe('a reply ends the turn', { at: '#thread-inner', msg: 'Example: The report is drafted and in Projects.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'response', role: 'identity', message: reply.content }) });
  await writes('#announce as that reply is read back into the conversation', '#announce', [() => readBack([...earlier, asked, reply])], 0);
  if (!norm($('thread-inner').textContent).includes(reply.content)) problems.push('the reply read back is not in the conversation: the rows above measured nothing');
  await probe('a reply of more than one paragraph', { at: '#thread-inner', msg: 'Example: I found it in the loader.… The rest is in the conversation.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'response', role: 'identity', message: 'I found it in the loader.\n\nThe second paragraph says how, at length.' }) });
  const long = Array.from({ length: 60 }, () => 'abcdefghi').join(' ');
  await probe('a reply whose first paragraph passes the bound', { at: '#thread-inner', msg: 'Example: ' + long.slice(0, 399) + '… The rest is in the conversation.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'response', role: 'identity', message: long }) });
  const all = [...earlier, asked, reply, words(5, 'resident', 'I found it in the loader.\n\nThe second paragraph says how, at length.'), words(6, 'resident', long),
    words(7, 'resident', 'A reply given while this page was away.')];
  await probe('the conversation is read back after a reconnect', { checks: [
    { name: 'reconnect: a reply given while this page was away', at: '#thread-inner', msg: 'A reply given while this page was away.', want: 'silent' },
    { name: 'reconnect: a reply said when it was given', at: '#thread-inner', msg: 'The report is drafted and in Projects.', want: 'silent' }],
    act: () => { live().drop(); wake(); live().open(); say({ type: 'history', history: [{ role: 'identity', content: 'An earlier reply, from before this page.' }] });
      say({ type: 'interaction_changed' }); readBack(all); } });
  const woke = words(8, 'resident', 'The timer you set has come round: time to stretch.');
  await probe('words without a turn (outbox): shown, not said, not decided', { at: '#thread-inner', msg: woke.content, want: 'silent',
    act: () => { say({ type: 'outbox', outbox: [{ id: 'o1', to: 'operator', content: woke.content }] }); readBack([...all, woke]); } });

  const choose = { id: 'k1', kind: 'choose', from: 'identity', text: 'Which report first?', choices: ['Q3', 'Q4'] };
  const confirm = { id: 'k2', kind: 'confirm', from: 'gmail', plugin: 'gmail', text: 'Send the draft to ContactB', operation: 'gmail.send', effects: 'sends one email to one address',
    args: { to: 'sam@example.test', body: 'The draft, as written.' } };
  const connect = { id: 'k3', kind: 'connect', from: 'identity', connector: 'Calendar', text: 'I would like to see your week.' };
  const clarify = { id: 'k4', kind: 'clarify', from: 'identity', text: 'Which week do you mean?' };
  await probe('an ask to choose', { at: '[data-ask-id="k1"] .c-ask__text', msg: 'Example asks: Which report first?', want: 'polite', voice: 'announce',
    act: () => say({ type: 'asks', asks: [choose] }) });
  await probe('an ask to confirm', { checks: [
    { name: 'an ask to confirm', at: '[data-ask-id="k2"] .c-ask__text', msg: 'gmail: Send the draft to ContactB', want: 'polite', voice: 'announce' },
    { name: 'an ask to confirm: an argument', at: '[data-ask-id="k2"] .c-ask__args dd', msg: 'The draft, as written.', want: 'silent' },
    { name: 'an ask to confirm: another argument', at: '[data-ask-id="k2"] .c-ask__args dd + dt + dd', msg: 'sam@example.test', want: 'silent' }],
    act: () => say({ type: 'asks', asks: [choose, confirm] }) });
  await probe('an ask to connect', { at: '[data-ask-id="k3"] .c-ask__text', msg: 'Example asks: I would like to see your week.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'asks', asks: [choose, confirm, connect] }) });
  await probe('an ask to clarify', { at: '[data-ask-id="k4"] .c-ask__text', msg: 'Example asks: Which week do you mean?', want: 'polite', voice: 'announce',
    act: () => say({ type: 'asks', asks: [choose, confirm, connect, clarify] }) });
  await writes('#announce as the same asks are pushed again', '#announce', Array.from({ length: 3 }, () => () => say({ type: 'asks', asks: [choose, confirm, connect, clarify] })), 0);
  await writes('#announce as asks close', '#announce', [() => say({ type: 'asks', asks: [confirm, connect] }), () => say({ type: 'asks', asks: [] })], 0);

  // ── Settings: every line the page rebuilds is said through #announce ──
  await goTo('settings'); section('dashboard');
  await probe('a setting is being checked and saved', { at: '.savebar .savesay', msg: 'Checking and saving… the current configuration remains active.', want: 'polite', atomic: 'true', voice: 'announce', act: () => click('[data-save="dashboard"]') });
  await probe('a setting is saved', { at: '[data-said="dashboard"]', msg: 'Saved.', want: 'polite', voice: 'announce', act: () => say({ type: 'config', request_id: idOf('config_set'), config: config() }) });
  click('[data-save="dashboard"]');
  await probe('a setting is refused', { at: '[data-said="dashboard"]', msg: 'prompt.max_tokens: must be at least 1000', want: 'polite', voice: 'announce',
    act: () => say({ type: 'error', request_id: idOf('config_set'), message: 'prompt.max_tokens: must be at least 1000' }) });

  section('providers'); click('[data-prov-row="0"]');
  await probe('a provider is being saved', { at: '#settings-stack .config-result', msg: 'Saving Anthropic… waiting for the runtime to confirm.', want: 'polite', voice: 'announce', act: () => click('[data-prov-commit="0"]') });
  await probe('a provider is saved', { at: '#settings-stack .config-result', msg: 'Saved — Anthropic', want: 'polite', voice: 'announce',
    act: () => say({ type: 'providers', request_id: idOf('provider_set'), providers }) });
  click('[data-prov-row="0"]'); click('[data-prov-commit="0"]');
  await probe('a provider save is refused', { at: '#settings-stack .config-result', msg: 'provider: the endpoint does not answer', want: 'polite', voice: 'announce',
    act: () => say({ type: 'error', request_id: idOf('provider_set'), message: 'provider: the endpoint does not answer' }) });

  say({ type: 'providers', providers, broken_providers: [{ position: 1, sha256: 'abc123', name: 'Old entry', reason: 'invalid JSON at line 4', repair: 'Remove the trailing comma' }] });
  await probe('a broken provider is being repaired', { at: '#settings-stack .config-result', msg: 'Repairing Old entry… waiting for the runtime to confirm.', want: 'polite', voice: 'announce', act: () => click('[data-prov-repair="1"]') });
  await probe('a broken provider is repaired', { at: '#settings-stack .config-result', msg: 'Repaired — Old entry.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'providers', request_id: idOf('provider_repair'), providers, broken_providers: [] }) });

  section('dashboard');
  const tokenCarrier = live();
  await probe('an access token is asked for (Settings)', { at: '[data-token-require-said]', msg: 'On. Every other browser and device must now sign in.', want: 'polite', voice: 'announce',
    act: async () => { click('[data-token-require]'); await until(() => document.querySelector('[data-token-require-said]')); } });
  await until(() => live() !== tokenCarrier && live().readyState === 0);
  live().open();
  await until(() => S.connected);
  // Signing every other device out is Phone and other devices'.
  location.hash = '#/settings/phone';
  await until(() => document.querySelector('[data-sign-out-all]'));
  tokenRefused = 'This connection is not private, so the access token was not rotated.';
  await probe('signing every other device out is refused (Phone and other devices)', { at: '[data-sign-out-said]', msg: 'This connection is not private, so the access token was not rotated.', want: 'polite', voice: 'announce',
    act: async () => { click('[data-sign-out-all]'); await until(() => document.querySelector('[data-sign-out-said]')); } });
  tokenRefused = '';
  location.hash = '#/settings/all';
  await until(() => S.view === 'settings' && document.querySelector('#settings-stack [data-section]'));

  section('backups');
  say({ type: 'backups', backups: { action: 'status', view: backupsView } });
  click('#bk-take');
  await probe('a snapshot is taken', { at: '[data-said="kept"]', msg: 'A snapshot was taken and proved to restore.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'backups', request_id: idOf('backups'), backups: { action: 'take', view: backupsView } }) });
  click('#bk-take');
  await probe('a snapshot is refused', { at: '[data-said="kept"]', msg: 'the snapshot could not be proved: the disk is full', want: 'polite', voice: 'announce',
    act: () => say({ type: 'error', request_id: idOf('backups'), message: 'the snapshot could not be proved: the disk is full' }) });

  await probe('a failed restore is reported (Backups)', { at: '[data-restore-failed]', msg: 'the restore did not complete: the snapshot did not verify', want: 'polite', voice: 'announce',
    act: () => say({ type: 'backups', backups: { action: 'status', view: Object.assign({}, backupsView, { restore_failed: 'the restore did not complete: the snapshot did not verify' }) } }) });
  { const view = Object.assign({}, backupsView, { snapshots: [{ name: 'ledger-2026-09-29-seq7', at: '2026-09-29T04:00:00Z', record: 7, on_demand: false, encrypted: true }] });
    say({ type: 'backups', backups: { action: 'status', view } });
    click('[data-restore]');
    say({ type: 'backups', request_id: idOf('backups'), backups: { action: 'restore_plan', view, plan: { source: 'snapshot', at: '2026-09-29T04:00:00Z', name: 'ledger-2026-09-29-seq7', restored_to: 7, set_aside: 0, live_through: 7, witnessed: -1, witnessed_set_aside: 0, aside_under: '/data/aside', confirm_text: 'RESTORE 7' } } });
    $('bk-confirm').value = 'RESTORE 7'; $('bk-confirm').dispatchEvent(new Event('input')); click('#bk-restore'); }
  await probe('a restore is restarting (Backups)', { at: '[data-restarting] b', msg: 'Restarting to restore.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'backups', request_id: idOf('backups'), backups: { action: 'restore', restarting: true } }) });

  section('messages');
  await probe('held mail is read', { at: '[data-mail-notice]', msg: 'Nothing is waiting to be sent.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'held_mail', request_id: idOf('query', f => f.query === 'held_mail'), mail_repair: { messages: [], routes: [] } }) });
  section('messages');
  await probe('messages are read', { at: '[data-messages-notice]', msg: 'Read at', want: 'polite', voice: 'announce',
    act: () => say({ type: 'messages', request_id: idOf('query', f => f.query === 'messages'), messages: { read_ms: Date.now(), limit: 50, max_limit: 200, arrivals: [], open: [], delivered: [], notices: [], arrivals_total: 0, open_total: 0, delivered_total: 0, notices_total: 0, channels: [] } }) });
  // The notice route's switch, on the Messages card: the line about an
  // active route and a refusal of the switch are said through #announce.
  const quinn = (channel, carried) => ({ name: 'Quinn', channel, carried });
  await probe('the notice route line appears', { at: '[data-route-line]', msg: 'nothing can carry them now', want: 'polite', voice: 'announce',
    act: () => status({ notices: { on: true, lines: [quinn('telegram', false)] } }) });
  await probe('the notice route line changes', { at: '[data-route-line]', msg: 'the channel is paused', want: 'polite', voice: 'announce',
    act: () => status({ notices: { on: true, lines: [quinn('telegram', false)], held: 'the channel is paused' } }) });
  status({ notices: { on: false, lines: [quinn('telegram', true)] } });
  await probe('the notice route switch is refused', { at: '[data-notice-said]', msg: 'mark a contact line as you first', want: 'polite', voice: 'announce',
    act: () => { click('[data-notice-route-switch]'); say({ type: 'error', request_id: idOf('config_set'), message: 'dashboard.notices_off_dashboard: mark a contact line as you first' }); } });
  status({ notices: { on: false, lines: [] } });
  say({ type: 'sandbox', sandbox: { root: '/home/example', extra_roots: [] } });
  section('sandbox');
  $('sbx-add').value = '/work/project'; click('#sbx-grant');
  await probe('sandbox roots are saved', { at: '#settings-stack [role="status"], #settings-stack [data-announce]', msg: 'Sandbox roots saved.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'sandbox', request_id: idOf('sandbox_add'), sandbox: { root: '/home/example', extra_roots: ['/work/project'] } }) });
  location.hash = '#/settings/technical/storage';
  await until(() => document.getElementById('cfg-db-format'), 5000);
  await probe('a database conversion is reported (Storage)', { at: '.technical__pane p[data-announce], .technical__pane p[role="status"]', msg: 'Conversion verified: the running database is unchanged.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'config', config: config({ database: { active: 'sqlite', preferred: '', can_export: false, notice: 'Conversion verified: the running database is unchanged.' } }) }) });
  location.hash = '#/settings/updates';
  await until(() => S.view === 'settings' && document.querySelector('#settings-stack [data-section]'), 5000);
  section('updates');
  await probe('an update is available', { checks: [
    { name: 'an update is available (Settings)', at: '[data-update-state]', msg: 'Update available: 1.1.0', want: 'polite', voice: 'announce' },
    { name: 'an update is available (sidebar chip)', at: '#nav-update-text', msg: 'Update 1.1.0', want: 'polite', voice: 'announce' }],
    act: () => say({ type: 'update_check', update: { enabled: true, current_version: '1.0.0', installed_version: '1.0.0', available_version: '1.1.0', automatic: false } }) });
  await probe('the running release is withdrawn (sidebar chip)', { at: '#nav-update-text', msg: 'Release withdrawn', want: 'polite', voice: 'announce',
    act: () => say({ type: 'update_check', update: { enabled: true, current_version: '1.0.0', installed_version: '1.0.0', running_withdrawn: true, automatic: false } }) });
  // An available update is the way out of a withdrawn release: the chip
  // offers it, and its tooltip says the release was withdrawn.
  await probe('an update is available while the running release is withdrawn (sidebar chip)', { at: '#nav-update-text', msg: 'Update 1.1.0', want: 'polite', voice: 'announce',
    act: () => say({ type: 'update_check', update: { enabled: true, current_version: '1.0.0', installed_version: '1.0.0', available_version: '1.1.0', running_withdrawn: true, automatic: false } }) });
  if (!/withdrawn/.test(document.getElementById('nav-update').title)) problems.push('an update offered over a withdrawn release does not say in its tooltip that the release was withdrawn: ' + document.getElementById('nav-update').title);
  await writes('#announce as status pushes leave the update chip as it was', '#announce',
    Array.from({ length: 3 }, () => () => status({ update: { enabled: true, current_version: '1.0.0', available_version: '1.1.0', automatic: false } })), 0);
  await writes('#announce as sections are opened and configuration is pushed', '#announce', [
    () => section('backups'), () => section('messages'), () => section('updates'), () => section('dashboard'),
    ...Array.from({ length: 3 }, () => () => say({ type: 'config', config: config() }))], 0);

  // ── Settings: focus outlives a rebuild ──
  // A pressed control is disabled until its answer comes, by the rebuild
  // (Save) or by its own press (Refresh models), and a disabled control cannot
  // hold focus in either engine: focus is on the page until the answer
  // enables the control again, and then it is back on it.
  // ("A section tab, pressed" went with the tabs in 2b: a page is reached
  // by its address and the Settings list, which the stack's rebuild does
  // not redraw.)
  section('substrate');
  await focusProbe('Save, while a push rebuilds the page', { focus: '[data-save="llm"]', act: () => say({ type: 'config', config: config() }), want: '[data-save="llm"]' });
  await focusProbe('a text field, while a push rebuilds the page', { focus: '#cfg-model', select: [0, 2], act: () => say({ type: 'config', config: config() }), want: '#cfg-model' });
  await focusProbe('Save, pressed: disabled while it is checked', { focus: '[data-save="llm"]', act: () => click('[data-save="llm"]'), want: 'page' });
  await focusProbe('Save, when the answer enables it again', { act: () => say({ type: 'config', request_id: idOf('config_set'), config: config() }), want: '[data-save="llm"]' });
  section('substrate');
  const noFocusout = e => e.stopImmediatePropagation();
  window.addEventListener('focusout', noFocusout, true);
  await focusProbe('Refresh models, pressed: disabled where it is', { focus: '[data-refresh-models]', want: 'page',
    act: async () => { click('[data-refresh-models]'); await until(() => document.activeElement === document.body, 30000); } });
  await focusProbe('Refresh models, when the answer enables it again', { want: '[data-refresh-models]',
    act: () => say({ type: 'models', request_id: idOf('query', f => f.query === 'discover'), provider: 'Anthropic', model_list: ['m1', 'm2', 'm3'] }) });
  section('updates');
  await focusProbe('Check for an update, pressed: disabled where it is', { focus: '[data-update-check]', want: 'page',
    act: async () => { click('[data-update-check]'); await until(() => document.activeElement === document.body, 30000); } });
  await focusProbe('Check for an update, when the answer rebuilds the page', { want: '[data-update-check]',
    act: () => say({ type: 'update_check', update: { enabled: true, current_version: '1.0.0', installed_version: '1.0.0', available_version: '1.1.0', automatic: false } }) });
  window.removeEventListener('focusout', noFocusout, true);
  const contacts = [{ name: 'Robin', channel: 'telegram', address: '111', wake: true, operator: false }, { name: 'Ada', channel: 'sms', address: '222', wake: false, operator: true }];
  section('messages'); say({ type: 'config', config: config({ contacts }) });
  click('[data-contact-edit="1"]');
  await focusProbe('a field of the second contact, while a push rebuilds the page', { focus: '[data-contact-editor] [data-contact-name]', select: [1, 2],
    act: () => say({ type: 'config', config: config({ contacts }) }), want: '[data-contact-editor] [data-contact-name]' });
  document.activeElement.blur();

  // ── Projects: the one owner of grade state (host disposition 12.1) ──
  // These two probes drove the grade controls on Home's interim work section.
  // That section retired and the controls moved here with the state, so the
  // probes move with them: same announcement, same politeness, new owner.
  // Projects asks for work as it opens: it draws delivered work, and a list
  // that waited for the next push would show nothing until one came.
  const askedFrom = sent.length;
  await goTo('projects');
  if (!sent.slice(askedFrom).some(f => f.type === 'query' && f.query === 'work')) problems.push('Projects opened without asking for work');
  say({ type: 'work', work: { live: [], queued: 0, delivered: [{ id: 'ses-1', description: 'Draft the report', result: 'Drafted.' }] } });
  if (!document.querySelector('[data-grade-work="ses-1"]')) problems.push('a work push while Projects is open did not draw its delivered result');
  await probe('a grade is being recorded (Projects)', { at: '.grade-note', msg: 'Recording…', want: 'polite', act: () => click('[data-grade="served"]') });
  await probe('a grade is refused (Projects)', { at: '.grade-note', msg: 'Not recorded: the ledger refused the grade', want: 'polite',
    act: () => say({ type: 'error', request_id: idOf('grade'), message: 'the ledger refused the grade' }) });
  // Typed grade text keeps its focus and selection through a work push that
  // redraws Projects, as the contract's fields do.
  const twoDelivered = [{ id: 'ses-1', description: 'Draft the report', result: 'Drafted.' }, { id: 'ses-2', description: 'Check the dates', result: 'Checked.' }];
  say({ type: 'work', work: { live: [], queued: 0, delivered: twoDelivered } });
  click('[data-grade-work="ses-2"][data-grade="partial"]');
  const typed = document.querySelector('.grade-input[data-grade-session="ses-2"]');
  if (typed) { typed.value = 'the dates were off'; typed.dispatchEvent(new Event('input')); }
  await focusProbe('a grade comment on Projects, while a work push redraws it', { focus: '.grade-input[data-grade-session="ses-2"]', select: [4, 9],
    act: () => say({ type: 'work', work: { live: [], queued: 0, delivered: twoDelivered.concat([{ id: 'ses-3', description: 'File the letter', result: 'Filed.' }]) } }),
    want: '.grade-input[data-grade-session="ses-2"]' });
  document.activeElement.blur();
  // The page's own grade, answered. The host answers a grade with the work
  // snapshot under the grade's request id (ws_work.go, wsGrade), and Projects
  // is repainted from it: the recorded grade replaces the chips (a review 2259
  // item 1; host 2300 item 1). Then a grade out when the connection is lost:
  // the page says it cannot know and keeps the chips shut, and on reconnect
  // it reads the outcome from the host's snapshot without sending it again.
  try {
    click('[data-grade-work="ses-3"][data-grade="served"]');
    const own = sent.filter(f => f.type === 'grade' && f.grade && f.grade.session === 'ses-3').at(-1);
    const ses3Graded = { id: 'ses-3', description: 'File the letter', result: 'Filed.', grade: { grade: 'served', comment: '' } };
    say({ type: 'work', request_id: own && own.request_id, work: { live: [], queued: 0, delivered: twoDelivered.concat([ses3Graded]) } });
    if (document.querySelector('[data-grade-work="ses-3"]') || !document.querySelector('.grade-said .grade-chip.is-served'))
      problems.push('the work frame that answers a grade from this page did not repaint Projects with the recorded grade');
    const fourDelivered = twoDelivered.concat([ses3Graded, { id: 'ses-4', description: 'Send the invoice', result: 'Sent.' }]);
    say({ type: 'work', work: { live: [], queued: 0, delivered: fourDelivered } });
    click('[data-grade-work="ses-4"][data-grade="served"]');
    const gradesSent = sent.filter(f => f.type === 'grade').length;
    const ses4 = () => document.querySelector('[data-grade-work="ses-4"][data-grade="served"]');
    const ses4Note = () => (ses4()?.closest('.grade-ask')?.querySelector('.grade-note')?.textContent || '');
    live().drop();
    if (!/closed before the host answered/.test(ses4Note()) || !ses4()?.disabled)
      problems.push('a grade out when the connection was lost did not say its outcome is unknown: ' + JSON.stringify(ses4Note()));
    const socketsBefore = sockets.length;
    wake();
    await until(() => sockets.length > socketsBefore, 4000);
    if (sockets.length === socketsBefore) problems.push('the page opened no socket to read a lost grade back');
    else {
      live().open();
      say({ type: 'work', work: { live: [], queued: 0, delivered: fourDelivered } });
      if (sent.filter(f => f.type === 'grade').length !== gradesSent) problems.push('reconnect sent a lost grade again');
      if (!ses4() || ses4().disabled || !/No grade is recorded/.test(ses4Note()))
        problems.push('the work frame after reconnect did not give back the chips of a grade lost with the connection: ' + JSON.stringify(ses4Note()));
    }
  } catch (e) { problems.push('the grade cases on Projects: the driver threw: ' + ((e && e.message) || e)); }
  goTo('chat');

  await announcerChecks();

  await probe('no identity yet', { at: '#p-state', msg: 'awaiting birth', want: 'polite', act: () => { status({ name: '(not born)' }); say({ type: 'providers', providers }); } });
  // First run is fetched by the status that finds no identity, so Begin
  // is pressed once its screen is drawn and wired.
  await until(() => document.getElementById('fb-birth')?.onclick, 6000);
  // The notes choice as saved: Begin asks for the birth at once (step 7).
  await probe('Birth begins', { at: '#fb-result', msg: 'Beginning…', want: 'polite', act: () => { S.config = Object.assign({}, S.config, { genesis: { agent_memory_off: false } }); click('#fb-birth'); } });
  await probe('Birth is refused', { at: '#fb-result', msg: 'the provider could not be reached: connection refused', want: 'polite',
    act: () => say({ type: 'error', message: 'the provider could not be reached: connection refused' }) });
  await probe('the provider says the key is wrong', { at: '#fb-hint', msg: 'The API key was rejected by the provider.', want: 'polite',
    act: () => say({ type: 'error', message: 'The API key was rejected by the provider.' }) });
  $('fb-provider').value = '0'; $('fb-provider').onchange();
  const signingIn = signin => [Object.assign({}, providers[0], { can_sign_in: true, signin })];
  await probe('a sign-in is under way (first boot)', { at: '#fb-signin .device-code', msg: 'Enter ABCD-1234 on the provider’s page.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'providers', providers: signingIn({ status: 'pending', url: 'https://login.example.test/x', device: { user_code: 'ABCD-1234', verification_uri: 'https://login.example.test/device' } }) }) });
  await probe('a sign-in is complete (first boot)', { at: '#fb-signin .store-hint', msg: 'Sign-in connected.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'providers', providers: signingIn({ status: 'connected' }) }) });
  click('#fb-restore-open');
  await probe('a restore is refused at first boot', { at: '[data-restore-said]', msg: 'That escrow file did not open: the passphrase is wrong.', want: 'polite', voice: 'announce',
    act: () => say({ type: 'restore_new', restore_new: { action: 'keys', error: 'That escrow file did not open: the passphrase is wrong.' } }) });
  await probe('the escrow keys are in place (first boot)', { at: '[data-keys-ok]', msg: 'keys are on this computer', want: 'polite', voice: 'announce',
    act: () => say({ type: 'restore_new', restore_new: { action: 'keys', keys: { identity: 'abcdef0123456789abcdef01', holds_snapshot_key: true } } }) });
  { const files = new DataTransfer(); files.items.add(new File(['x'], 'ledger-2026-09-29-seq7.tar.age')); $('fbr-snap').files = files.files; }
  await probe('the snapshot is on this machine (first boot)', { at: '[data-snapshot-ok]', msg: 'This backup is already here.', want: 'polite', voice: 'announce',
    act: async () => { click('#fbr-send'); await until(() => document.querySelector('[data-snapshot-ok]')); } });
  await probe('a restore is unavailable on this connection (first boot)', { at: '[data-nosecrets]', msg: 'To restore, open this page on the computer where AII OS runs', want: 'polite', voice: 'announce',
    act: () => say({ type: 'restore_new', restore_new: { action: 'hello', secrets_ok: false } }) });
}

const finish = () => {
  const text = table();
  report(problems.length ? 'FAIL: ' + problems.length + ' unmet:\n  - ' + problems.join('\n  - ') + '\n' + text : 'OK\n' + text);
};
run().then(finish, e => { problems.push('the run stopped: ' + ((e && e.message) || e) + '\n' + ((e && e.stack) || '')); finish(); });
`

func TestEveryStatusTheFrameGivesIsSaidByARegionThatWasThereBeforeTheChange(t *testing.T) {
	runStatusMeasurement(t, statusFakeSocketJS, statusMeasureJS)
}

func TestStatusMeasurementWaitsForForegroundStartup(t *testing.T) {
	socket := `window.__statusForeground = false;
Object.defineProperty(document, 'visibilityState', { configurable: true,
  get: () => window.__statusForeground ? 'visible' : 'hidden' });
` + statusFakeSocketJS
	measure := strings.Replace(statusMeasureJS, "async function run() {", `async function run() {
  queueMicrotask(() => {
    window.__statusForeground = true;
    document.dispatchEvent(new Event('visibilitychange'));
  });`, 1)
	runStatusMeasurement(t, socket, measure)
}

func runStatusMeasurement(t *testing.T, socketJS, measureJS string) {
	t.Helper()
	read := func(p string) []byte {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return b
	}
	frame := string(read("index.html"))
	const first = `<script src="./theme-boot.js"></script>`
	if !strings.Contains(frame, first) || !strings.Contains(frame, "</body>") {
		t.Fatal("index.html no longer has the shape this test loads it by")
	}
	page := strings.Replace(frame, first, `<script src="/__socket.js"></script>`+first, 1)
	page = strings.Replace(page, "</body>", `<script type="module" src="/__measure.js"></script></body>`, 1)
	modules := map[string][]byte{
		"/__socket.js":  []byte(socketJS),
		"/__measure.js": []byte(measureJS),
		"/theme.css":    read("theme.css"),
		"/layout.css":   read("layout.css"),
		"/custom.css":   read("custom.css"),
	}
	runPageInEnginesWithHeaders(t, page, modules, map[string]string{"Content-Security-Policy": uiCSP})
}
