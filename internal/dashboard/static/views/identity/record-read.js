import { signedFetch } from '../../pagekey.js';

export async function readRecord(params) {
  const res = await signedFetch('/record?' + new URLSearchParams(params), { cache: 'no-store', credentials: 'same-origin' });
  if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
  return res.json();
}

let born = null;
export function firstEntryTime() {
  born ||= readRecord({ before: '2' }).then(page => {
    const t = Date.parse((page.rows || [])[0]?.time || '');
    if (isNaN(t)) throw new Error('the record has no first entry');
    return t;
  }).catch(err => { born = null; throw err; });
  return born;
}

export async function entriesSince(since, most) {
  const rows = [];
  for (let before = ''; ;) {
    const page = await readRecord(before ? { before } : {});
    for (const row of page.rows || []) {
      if (Number(row.seq) <= since) return { rows, more: false };
      if (rows.length === most) return { rows, more: true };
      rows.push(row);
    }
    if (!page.older) return { rows, more: false };
    before = page.older;
  }
}

export async function payloadOf(row, rangeBytes) {
  const head = String(row.head || ''), total = Number(row.bytes) || 0;
  let offset = new TextEncoder().encode(head).length;
  if (offset >= total) return head;
  const decoder = new TextDecoder('utf-8');
  let text = head;
  while (offset < total) {
    const res = await signedFetch('/record/payload?' + new URLSearchParams({ seq: row.seq, offset: String(offset), length: String(rangeBytes || 65536) }), { cache: 'no-store', credentials: 'same-origin' });
    if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
    const bytes = new Uint8Array(await res.arrayBuffer());
    if (!bytes.length) break;
    text += decoder.decode(bytes, { stream: true });
    offset += bytes.length;
  }
  return text + decoder.decode();
}

const KINDS = {
  'belief.upsert': 'kindNewBelief', 'belief.supersede': 'kindRevised', 'belief.promote': 'kindRevised', 'belief.archive': 'kindRevised',
  'experience.create': 'kindExperience', 'relationship.upsert': 'kindCharter', 'intention.create': 'kindIntention',
  'intention.state_change': 'kindIntention', 'commitment.promised': 'kindIntention', 'commitment.state_change': 'kindIntention',
  'self_model.synthesize': 'kindReflection', 'consolidation.run': 'kindReflection', 'dream.run': 'kindReflection',
};
export const kindWord = (type, W) => (KINDS[type] && W[KINDS[type]]) || '';
const RECORD_KINDS = { 'ring0.genesis': 'kindBorn', 'network.name_claimed': 'kindWebAddress', 'system.witnessed': 'kindChecked' };
export const recordWord = (type, W) => kindWord(type, W) || W[RECORD_KINDS[type]] || W.kindRecorded;

export function fieldOf(head, name) {
  const m = String(head || '').match(new RegExp('"' + name + '"\\s*:\\s*"((?:[^"\\\\]|\\\\.)*)("?)'));
  if (!m) return '';
  let text = m[1];
  try { text = JSON.parse('"' + text.replace(/\\$/, '') + '"'); } catch (err) { }
  return m[2] ? text : text + ' …';
}
