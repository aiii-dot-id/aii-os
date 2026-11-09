import { S } from './state.js';

let api = null, asked = 0;
export const useBridge = a => { api = a; };
export const act = (command, args) => api.act(command, args);
export const here = { base: '', hash: '', redraw: () => {} };

export function send(frame) {
  const choice = frame.type === 'config_set' && frame.config ? frame.config['memory.meaning_source'] : undefined;
  if (typeof choice !== 'string') return '';
  act('meaning.source', { choice }).catch(() => {});
  return 'source-' + ++asked;
}

export function query(name, extra) {
  if (!S.connected) return '';
  if (name === 'recall') {
    act('recall.search', { q: extra.q }).then(a => { S.recall = { query: a.query || '', text: a.message || '' }; here.redraw(); }, () => {});
    return 'recall-' + ++asked;
  }
  if (name === 'belief_trail') {
    const id = 'trail-' + ++asked;
    act('belief.trail', { id: extra.belief_id }).then(
      answer => S.beliefTrailAnswered?.({ request_id: id, belief_trail: answer }),
      err => { if (/connection was lost|not connected/.test(err.message)) S.beliefTrailLost?.(); else S.beliefTrailRefused?.(id, err.message, err.code || ''); });
    return id;
  }
  return '';
}
