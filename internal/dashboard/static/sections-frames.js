import { S } from './state.js';
import { $ } from './util.js';
import { send } from './ws.js';
import { go } from './app.js';

let lastPlanJSON = '';
const mounted = new Map();
let page = null;

const HELLO_GRACE_MS = 1000;
const SLOTS = ['rail', 'main-tabs', 'panel', 'dock', 'overlay'];
const SLOT_EL = { rail: 'slot-rail', panel: 'slot-panel', dock: 'slot-dock', overlay: 'slot-overlay' };

const mobileMQ = window.matchMedia('(max-width: 767px)');
function profileName() { return mobileMQ.matches ? 'mobile' : 'desktop'; }

export function frame(msg) {
  if (msg.type === 'config') { const ids = rowIds(msg.config); mounted.forEach(function (m) { placeNav(m, ids); }); }
  mounted.forEach(function (m) { if (m.bridge) m.bridge.frame?.(msg); });
}
export function lost() { mounted.forEach(function (m) { if (m.bridge) m.bridge.lost?.(); }); }

function rowIds(config) {
  const list = config && config.plugins && config.plugins.installed;
  return new Set((list || []).map(function (p) { return p.id; }));
}
function placeNav(m, ids) {
  if (!m.navItem) return;
  if (m.sec.plugin && ids.has(m.sec.plugin)) { m.navItem.remove(); return; }
  if (!m.navItem.isConnected) (document.querySelector('.c-rail__pages') || $('nav')).appendChild(m.navItem);
}
S.framePlace = function (view) {
  const m = view.indexOf('section:') === 0 && mounted.get(view.slice(8));
  return m && m.navItem && !m.navItem.isConnected ? 'settings' : '';
};
S.framePagesOf = function (pkg) {
  const pages = [];
  mounted.forEach(function (m, id) { if (m.slot === 'main-tabs' && pkg && m.sec.plugin === pkg) pages.push({ id: id, title: m.sec.title }); });
  return pages;
};

export function sectionTitle(view) {
  if (view.indexOf('section:') !== 0) return '';
  const m = mounted.get(view.slice(8));
  return m ? m.sec.title : '';
}

export function collectTokens() {
  const out = {};
  for (let i = 0; i < document.styleSheets.length; i++) {
    let rules;
    try { rules = document.styleSheets[i].cssRules; } catch (err) { continue; }
    if (!rules) continue;
    for (let j = 0; j < rules.length; j++) {
      const r = rules[j];
      if (!r.selectorText || r.selectorText !== ':root' || !r.style) continue;
      for (let k = 0; k < r.style.length; k++) {
        const name = r.style[k];
        if (name && name.indexOf('--') === 0) out[name] = r.style.getPropertyValue(name).trim();
      }
    }
  }
  const inline = document.documentElement.style;
  for (let i = 0; i < inline.length; i++) {
    const name = inline[i];
    if (name && name.indexOf('--') === 0) out[name] = inline.getPropertyValue(name).trim();
  }
  const live = getComputedStyle(document.documentElement);
  Object.keys(out).forEach(function (name) { const now = live.getPropertyValue(name).trim(); if (now) out[name] = now; });
  const scheme = live.getPropertyValue('color-scheme').trim();
  if (scheme) out['color-scheme'] = scheme;
  return out;
}

function tokensFor(m) {
  const tokens = collectTokens();
  const column = getComputedStyle(m.box).getPropertyValue('--column-wide').trim();
  if (column) tokens['--column-wide'] = column;
  return tokens;
}

export function onTokensChanged() {
  mounted.forEach(function (m) { if (m.bridge) m.bridge.pushTokens(tokensFor(m)); });
}

function placedIds() {
  const prof = page.layout && page.layout.profiles && page.layout.profiles[profileName()];
  return prof ? SLOTS.flatMap(function (slot) { return Array.isArray(prof[slot]) ? prof[slot] : []; }) : [];
}

