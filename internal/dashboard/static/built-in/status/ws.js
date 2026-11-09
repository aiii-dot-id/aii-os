import { S } from './state.js';

let api = null, asked = 0;
export const useBridge = a => { api = a; };

export function send(frame) {
  if (frame.type !== 'pass_now') return '';
  const id = 'pass-' + ++asked;
  api.act('pass.now', {}).then(
    answer => S.passNowAnswered?.({ request_id: id, pass_now: answer }),
    err => { if (/connection was lost/.test(err.message)) S.passNowLost?.(); else S.passNowRefused?.(id, err.message); });
  return id;
}

export function query(name, extra) {
  if (name !== 'spend') return '';
  const id = 'spend-' + ++asked;
  api.act('spend.read', extra || {}).then(answer => {
    S.spend = answer || { days: [] };
    S.spendRequest = id;
    S.renderStatus?.();
  }, () => {});
  return id;
}
