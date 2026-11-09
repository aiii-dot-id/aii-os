import { collect, putBack } from './keep.js';
import { reloaded } from './visit.js';

const KEY = 'aii.reloadKeep';
let sections = {}, given = {};
export const keepOfSection = (id, kept) => { sections[id] = kept; };
export const keptOfSection = id => { const kept = given[id] || null; delete given[id]; return kept; };

export function keepThroughReload() {
  const root = document.querySelector('.stage');
  if (!root) return;
  window.addEventListener('pagehide', () => {
    try { sessionStorage.setItem(KEY, JSON.stringify({ hash: location.hash, page: collect(root), sections })); } catch (err) { }
  });
  if (!reloaded()) return;
  let was = null;
  try { was = JSON.parse(sessionStorage.getItem(KEY) || 'null'); } catch (err) { was = null; }
  if (!was || was.hash !== location.hash) return;
  given = was.sections || {};
  sections = { ...given };
  putBack(root, was.page);
}
