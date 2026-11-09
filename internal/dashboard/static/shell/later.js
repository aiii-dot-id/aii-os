import { S } from '../state.js';

export const later = name => (...args) => S.later?.[name]?.(...args);
export const LATER = new Proxy({}, { get: (_, name) => later(name) });
export function offer(mod) { for (const [name, value] of Object.entries(mod)) if (typeof value === 'function') (S.later ||= {})[name] = value; }
