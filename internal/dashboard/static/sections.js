/* sections.js — the slot/layout renderer and section mounter (R66 UP2,
   UI_FRAME.md §§3-5): mounts each registered+laid-out section as a
   sandboxed iframe (allow-scripts, NEVER allow-same-origin — the
   opaque origin is the wall), runs the per-section MessageChannel
   handshake, and relays exactly the bridge's four primitives. The
   frame never trusts a section: allowlists live in bridge.js, files
   come only from /sections/<id>/ behind the server's CSP, and SAFE
   unmounts everything (bare frame, §3). */
import { S } from './state.js';
import { $ } from './util.js';
import { send } from './ws.js';
import { go } from './app.js';
import { createFrameBridge, topicOf, WIRED_COMMANDS } from './bridge.js';
import { renderPanel } from './panel.js';

/* --- frame state -------------------------------------------- */
let sectionsList = [];   // SectionState[] from the server ("sections")
let safeReason = '';     // non-empty = server suppressed the list (SAFE)
let layout = null;       // parsed ui-layout.json ("layout"), null = none
let lastPlanJSON = '';   // remount only when the mount plan changes
const mounted = new Map(); // id -> {sec, box, frame, port, bridge, navItem, viewEl}

const SLOTS = ['rail', 'main-tabs', 'panel', 'dock', 'overlay'];
const SLOT_EL = { rail: 'slot-rail', panel: 'slot-panel', dock: 'slot-dock', overlay: 'slot-overlay' };

/* Form-factor profile (§5): ONE frame everywhere; profiles differ, the
   codebase does not. The viewport class picks the profile — the same
   767px line the UP1 mobile restructure uses. */
const mobileMQ = window.matchMedia('(max-width: 767px)');
function profileName() { return mobileMQ.matches ? 'mobile' : 'desktop'; }

/* --- inbound from ws.js ------------------------------------- */
export function onSections(list, reason) {
  sectionsList = list || [];
  safeReason = reason || '';
  remount();
}
export function onLayout(raw) {
  layout = raw || null;
  remount();
}
/* publish taps EVERY server message (called from ws.js dispatch):
   topic projections flow to subscribed sections; the continuity
   projection doubles as the frame's own SAFE watch, so sections
   leave the screen on the same signal the mode pill uses. */
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

/* --- design tokens ------------------------------------------ */
/* The token surface (§4): the effective :root custom properties,
   pushed over the bridge at connect and again whenever the operator's
   theme changes (onTokensChanged, called by ws.js after theme.js has
   applied). That runtime switch is no longer hypothetical, so this
   reads BOTH places a token can live:

     - stylesheet rules whose selector is :root — the compiled
       theme.css defaults;
     - the INLINE custom properties on documentElement — how theme.js
       applies theme.json, because setProperty is the mechanism with
       no parse step.

   An inline style is not a stylesheet rule, so the rule walk alone
   cannot see a themed token: the frame would recolour and every
   section would keep the compiled value, with nothing logged either
   side. Overlay order matters — inline LAST, because that is the
   cascade the frame itself resolves. See
   TestThemeTokensAreInvisibleToRuleWalk, which establishes this
   divergence empirically in both engines. */
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

/* onTokensChanged re-pushes the effective tokens to every mounted
   section. Without it a section mounted BEFORE a theme edit keeps the
   old palette for its whole life, since collectTokens ran once at its
   handshake. bridge.pushTokens has existed as a seam since UP2 with
   no caller; this is the caller. Sections mounted AFTER the edit are
   already correct via the handshake path above. */
export function onTokensChanged() {
  const tokens = collectTokens();
  mounted.forEach(function (m) { if (m.bridge) m.bridge.pushTokens(tokens); });
}

/* --- mount plan --------------------------------------------- */
/* The layout binds slot → section ids per profile; a section mounts
   only into the slot it DECLARED (section.json is its design intent —
   a rail-shaped surface pointed at overlay renders broken, so the
   mismatch is refused loudly, never guessed around). Absent layout or
   profile = no sections: frame-only, the safe default. */
