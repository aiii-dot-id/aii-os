

export function topicOf(msg) {
  if (!msg) return null;
  switch (msg.type) {
    case 'status': return { topic: 'status', data: msg.stats || null };
    case 'projects': return { topic: 'projects', data: msg.projects || [] };
    case 'work': return { topic: 'work', data: msg.work || null };
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
};

export function createFrameBridge(opts) {
  const declTopics = new Set(opts.topics || []);
  const declCommands = new Set(opts.commands || []);
  const wired = opts.wired || {};
  const subscribed = new Set();

  function reply(id, ok, extra) {
    const m = Object.assign({ kind: 'reply', id: id, ok: ok }, extra || {});
    opts.post(m);
  }

  function onFrame(m) {
    if (!m || typeof m !== 'object') return;
    if (m.kind === 'subscribe') {
      const topic = String(m.topic || '');
      if (!declTopics.has(topic)) {

        reply(m.id, false, { error: 'topic not declared by this section: ' + topic });
        return;
      }
      subscribed.add(topic);
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
      if (!entry) {

        reply(m.id, false, { error: 'command not wired by the frame: ' + name });
        return;
      }
      const args = (m.args && typeof m.args === 'object') ? m.args : {};
      const bad = entry.validate ? entry.validate(args) : '';
      if (bad) { reply(m.id, false, { error: bad }); return; }
      opts.sendToServer(entry.frame(args));

      reply(m.id, true, { data: { forwarded: true } });
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

  function pushTokens(tokens) { opts.post({ kind: 'tokens', tokens: tokens }); }

  return { onFrame: onFrame, publish: publish, pushTokens: pushTokens, subscribed: subscribed };
}
