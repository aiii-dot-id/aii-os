

export function topicOf(msg) {
  if (!msg) return null;
  switch (msg.type) {
    case 'connection': return { topic: 'connection', data: msg.on };
    case 'route': return { topic: 'route', data: { view: msg.view, hash: msg.hash, base: msg.base } };
    case 'turn': return { topic: 'turn', data: { thinking: msg.thinking, ended: msg.ended } };
    case 'status': return { topic: 'status', data: msg.stats || null };
    case 'projects': return { topic: 'projects', data: msg.projects || [] };
    case 'work': return { topic: 'work', data: msg.work || null };
    case 'identity': return { topic: 'identity', data: msg.identity || null };
    case 'continuity': return { topic: 'continuity', data: msg.continuity || null };
    case 'asks': return { topic: 'asks', data: msg.asks || [] };
    case 'tools': return { topic: 'tools', data: msg.tools || [] };
    case 'overlays': return { topic: 'overlays', data: msg.overlays || [] };
    case 'providers': return { topic: 'providers', data: (msg.providers || []).map(p => ({ name: p.name, context_length: p.context_length })) };
    case 'backups': {
      const v = msg.backups && msg.backups.view;
      if (!v) return null;
      const newest = (v.snapshots || [])[0];
      return { topic: 'backups', data: {
        enabled: v.enabled, escrow_covers: v.escrow_covers, escrow_checked_at: v.escrow_checked_at,
        snapshots: newest ? [{ at: newest.at, record: newest.record, encrypted: newest.encrypted }] : [],
        last_pass: v.last_pass ? { outcome: v.last_pass.outcome } : null,
      } };
    }
    case 'config': {
      const c = msg.config || {}, l = c.llm || {};
      return { topic: 'config', data: {
        llm: { model: l.model, provider: l.provider, resolved_model: l.resolved_model, resolved_provider: l.resolved_provider },
        cognition: { pulse_seconds: c.cognition && c.cognition.pulse_seconds },
        plugins: { installed: (c.plugins && c.plugins.installed) || [] },
      } };
    }
  }
  return null;
}

export const WIRED_COMMANDS = {

  'project.select': {
    validate: function (args) {
      return (args && typeof args.id === 'string' && args.id) ? '' : 'project.select requires args.id (a project id string)';
    },
    frame: function (args) {
      return { type: 'project', project: { action: 'select', id: args.id } };
    },
  },

  'pass.now': { builtInOnly: true, says: true, frame: function () { return { type: 'pass_now' }; } },
  'spend.read': {
    builtInOnly: true,
    validate: function (args) { return Number.isInteger(args.days) && args.days > 0 && args.days <= 366 ? '' : 'spend.read takes args.days, a whole number of days'; },
    frame: function (args) { return { type: 'query', query: 'spend', days: args.days }; },
  },

  'recall.search': {
    builtInOnly: true,
    validate: function (args) { return (typeof args.q === 'string' && args.q.trim()) ? '' : 'recall.search takes args.q, the words to recall'; },
    frame: function (args) { return { type: 'query', query: 'recall', q: args.q }; },
    answered: function (msg) { return { query: msg.query, message: msg.message }; },
  },
  'belief.trail': {
    builtInOnly: true, says: true,
    validate: function (args) { return (typeof args.id === 'string' && args.id) ? '' : 'belief.trail takes args.id, a belief'; },
    frame: function (args) { return { type: 'query', query: 'belief_trail', belief_id: args.id }; },
  },
  'meaning.source': {
    builtInOnly: true,
    validate: function (args, opts) { return offeredSources(opts).includes(args.choice) ? '' : 'meaning.source takes a choice the host offers'; },
    frame: function (args) { return { type: 'config_set', config: { 'memory.meaning_source': args.choice } }; },
  },

  'record.payload': {
    builtInOnly: true,
    validate: function (args) { return [args.seq, args.offset, args.length].every(function (n) { return /^[0-9]+$/.test(String(n)); }) ? '' : 'record.payload takes args.seq, args.offset and args.length, whole numbers'; },
    read: function (args, reads) { return reads.payload({ seq: String(args.seq), offset: String(args.offset), length: String(args.length) }); },
  },
  'page.go': {
    builtInOnly: true,
    validate: function (args) { return (typeof args.to === 'string' && args.to.indexOf('#/') === 0) ? '' : 'page.go takes args.to, an address of the page'; },
    read: function (args, reads) { return reads.go(args.to, !!args.replace); },
  },
  'clipboard.write': {
    builtInOnly: true,
    validate: function (args) { return typeof args.text === 'string' ? '' : 'clipboard.write takes args.text'; },
    read: function (args, reads) { return reads.copy(args.text); },
  },
  'visit': { builtInOnly: true, read: function (args, reads) { return reads.visit(); } },
  'place.keep': {
    builtInOnly: true,
    validate: function (args) { return typeof args.id === 'string' && args.kept && typeof args.kept === 'object' ? '' : 'place.keep takes args.id and args.kept'; },
    read: function (args, reads) { return reads.keepPlace(args.id, args.kept); },
  },
  'place.kept': {
    builtInOnly: true,
    validate: function (args) { return typeof args.id === 'string' ? '' : 'place.kept takes args.id'; },
    read: function (args, reads) { return reads.keptPlace(args.id); },
  },
  'reply.quote': {
    builtInOnly: true,
    validate: function (args) { return (typeof args.text === 'string' && args.text) ? '' : 'reply.quote takes args.text'; },
    read: function (args, reads) { return reads.quote(args.text); },
  },

  'page.key': {
    builtInOnly: true,
    validate: function (args) { return pageKey(args) ? '' : 'page.key takes a key the page hears'; },
    read: function (args, reads) { return reads.key(args); },
  },

  'record.read': {
    validate: function (args) {
      if (args.type !== undefined && !/^[a-z_.]+$/.test(String(args.type))) return 'record.read takes args.type, a kind of entry, or nothing';
      return (args.before === undefined || /^[1-9][0-9]*$/.test(String(args.before))) ? '' : 'record.read takes args.before, an entry number, or nothing';
    },
    read: function (args, reads) {
      const params = {};
      if (args.before !== undefined) params.before = String(args.before);
      if (args.type !== undefined) params.type = String(args.type);
      return reads.record(params);
    },
  },
};

