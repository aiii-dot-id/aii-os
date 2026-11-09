import { sheet } from './sheet.js';
import { door } from '../views.js';
import W from '../strings/shell.js';

let menu = null;
export function fetchMenu() {
  menu ||= Promise.all([import('./menu.js'), sheet('shell/menu.css')]).then(([m]) => m, err => { menu = null; throw err; });
  return menu;
}
const notOpened = err => import('../recovery.js').then(r => r.partFailed(W.search, err), () => {});
const opened = act => { const holds = door(); return fetchMenu().then(m => { if (holds()) m[act](); }, notOpened); };
export const openSearch = () => opened('openMenu');
const openKeysList = () => opened('openKeys');

const typing = el => !!el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName));
const JUMP = { c: '#/chat', p: '#/projects', a: '#/identity', s: '#/settings' };
let pendingG = 0;
document.addEventListener('keydown', e => {
  if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === 'k') { e.preventDefault(); openSearch(); return; }
  if (e.ctrlKey || e.metaKey || e.altKey || typing(e.target) || document.querySelector('dialog.c-menu[open]')) return;
  if (e.key === '?') { e.preventDefault(); openKeysList(); return; }
  const k = e.key.toLowerCase();
  if (pendingG && Date.now() - pendingG < 1500 && JUMP[k]) { e.preventDefault(); pendingG = 0; location.hash = JUMP[k]; return; }
  pendingG = k === 'g' ? Date.now() : 0;
});
