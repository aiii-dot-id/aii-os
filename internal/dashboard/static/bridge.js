/* bridge.js — the frame side of section-api v0 (R66 UP2,
   UI_FRAME.md §4): the per-section bridge state machine and the two
   registries the frame enforces — the topic projections and the WIRED
   COMMANDS. Pure logic, no DOM, no socket: sections.js binds it to a
   real MessageChannel port and ws.js's send; the node bridge suite
   drives it with the same calls. The allowlists here are the UX layer;
   the server gates each command lands on are the wall (§1). */

/* --- topic projections --------------------------------------
   Every topic is a read-only projection of one ws.js dispatch case
   (the frame-owned socket). v0 maps exactly three:
     status   ← ServerMessage type "status"   → msg.stats
     projects ← ServerMessage type "projects" → msg.projects
     work     ← ServerMessage type "work"     → msg.work
   Sections never open sockets; this table is their whole view. */
export function topicOf(msg) {
  if (!msg) return null;
  switch (msg.type) {
    case 'status': return { topic: 'status', data: msg.stats || null };
    case 'projects': return { topic: 'projects', data: msg.projects || [] };
    case 'work': return { topic: 'work', data: msg.work || null };
  }
  return null;
}

/* --- the command registry -----------------------------------
   Command-registry discipline (§4): every command is authority
   surface, added the way tools are — deliberately, with its SERVER
   gate named in the registration. Convenience is not a reason.
   Each entry: validate(args) → '' or a refusal string;
   frame(args) → the ClientMessage the frame forwards over ws.js send.
   v0 wires exactly ONE command. */
export const WIRED_COMMANDS = {
  /* project.select — switch the shared project focus.
     Server gate: App.selectProject (internal/app/projects_adapter.go),
     the ONE focus-switch path both hands already share (R62): refuses
     closed projects, records the transition in the transcript. The
     wire is the existing {type:"project"} WS case; this frame entry
     adds no authority the operator's own bubbles don't have. */
  'project.select': {
    validate: function (args) {
      return (args && typeof args.id === 'string' && args.id) ? '' : 'project.select requires args.id (a project id string)';
    },
    frame: function (args) {
      return { type: 'project', project: { action: 'select', id: args.id } };
    },
  },
};

/* --- the per-section bridge ---------------------------------
   createFrameBridge wires ONE mounted section. opts:
     topics/commands — the section's DECLARED allowlists (section.json,
       verified with the package);
     wired    — the frame's command registry (WIRED_COMMANDS; injectable
       for tests);
     post(m)  — deliver a frame to the section's port;
     sendToServer(clientMsg) — ws.js send (the frame-owned socket);
     onResize(px) — frame applies the section's reported height.
   Refusals are REPLIES naming the thing refused — a section author
   sees why, immediately, in their own console. */
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
        /* Undeclared subscribe = refused reply (the declaration is the
           section's own signed statement of appetite). */
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
        /* Declared by the section but not wired by the frame: the
           double allowlist's second wall. */
        reply(m.id, false, { error: 'command not wired by the frame: ' + name });
        return;
      }
      const args = (m.args && typeof m.args === 'object') ? m.args : {};
      const bad = entry.validate ? entry.validate(args) : '';
      if (bad) { reply(m.id, false, { error: bad }); return; }
      opts.sendToServer(entry.frame(args));
      /* The WS protocol is broadcast-shaped (no request ids): the reply
         confirms the command passed both allowlists and was forwarded;
         OUTCOMES arrive as data on subscribed topics (a project switch
         comes back as the next "projects" projection) or as the frame's
         own error surface. */
      reply(m.id, true, { data: { forwarded: true } });
      return;
    }
    if (m.kind === 'resize') {
      const h = Number(m.height);
      if (isFinite(h) && opts.onResize) opts.onResize(Math.max(40, Math.min(4000, Math.round(h))));
      return;
    }
    /* Unknown kinds are ignored, not errors: the ABI is four
       primitives forever; a future section speaking a fifth gets
       silence, not a crash. */
  }

  /* publish relays one projection — ONLY if the section declared the
     topic AND subscribed to it. */
  function publish(topic, data) {
    if (declTopics.has(topic) && subscribed.has(topic)) {
      opts.post({ kind: 'topic', topic: topic, data: data });
    }
  }

  function pushTokens(tokens) { opts.post({ kind: 'tokens', tokens: tokens }); }

  return { onFrame: onFrame, publish: publish, pushTokens: pushTokens, subscribed: subscribed };
}