const BUILT_IN_ONLY = new Set(['tools', 'overlays', 'connection', 'providers', 'config', 'route', 'turn', 'backups']);
const ASKED = { status: 'status', continuity: 'continuity', config: 'config', providers: 'providers', tools: 'tools', work: 'work', asks: 'asks', overlays: 'ui.overlay', projects: 'projects', identity: 'identity' };

const PAGE_KEYS = new Set(['?', 'g', 'c', 'p', 'a', 's']);
const pageKey = function (a) {
  const chord = !!a.ctrl || !!a.meta;
  return a.key === 'k' ? chord : (PAGE_KEYS.has(a.key) && !chord);
};

function offeredSources(opts) {
  const kept = opts.latest && opts.latest('identity');
  const m = kept && kept.identity && kept.identity.meaning;
  if (!m) return [];
  return ['none', 'provider'].concat((m.plugins || []).map(function (id) { return 'plugin:' + id; }), m.choice ? [m.choice] : []);
}

const FRAME_DOES = {
  record: function (params) { return import('./views/identity/record-read.js').then(function (m) { return m.readRecord(params); }); },
  payload: function (params) {
    return import('./pagekey.js').then(function (k) { return k.signedFetch('/record/payload?' + new URLSearchParams(params), { cache: 'no-store', credentials: 'same-origin' }); }).then(async function (res) {
      if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
      return res.arrayBuffer();
    });
  },
  go: function (to, replace) {
    return Promise.all([import('./shell/router.js'), import('./registry.js')]).then(function ([r, reg]) {
      let known = r.route(to);
      if (known && known.redirect) known = r.route(known.redirect);
      if (!known || !reg.VIEWS[known.view]) throw new Error('page.go takes an address of the page');
      if (replace) history.replaceState(null, '', to); else location.hash = to;
      return true;
    });
  },
  copy: function (text) {
    if (navigator.userActivation && !navigator.userActivation.isActive) return Promise.reject(new Error('clipboard.write needs the reader\'s click'));
    return import('./util.js').then(function (m) { return m.copyText(text); }).then(function (ok) {
      if (!ok) throw new Error('the browser did not copy it');
      return true;
    });
  },
  keepPlace: function (id, kept) { return import('./shell/reload-keep.js').then(function (m) { m.keepOfSection(id, kept); return true; }); },
  keptPlace: function (id) { return import('./shell/reload-keep.js').then(function (m) { return m.keptOfSection(id); }); },
  visit: function () {
    return Promise.all([import('./views/home/note.js'), import('./views/identity/record-read.js')]).then(function ([n, r]) {
      return r.firstEntryTime().catch(function () {}).then(function () { return n.lastVisit(); });
    });
  },
  quote: function (text) { return import('./views/identity/page.js').then(function (m) { m.putInReply(text); return true; }); },
  key: function (a) { document.dispatchEvent(new KeyboardEvent('keydown', { key: a.key, ctrlKey: !!a.ctrl, metaKey: !!a.meta, bubbles: true, cancelable: true })); return true; },
};

