/* section-api.js — v0 (R66 UP2). FRAME-OWNED, served at
   /section-api.js: the client a section imports to reach the bridge.
   The whole ABI is four primitives, kept small forever (UI_FRAME.md
   §4): data.subscribe(topic, cb) · act(command, args) · tokens(cb) ·
   resize(px). A section runs in a sandboxed iframe with an opaque
   origin — no parent DOM, no storage, no sockets; this port is its
   ONLY channel, and the frame polices both directions.

   Author usage (module.js):
     import { ready } from '/section-api.js';
     const api = await ready();
     await api.data.subscribe('status', s => render(s));
     api.tokens(t => restyle(t));
     await api.act('project.select', { id: someId });
     api.resize(document.body.scrollHeight);
*/

let resolveReady;
const readyP = new Promise(function (r) { resolveReady = r; });

/* ready resolves with the api once the frame has connected the
   MessageChannel (the mount handshake). */
export function ready() { return readyP; }

/* _accept builds the api around a connected port. Exported for the
   node bridge suite (which owns both ends of a real MessageChannel);
   in the browser the window listener below is the only caller. */
export function _accept(connect, port) {
  /* The bridge ABI is versioned from day one — cheap now, impossible
     to retrofit once third-party sections exist. Major mismatch is a
     hard refusal, never a shrug. */
  if (!connect || connect.v !== 1) {
    throw new Error('section-api: frame bridge v' + (connect && connect.v) + ' is not v1');
  }
  let tokens = (connect && connect.tokens) || {};
  const subs = new Map();    // topic -> [cb]
  const pending = new Map(); // request id -> {resolve, reject}
  const tokenCbs = [];
  let nextID = 1;

  port.onmessage = function (e) {
    const m = e.data || {};
    if (m.kind === 'topic') {
      (subs.get(m.topic) || []).forEach(function (cb) { cb(m.data); });
    } else if (m.kind === 'reply') {
      const p = pending.get(m.id);
      if (p) {
        pending.delete(m.id);
        if (m.ok) p.resolve(m.data !== undefined ? m.data : true);
        else p.reject(new Error(m.error || 'refused'));
      }
    } else if (m.kind === 'tokens') {
      tokens = m.tokens || {};
      tokenCbs.forEach(function (cb) { cb(tokens); });
    }
  };

  function request(frame) {
    return new Promise(function (resolve, reject) {
      frame.id = nextID++;
      pending.set(frame.id, { resolve: resolve, reject: reject });
      port.postMessage(frame);
    });
  }

  const api = {
    version: 0,
    data: {
      /* subscribe asks the frame to relay one declared topic; the
         returned promise REJECTS (naming the topic) when the section
         never declared it — cb then never fires. */
      subscribe: function (topic, cb) {
        if (cb) {
          const l = subs.get(topic) || [];
          l.push(cb);
          subs.set(topic, l);
        }
        return request({ kind: 'subscribe', topic: topic });
      },
    },
    /* act forwards one declared+wired command; rejects naming the
       command otherwise. Resolution means "passed both allowlists and
       was forwarded" — outcomes arrive as topic data (the WS protocol
       is broadcast-shaped). */
    act: function (command, args) {
      return request({ kind: 'act', command: command, args: args || {} });
    },
    /* tokens registers for design-token pushes and returns the current
       set; cb fires immediately with what the connect carried, then on
       every change. */
    tokens: function (cb) {
      if (cb) { tokenCbs.push(cb); cb(tokens); }
      return tokens;
    },
    /* resize reports content height; the frame sets the iframe box. */
    resize: function (px) { port.postMessage({ kind: 'resize', height: px }); },
  };
  resolveReady(api);
  return api;
}

/* Browser lane. The handshake is SECTION-INITIATED: this module
   announces itself to the parent and the frame answers with the
   port. Announce-then-answer (not frame-on-iframe-load) because a
   section module awaiting ready() at top level DELAYS the iframe's
   load event until ready resolves — a frame that waited for load to
   send the connect would deadlock; and an announce survives a frame
   reload for free (the fresh document announces again).

   Accept exactly ONE connect, and ONLY from the parent. The source
   check is LOAD-BEARING, not politeness: sibling sections can reach
   this window as parent.frames[i] and postMessage into it —
   cross-origin frame traversal is legal — so anything not from the
   frame window itself is ignored. First connect wins; later ones are
   dropped (a rebinding attempt does not re-home the port). */
if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  let accepted = false;
  window.addEventListener('message', function (e) {
    if (accepted) return;
    if (e.source !== window.parent) return;
    const d = e.data;
    if (!d || d.type !== 'aii-section-connect' || !e.ports || !e.ports[0]) return;
    accepted = true;
    _accept(d, e.ports[0]);
  });
  if (window.parent && window.parent !== window) {
    window.parent.postMessage({ type: 'aii-section-hello', v: 1 }, '*');
  }
}
