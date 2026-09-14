
import { S } from './state.js';
import { $ } from './util.js';
import { send } from './ws.js';
import { go } from './app.js';
import { createFrameBridge, topicOf, WIRED_COMMANDS } from './bridge.js';
import { renderPanel } from './panel.js';

let sectionsList = [];
let safeReason = '';
let layout = null;
let lastPlanJSON = '';
const mounted = new Map();

const SLOTS = ['rail', 'main-tabs', 'panel', 'dock', 'overlay'];
const SLOT_EL = { rail: 'slot-rail', panel: 'slot-panel', dock: 'slot-dock', overlay: 'slot-overlay' };

const mobileMQ = window.matchMedia('(max-width: 767px)');
function profileName() { return mobileMQ.matches ? 'mobile' : 'desktop'; }

export function onSections(list, reason) {
  sectionsList = list || [];
  safeReason = reason || '';
  remount();
}
export function onLayout(raw) {
  layout = raw || null;
  remount();
}

export function publish(msg) {
  if (msg.type === 'continuity' && msg.continuity && msg.continuity.mode === 'safe') {
    if (!safeReason) { safeReason = msg.continuity.safe_reason || 'safe mode'; remount(); }
  }
  const t = topicOf(msg);
  if (!t) return;
  mounted.forEach(function (m) { m.bridge.publish(t.topic, t.data); });
}
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
  return out;
}

export function onTokensChanged() {
  const tokens = collectTokens();
  mounted.forEach(function (m) { if (m.bridge) m.bridge.pushTokens(tokens); });
}

function mountPlan() {
  if (safeReason) return [];
  if (!layout || !layout.profiles) return [];
  const prof = layout.profiles[profileName()];
  if (!prof) return [];
  const plan = [];
  SLOTS.forEach(function (slot) {
    (Array.isArray(prof[slot]) ? prof[slot] : []).forEach(function (id) {
      const sec = sectionsList.find(function (s) { return s.id === id; });
      if (!sec) return;
      if (sec.slot !== slot) {
        console.warn('sections: ' + id + ' declares slot "' + sec.slot + '" but the layout places it in "' + slot + '" — refusing the mismatch');
        return;
      }
      if (!plan.some(function (p) { return p.id === id; })) plan.push({ id: id, slot: slot, sec: sec });
    });
  });
  return plan;
}

function remount() {
  const plan = mountPlan();
  const planJSON = JSON.stringify(plan.map(function (p) { return { id: p.id, slot: p.slot, entry: p.sec.entry, dev: !!p.sec.dev }; })) + '|' + safeReason;
  if (planJSON === lastPlanJSON) return;
  lastPlanJSON = planJSON;

  mounted.forEach(function (m, id) { unmountOne(id, m); });
  mounted.clear();

  plan.forEach(function (p) { mountOne(p.sec, p.slot); });

  SLOTS.forEach(function (slot) {
    const el = SLOT_EL[slot] ? $(SLOT_EL[slot]) : null;
    if (!el) return;
    const note = el.querySelector('.sections-safe');
    if (note) note.remove();
    el.classList.toggle('occupied', plan.some(function (p) { return p.slot === slot; }));
    if (slot === 'rail' && safeReason && layout) {

      el.classList.add('occupied');
      const div = document.createElement('div');
      div.className = 'sections-safe';
      div.textContent = 'sections suspended — SAFE: ' + safeReason;
      el.appendChild(div);
    }
  });

  renderPanel();
}

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
  frame.src = '/sections/' + encodeURIComponent(sec.id) + '/' + sec.entry;

  const entry = { sec: sec, box: box, frame: frame, slot: slot, port: null, bridge: null, navItem: null, viewEl: null };

  if (slot === 'main-tabs') {

    const view = document.createElement('section');
    view.className = 'view';
    view.id = 'view-section:' + sec.id;
    box.appendChild(frame);
    view.appendChild(box);
    $('views').appendChild(view);
    const nav = document.createElement('div');
    nav.className = 'nav-item';
    nav.dataset.view = 'section:' + sec.id;
    nav.innerHTML = '<span class="ico">&#9724;</span>';
    nav.appendChild(document.createTextNode(sec.title));
    nav.onclick = function () { go('section:' + sec.id); };
    $('nav').appendChild(nav);
    entry.navItem = nav;
    entry.viewEl = view;
  } else {
    box.appendChild(frame);
    $(SLOT_EL[slot]).appendChild(box);
  }
  mounted.set(sec.id, entry);
}

function unmountOne(id, m) {
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
  mounted.forEach(function (entry) {
    if (!entry.frame.contentWindow || entry.frame.contentWindow !== e.source) return;
    if (entry.port) { try { entry.port.close(); } catch (err) {} }
    const ch = new MessageChannel();
    entry.port = ch.port1;
    entry.bridge = createFrameBridge({
      topics: entry.sec.topics, commands: entry.sec.commands, wired: WIRED_COMMANDS,
      post: function (m) { ch.port1.postMessage(m); },
      sendToServer: send,
      onResize: function (px) {
        if (entry.slot !== 'main-tabs') entry.frame.style.height = px + 'px';
      },
    });
    ch.port1.onmessage = function (ev) { entry.bridge.onFrame(ev.data); };
    e.source.postMessage({ type: 'aii-section-connect', v: 1, tokens: collectTokens() }, '*', [ch.port2]);
  });
}

export function initSections() {
  SLOTS.forEach(function (slot) {
    const el = SLOT_EL[slot] ? $(SLOT_EL[slot]) : null;
    if (el) el.innerHTML = '';
  });
  window.addEventListener('message', onHello);
  const onMQ = function () { lastPlanJSON = ''; remount(); };
  if (mobileMQ.addEventListener) mobileMQ.addEventListener('change', onMQ);
  else if (mobileMQ.addListener) mobileMQ.addListener(onMQ);
}