function mountPlan() {
  const { sectionsList, safeReason, layout } = page;
  const plan = sectionsList.filter(function (s) { return s.built_in && s.views; }).map(function (s) { return { id: s.id, slot: 'view', sec: s }; });
  if (!layout || !layout.profiles) return plan;
  const prof = layout.profiles[profileName()];
  if (!prof) return plan;
  SLOTS.forEach(function (slot) {
    (Array.isArray(prof[slot]) ? prof[slot] : []).forEach(function (id) {
      const sec = sectionsList.find(function (s) { return s.id === id; });
      if (!sec) return;
      if (safeReason && !sec.built_in) return;
      if (sec.slot !== slot) {
        console.warn('sections: ' + id + ' declares slot "' + sec.slot + '" but the layout places it in "' + slot + '" — refusing the mismatch');
        return;
      }
      if (!plan.some(function (p) { return p.id === id; })) plan.push({ id: id, slot: slot, sec: sec });
    });
  });
  return plan;
}

export function remount(from) {
  if (from) begin(from);
  const { safeReason, heldName } = page;
  const plan = mountPlan();
  const planJSON = JSON.stringify(plan.map(function (p) { return { id: p.id, slot: p.slot, entry: p.sec.entry, dev: !!p.sec.dev, address: p.sec.address }; })) + '|' + safeReason;
  if (planJSON === lastPlanJSON) return;
  lastPlanJSON = planJSON;

  mounted.forEach(function (m, id) {
    const p = plan.find(function (x) { return x.id === id; });
    if (p && p.slot === 'view' && m.slot === 'view' && m.src === srcOf(p.sec)) { m.sec = p.sec; return; }
    unmountOne(id, m);
    mounted.delete(id);
  });

  plan.forEach(function (p) { if (!mounted.has(p.id)) mountOne(p.sec, p.slot); });
  S.framePagesChanged?.();

  SLOTS.forEach(function (slot) {
    const el = SLOT_EL[slot] ? $(SLOT_EL[slot]) : null;
    if (!el) return;
    const note = el.querySelector('.sections-safe');
    if (note) note.remove();
    el.classList.toggle('occupied', plan.some(function (p) { return p.slot === slot; }));
    if (slot === 'rail' && safeReason && placedIds().some(function (id) { return !plan.some(function (p) { return p.id === id; }); })) {

      el.classList.add('occupied');
      const div = document.createElement('div');
      div.className = 'sections-safe';
      div.textContent = 'sections suspended — ' + heldName + ': ' + safeReason;
      el.appendChild(div);
    }
  });
}

function srcOf(sec) { return (sec.address || '/sections/' + encodeURIComponent(sec.id) + '/') + sec.entry; }

function mountOne(sec, slot) {
  const box = document.createElement('div');
  box.className = 'section-box';
  if (sec.dev) {

    const banner = document.createElement('div');
    banner.className = 'dev-banner';
    banner.textContent = 'UNVERIFIED — dev section served from disk';
    box.appendChild(banner);
  }
  const frame = document.createElement('iframe');
  frame.className = 'section-frame';

  frame.setAttribute('sandbox', 'allow-scripts');
  frame.setAttribute('title', sec.title);
  const src = srcOf(sec);
  if (slot !== 'view') frame.src = src;

  const entry = { src: src, sec: sec, box: box, frame: frame, slot: slot, port: null, bridge: null, navItem: null, viewEl: null };

  if (slot === 'view' || slot === 'main-tabs') {

    const view = document.createElement('section');
    view.className = 'view';
    view.id = 'view-section:' + sec.id;
    box.appendChild(frame);
    view.appendChild(box);
    $('views').appendChild(view);
    entry.viewEl = view;
  }
  if (slot === 'view') frame.style.visibility = 'hidden';
  if (slot === 'main-tabs') {
    const nav = document.createElement('button');
    nav.type = 'button';
    nav.className = 'nav-item';
    nav.dataset.view = 'section:' + sec.id;
    nav.innerHTML = '<span class="ico">&#9724;</span>';
    nav.appendChild(document.createTextNode(sec.title));
    nav.onclick = function () { go('section:' + sec.id); };
    entry.navItem = nav;
    placeNav(entry, rowIds(S.config));
  } else if (slot !== 'view') {
    box.appendChild(frame);
    $(SLOT_EL[slot]).appendChild(box);
  }
  mounted.set(sec.id, entry);
}

let turnsEnded = 0, turnEnds = null;
const turnNow = function () { return { type: 'turn', thinking: !!S.thinking, ended: turnsEnded }; };
function tellTurn(ended) {
  if (ended) turnsEnded++;
  frame(turnNow());
}
function hearTurnEnds() {
  if (S.turnEnded === turnEnds) return;
  const own = S.turnEnded;
  turnEnds = function () { own?.(); tellTurn(true); };
  S.turnEnded = turnEnds;
}

