import WM from '../../strings/settings-model.js';
import { S } from '../../state.js';
import { fill } from '../../util.js';

export const STATUS_DOT = {
  ok: ['#3fa34d', 'reachable — models listed live'],
  auth_required: ['#c9a227', 'needs an API key to list models'],
  key_refused: ['#c9a227', 'refused the key it was given'],
  no_credential: ['#c9a227', 'adopted credential unavailable'],
  credential_expired: ['#c9a227', 'adopted credential expired — sign in again, or refresh it with the tool it is borrowed from'],
  unreachable: ['#c0392b', 'did not list its models'],
  not_running: ['#777', 'not running on this computer'],
  invalid_url: ['#c0392b', 'URL is not valid'],
  broken: ['#c0392b', 'broken entry in providers.json — not used until repaired or removed'],
};

export const statusWords = status => (STATUS_DOT[status] || [null, 'not probed yet'])[1];

export const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };

function modelNamed(kind, changes) {
  if (changes['llm.model']) return changes['llm.model'];
  if (kind === 'adopted' && S.config && S.config.llm && S.config.llm.resolved_model) return S.config.llm.resolved_model;
  const p = (S.providers || []).find(x => x.name === changes['llm.provider']) || (S.providers || []).find(x => x.default);
  return (p && (p.default_model || p.name)) || changes['llm.provider'] || '';
}
export function savesAModel(changes) {
  return !!changes && ('llm.provider' in changes || 'llm.model' in changes) && !!modelNamed('checking', changes);
}
export const modelAnswered = () => !!(S.config && S.config.llm && S.config.llm.answered);
export function modelSays(kind, changes) {
  return fill(WM[kind], { model: modelNamed(kind, changes || {}), name: nameOf() || WM.unnamed });
}