export function createFrameBridge(opts) {
  const declTopics = new Set(opts.topics || []);
  const declCommands = new Set(opts.commands || []);
  const wired = opts.wired || {};
  const subscribed = new Set();
  const reads = opts.reads || FRAME_DOES;
  const asked = new Map();

  function reply(id, ok, extra) {
    const m = Object.assign({ kind: 'reply', id: id, ok: ok }, extra || {});
    opts.post(m);
  }

  function onFrame(m) {
    if (!m || typeof m !== 'object') return;
    if (m.kind === 'subscribe') {
      const topic = String(m.topic || '');
      if (BUILT_IN_ONLY.has(topic) && !opts.builtIn) {
        reply(m.id, false, { error: 'topic not given to this section: ' + topic });
        return;
      }
      if (!declTopics.has(topic)) {

        reply(m.id, false, { error: 'topic not declared by this section: ' + topic });
        return;
      }
      subscribed.add(topic);
      const kept = opts.latest ? opts.latest(topic) : undefined;
      if (kept !== undefined) opts.post({ kind: 'topic', topic: topic, data: topicOf(kept).data, before: kept.before });
      if (kept === undefined || kept.before) ask(topic);
      reply(m.id, true);
      return;
    }
    if (m.kind === 'act') {
      const name = String(m.command || '');
      if (!declCommands.has(name)) {
        reply(m.id, false, { error: 'command not declared by this section: ' + name });
        return;
      }
      const entry = wired[name];
      if (!entry || (entry.builtInOnly && !opts.builtIn)) {

        reply(m.id, false, { error: 'command not wired by the frame: ' + name });
        return;
      }
      const args = (m.args && typeof m.args === 'object') ? m.args : {};
      const bad = entry.validate ? entry.validate(args, opts) : '';
      if (bad) { reply(m.id, false, { error: bad }); return; }
      if (entry.read) {
        Promise.resolve().then(function () { return entry.read(args, reads); }).then(
          function (data) { reply(m.id, true, { data: data }); },
          function (err) { reply(m.id, false, { error: String((err && err.message) || err) }); });
        return;
      }
      const sent = opts.sendToServer(entry.frame(args));
      if (!sent) { reply(m.id, false, { error: 'not connected: the command was not sent' }); return; }
      asked.set(sent, { id: m.id, entry: entry });
      return;
    }
    if (m.kind === 'resize') {
      const h = Number(m.height);
      if (isFinite(h) && opts.onResize) opts.onResize(Math.max(40, Math.min(4000, Math.round(h))));
      return;
    }

  }

  function publish(topic, data) {
    if (declTopics.has(topic) && subscribed.has(topic)) {
      opts.post({ kind: 'topic', topic: topic, data: data });
    }
  }

  function frame(msg) {
    if (msg.request_id) answer(msg);
    const t = topicOf(msg);
    if (t) publish(t.topic, t.data);
  }

  function ask(topic) {
    if (!opts.sendToServer) return;
    if (ASKED[topic]) opts.sendToServer({ type: 'query', query: ASKED[topic] });
    if (topic === 'backups') opts.sendToServer({ type: 'backups', backups: { action: 'status' } });
  }
  function refresh() { subscribed.forEach(ask); }

  function pushTokens(tokens) { opts.post({ kind: 'tokens', tokens: tokens }); }

  function answer(msg) {
    const was = msg ? asked.get(msg.request_id) : undefined;
    if (was === undefined) return;
    asked.delete(msg.request_id);
    if (msg.type === 'error') {
      if (was.entry.says) msg.said = true;
      reply(was.id, false, { error: msg.message || 'refused', code: msg.request_error_code });
      return;
    }
    if (was.entry.answered) { reply(was.id, true, { data: was.entry.answered(msg) }); return; }
    const t = topicOf(msg);
    reply(was.id, true, { data: t ? t.data : (msg[msg.type] !== undefined ? msg[msg.type] : { type: msg.type }) });
  }

  function lost() {
    asked.forEach(function (was) { reply(was.id, false, { error: 'the connection was lost before the host answered' }); });
    asked.clear();
  }

  return { onFrame: onFrame, publish: publish, pushTokens: pushTokens, frame: frame, answer: answer, lost: lost, refresh: refresh, subscribed: subscribed };
}
