import { S } from '../../state.js';
import { query } from '../../ws.js';
import W from '../../strings/identity.js';

let asking = '', now = { id: '', state: '', trail: null, reason: '', revision: '', lost: false };
const REFUSED = { 'belief.not_found': W.trailWhyNotFound, 'record.not_open': W.trailWhyNotOpen };

export const revisionOf = b => b ? JSON.stringify([b.statement, b.status, b.ring, b.evidence_count, b.confidence]) : '';

export function openTrail(id, revision = '', deliberate = false) {
  const same = now.id === id && now.revision === revision;
  if (same && asking) return;
  if (same && !deliberate && (now.state === 'shown' || (now.state === 'refused' && !now.lost))) return;
  asking = query('belief_trail', { belief_id: id }) || '';
  now = asking ? { id, state: 'loading', trail: null, reason: '', revision, lost: false } : { id, state: 'refused', trail: null, reason: W.trailWhyOffline, revision, lost: true };
}
export const trailNow = () => now;

S.beliefTrailAnswered = msg => {
  if (!asking || !msg.request_id || msg.request_id !== asking || !msg.belief_trail) return;
  asking = '';
  now = { id: now.id, state: 'shown', trail: msg.belief_trail, reason: '', revision: now.revision, lost: false };
  S.renderBeliefs?.();
};
S.beliefTrailRefused = (requestID, text, code) => {
  if (!asking || !requestID || requestID !== asking) return false;
  asking = '';
  now = { id: now.id, state: 'refused', trail: null, reason: REFUSED[code] || text || '', revision: now.revision, lost: false };
  S.renderBeliefs?.();
  return true;
};
S.beliefTrailLost = () => {
  if (!asking && now.state !== 'shown') return;
  asking = ''; now = { id: now.id, state: 'refused', trail: null, reason: W.trailWhyOffline, revision: now.revision, lost: true };
  S.renderBeliefs?.();
};