function mountPlan() {
  if (safeReason) return [];
  if (!layout || !layout.profiles) return [];
  const prof = layout.profiles[profileName()];
  if (!prof) return [];
  const plan = [];
  SLOTS.forEach(function (slot) {
    (Array.isArray(prof[slot]) ? prof[slot] : []).forEach(function (id) {
      const sec = sectionsList.find(function (s) { return s.id === id; });
      if (!sec) return; // laid out but not registered — nothing to mount
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
  if (planJSON === lastPlanJSON) return; // no flicker: identical plan = no rebuild
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
      /* Bare frame + the reason (§3): sections were configured, SAFE
         took them down, and the operator sees why where they lived. */
      el.classList.add('occupied');
      const div = document.createElement('div');
      div.className = 'sections-safe';
      div.textContent = 'sections suspended — SAFE: ' + safeReason;
      el.appendChild(div);
    }
  });

  /* After slots settle, re-render the default panel: if no section
     claimed the panel slot, panel.js fills it with system status;
     if a section is there, panel.js defers (checks .section-box). */
  renderPanel();
}

/* --- mount / unmount ---------------------------------------- */
function mountOne(sec, slot) {
  const box = document.createElement('div');
  box.className = 'section-box';
  if (sec.dev) {
    /* The dev-serve posture is LOUD (§3): unverified bytes never look
       like verified ones. The banner is frame DOM — outside the
       iframe, unreachable by the section it warns about. */
    const banner = document.createElement('div');
    banner.className = 'dev-banner';
    banner.textContent = 'UNVERIFIED — dev section served from disk';
    box.appendChild(banner);
  }
  const frame = document.createElement('iframe');
  frame.className = 'section-frame';
  /* allow-scripts ONLY: no same-origin (opaque origin — no parent DOM,
     no storage, no credentialed fetch), no forms, no popups, no
     top-navigation. The sandbox attribute is set BEFORE src. */
  frame.setAttribute('sandbox', 'allow-scripts');
  frame.setAttribute('title', sec.title);
  frame.src = '/sections/' + encodeURIComponent(sec.id) + '/' + sec.entry;

  const entry = { sec: sec, box: box, frame: frame, slot: slot, port: null, bridge: null, navItem: null, viewEl: null };

  if (slot === 'main-tabs') {
    /* A main-tabs section becomes a first-class tab: a frame-owned
       nav item plus a full view container. The nav item is frame DOM;
       the section only ever paints inside its iframe. */
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
  if (S.view === 'section:' + id) go('chat'); // the tab under the operator vanished — land somewhere real
}

/* --- the handshake ------------------------------------------
   SECTION-INITIATED: section-api.js announces 'aii-section-hello'
   from inside the frame; the frame answers the announcing WINDOW
   with a fresh MessageChannel — port1 stays here, port2 transfers
   with the connect message. Announce-then-answer instead of
   frame-on-iframe-load because a section module awaiting ready() at
   top level delays the frame's load event (a load-driven connect
   would deadlock), and a reloading frame re-announces for free. The
   sender is identified by WINDOW IDENTITY (e.source ===
   frame.contentWindow) — the one thing a sibling section cannot
   forge. targetOrigin is '*' of necessity: an opaque origin is
   unaddressable by name, and the payload carries only design tokens. */
function onHello(e) {
  const d = e.data;
  if (!d || d.type !== 'aii-section-hello') return;
  if (d.v !== 1) {
    /* Versioned handshake: a section speaking another major never
       connects — refusal by silence plus a loud log beats a
       half-working bridge. */
    console.error('[sections] hello v' + d.v + ' refused (frame speaks v1)');
    return;
  }
  mounted.forEach(function (entry) {
    if (!entry.frame.contentWindow || entry.frame.contentWindow !== e.source) return;
    if (entry.port) { try { entry.port.close(); } catch (err) {} } // a reloaded frame gets a fresh channel
    const ch = new MessageChannel();
    entry.port = ch.port1;
    entry.bridge = createFrameBridge({
      topics: entry.sec.topics, commands: entry.sec.commands, wired: WIRED_COMMANDS,
      post: function (m) { ch.port1.postMessage(m); },
      sendToServer: send,
      onResize: function (px) {
        if (entry.slot !== 'main-tabs') entry.frame.style.height = px + 'px'; // main-tabs frames fill the stage
      },
    });
    ch.port1.onmessage = function (ev) { entry.bridge.onFrame(ev.data); };
    e.source.postMessage({ type: 'aii-section-connect', v: 1, tokens: collectTokens() }, '*', [ch.port2]);
  });
}

/* --- boot --------------------------------------------------- */
export function initSections() {
  SLOTS.forEach(function (slot) {
    const el = SLOT_EL[slot] ? $(SLOT_EL[slot]) : null;
    if (el) el.innerHTML = '';
  });
  window.addEventListener('message', onHello);
  const onMQ = function () { lastPlanJSON = ''; remount(); };
  if (mobileMQ.addEventListener) mobileMQ.addEventListener('change', onMQ);
  else if (mobileMQ.addListener) mobileMQ.addListener(onMQ); // older WebView shells
}
