import { S } from '../../state.js';
import { fill } from '../../util.js';
import W from '../../strings/settings-people.js';

const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
export const named = (text, slots) => fill(text, { name: nameOf() || W.unnamed, ...slots });
export const say = (text, slots) => { const out = named(text, slots); return out.charAt(0).toUpperCase() + out.slice(1); };
