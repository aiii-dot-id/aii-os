

let resolveReady;
const readyP = new Promise(function (r) { resolveReady = r; });

export function ready() { return readyP; }

export function _accept(connect, port) {

  if (!connect || connect.v !== 1) {
    throw new Error('section-api: frame bridge v' + (connect && connect.v) + ' is not v1');
  }
  let tokens = (connect && connect.tokens) || {};
  const subs = new Map();
  const pending = new Map();
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

      subscribe: function (topic, cb) {
        if (cb) {
          const l = subs.get(topic) || [];
          l.push(cb);
          subs.set(topic, l);
        }
        return request({ kind: 'subscribe', topic: topic });
      },
    },

    act: function (command, args) {
      return request({ kind: 'act', command: command, args: args || {} });
    },

    tokens: function (cb) {
      if (cb) { tokenCbs.push(cb); cb(tokens); }
      return tokens;
    },

    resize: function (px) { port.postMessage({ kind: 'resize', height: px }); },
  };
  resolveReady(api);
  return api;
}

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
