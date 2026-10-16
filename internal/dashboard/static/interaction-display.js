export const locationKey = (source, page, id) => JSON.stringify([page.identity || '', source, page.incarnation || '', id]);

export function displayInteractions(pages) {
  const entries = [], seen = new Map(), starts = new Map();
  for (const source of ['recorded', 'transient']) {
    const page = pages.get(source);
    for (const row of page?.rows || []) {
      const key = locationKey(source, page, row.id);
      if (seen.has(key)) {
        if (JSON.stringify(seen.get(key).row) !== JSON.stringify(row)) seen.get(key).conflict = true;
        continue;
      }
      const entry = { key, source, page, row, conflict: false };
      entries.push(entry); seen.set(key, entry);
      if (row.kind === 'tool_call') starts.set(key, entry);
    }
  }
  const groups = new Map(), owner = new Map();
  for (const entry of entries) {
    const { row, source, page } = entry;
    if (row.kind !== 'tool_call' && row.kind !== 'tool_result') continue;
    let start = row.kind === 'tool_call' ? entry : starts.get(locationKey(source, page, row.related_id));
    const durable = pages.get('recorded');
    if (!start && row.kind === 'tool_result' && source === 'transient' && row.details?.origin === 'safe_tool' &&
        page.identity && page.identity === durable?.identity && !page.stale && !page.error && !durable.stale && !durable.error) {
      start = starts.get(locationKey('recorded', durable, row.related_id));
    }
    const key = 'tool:' + (start?.key || locationKey(source, page, row.related_id || row.id));
    let group = groups.get(key);
    if (!group) {
      group = { key, source: start?.source || source, anchor: start || entry, tool: true, records: [], conflict: false };
      groups.set(key, group);
    }
    group.records.push(entry); group.conflict ||= entry.conflict;
    if (start && row.kind === 'tool_result' &&
        ((row.turn_id && start.row.turn_id && row.turn_id !== start.row.turn_id) ||
         (row.source && start.row.source && row.source.id !== start.row.source.id))) group.conflict = true;
    owner.set(entry.key, group);
  }
  const out = new Map([['recorded', []], ['transient', []]]), emitted = new Set(), items = new Map();
  for (const entry of entries) {
    const group = owner.get(entry.key);
    if (!group) {
      if (entry.row.kind === 'annotation' && entry.row.related_id) {
        const subject = locationKey(entry.source, entry.page, entry.row.related_id);
        (owner.get(subject) || items.get(subject))?.records.push(entry);
        continue;
      }
      const item = { key: entry.key, source: entry.source, anchor: entry, records: [entry], conflict: entry.conflict };
      items.set(entry.key, item); out.get(entry.source).push(item); continue;
    }
    if (emitted.has(group.key) || entry !== group.anchor) continue;
    emitted.add(group.key);
    const results = group.records.filter(e => e.row.kind === 'tool_result');
    if (results.length > 1) group.conflict = true;
    group.outcome = group.conflict ? 'conflict' : results.length ? results[0].row.outcome || 'unknown' : '';
    group.transient = group.records.some(e => e.source === 'transient');
    group.missingStart = !group.records.some(e => e.row.kind === 'tool_call');
    out.get(group.source).push(group);
  }
  return out;
}

const SPEAKERS = new Set(['operator', 'participant', 'resident']);
export function isWords(item) {
  const r = item.anchor.row;
  return !item.tool && (r.kind === 'message' || r.kind === 'outbound_message' || (r.kind === 'legacy' && SPEAKERS.has(r.role)));
}
const when = r => Date.parse(r.recorded_at || r.created_at || '');
export function isWork(item) {
  const r = item.anchor.row;
  if (item.tool) return !!r.turn_id;
  return (r.kind === 'notice' || r.kind === 'annotation') && !r.related_id;
}
export function foldTurns(items, page = {}) {
  const out = [];
  let run = null;
  const flush = next => {
    if (!run) return;
    const tools = run.items.filter(it => it.tool), times = run.items.map(it => when(it.anchor.row)).filter(t => !isNaN(t));
    const turns = new Set(run.items.map(it => it.anchor.row.turn_id).filter(Boolean));
    const reply = next && isWords(next) && next.anchor.row.role === 'resident' && turns.has(next.anchor.row.turn_id) ? next : null;
    const end = reply ? when(reply.anchor.row) : NaN;
    out.push({
      key: 'work:' + run.source + ':' + run.items[0].key, receipt: true, source: run.source, items: run.items, turns: [...turns],
      steps: tools.length, notes: run.items.length - tools.length,
      failed: tools.filter(it => it.outcome === 'failed' || it.outcome === 'refused').length,
      unknown: tools.filter(it => it.outcome === 'unknown').length,
      cancelled: tools.filter(it => it.outcome === 'cancelled').length,
      unfinished: tools.filter(it => !it.outcome).length,
      conflict: run.items.some(it => it.conflict),
      unsaved: run.items.filter(it => it.source === 'transient').length,
      unsavedCompletions: run.items.filter(it => it.source !== 'transient' && it.transient).length,
      replied: !!reply,
      start: times.length ? Math.min(...times) : NaN,
      end: !isNaN(end) ? end : times.length ? Math.max(...times) : NaN,
      startOutside: run.first === 0 && !!page.has_older,
      endOutside: run.last === items.length - 1 && !!page.has_newer,
    });
    run = null;
  };
  items.forEach((item, i) => {
    if (!isWork(item)) { flush(item); out.push(item); return; }
    if (!run) run = { source: item.source, items: [], first: i, last: i };
    run.items.push(item); run.last = i;
  });
  flush(null);
  return out;
}