function showSection(m, v, on) {
  [[m.viewEl, on], [$('view-' + v), !on]].forEach(function ([el, shown]) {
    if (!el) return;
    el.classList.toggle('on', shown);
    el.inert = !shown;
    el.setAttribute('aria-hidden', String(!shown));
  });
}

function tellRoute(v) {
  const msg = { type: 'route', view: v, hash: location.hash, base: location.origin + location.pathname };
  page.latest.set('route', msg);
  frame(msg);
}

function leaveToPage(m) {
  page.failed.add(m.sec.id);
  if (m.sec.views.includes(S.view)) go(S.view);
}

export function open(v, read) {
  mounted.forEach(function (m) {
    if (m.slot !== 'view' || !m.sec.views.includes(v) || page.failed.has(m.sec.id)) return;
    showSection(m, v, true);
    if (read) {
      tellRoute(v);
      if (m.bridge) { m.bridge.pushTokens(tokensFor(m)); m.bridge.refresh?.(); }
    }
    if (m.frame.getAttribute('src')) return;
    m.frame.onload = function () {
      setTimeout(function () { if (!m.live) leaveToPage(m); }, HELLO_GRACE_MS);
    };
    m.frame.src = m.src;
  });
}

function cameAlive(m) {
  if (m.live) return;
  m.live = true;
  m.frame.style.visibility = '';
  hearTurnEnds();
  if (page.failed.delete(m.sec.id) && m.sec.views.includes(S.view)) open(S.view, true);
}

function unmountOne(id, m) {
  if (m.slot === 'view' && m.viewEl.classList.contains('on')) showSection(m, S.view, false);
  if (m.port) { try { m.port.close(); } catch (err) {} }
  if (m.navItem) m.navItem.remove();
  if (m.viewEl) m.viewEl.remove();
  m.box.remove();
  if (S.view === 'section:' + id) go('chat');
}

function onHello(e) {
  const d = e.data;
  if (!d || d.type !== 'aii-section-hello') return;
  if (d.v !== 1) {

    console.error('[sections] hello v' + d.v + ' refused (frame speaks v1)');
    return;
  }
  import('./bridge.js').then(function (b) { mounted.forEach(function (entry) {
    if (!entry.frame.contentWindow || entry.frame.contentWindow !== e.source) return;
    if (entry.port) { try { entry.port.close(); } catch (err) {} }
    const ch = new MessageChannel();
    entry.port = ch.port1;
    entry.bridge = b.createFrameBridge({
      topics: entry.sec.topics, commands: entry.sec.commands, wired: b.WIRED_COMMANDS, builtIn: !!entry.sec.built_in,
      latest: function (topic) { return topic === 'turn' ? turnNow() : page.latest.get(topic); },
      post: function (m) { ch.port1.postMessage(m); },
      sendToServer: send,
      onResize: function (px) {
        if (entry.slot !== 'main-tabs' && entry.slot !== 'view') entry.frame.style.height = px + 'px';
      },
    });
    ch.port1.onmessage = function (ev) { cameAlive(entry); entry.bridge.onFrame(ev.data); };
    e.source.postMessage({ type: 'aii-section-connect', v: 1, tokens: tokensFor(entry) }, '*', [ch.port2]);
  }); }, function (err) { console.error('[sections] the section bridge did not load', err); });
}

let begun = false;
function begin(from) {
  page = from;
  if (begun) return;
  begun = true;
  S.sectionsTurn = tellTurn;
  SLOTS.forEach(function (slot) {
    const el = SLOT_EL[slot] ? $(SLOT_EL[slot]) : null;
    if (el) el.innerHTML = '';
  });
  window.addEventListener('message', onHello);
  const onMQ = function () { lastPlanJSON = ''; remount(); onTokensChanged(); };
  window.addEventListener('hashchange', function () { if (page.sectionsList.some(function (s) { return s.built_in && s.views && s.views.includes(S.view); })) tellRoute(S.view); });
  if (mobileMQ.addEventListener) mobileMQ.addEventListener('change', onMQ);
  else if (mobileMQ.addListener) mobileMQ.addListener(onMQ);
}
