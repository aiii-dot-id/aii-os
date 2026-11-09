import { S } from '../../state.js';
import { act } from '../../ws.js';

let seen = { at: 0, seq: 0, known: false }, asked = false;
export function lastVisit() {
  if (!asked) {
    asked = true;
    act('visit').then(v => { seen = v; S.renderBetweenAgain?.(); }, () => { asked = false; });
  }
  return seen;
}
