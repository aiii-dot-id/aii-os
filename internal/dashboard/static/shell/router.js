import { TABS, REDIRECTS, VIEWS } from '../registry.js';

export function route(hash) {
  const m = /^#\/(section:[^/]+|[a-z]+)(?:\/(.+))?$/.exec(hash || '');
  if (!m) return null;
  const head = m[1];
  let rest = m[2] || null;
  if (rest !== null) {
    try { rest = decodeURIComponent(rest); } catch (err) { }
  }
  if (REDIRECTS[head]) return { redirect: REDIRECTS[head] + (m[2] ? '/' + m[2] : '') };
  const tabs = TABS[head];
  if (tabs) {
    const [first, ...more] = (rest || '').split('/');
    const tab = tabs.find(([id]) => id === first);
    if (tab) return { view: tab[1], project: tab[1] === 'settings' ? rest : more.length ? more.join('/') : null };
    if (head === 'settings' && rest) return { view: 'settings', project: rest };
    return { view: tabs[0][1], project: rest };
  }
  return { view: head, project: rest };
}

export function addressOf(view) {
  const v = VIEWS[view];
  if (!v || !v.tab) return '#/' + view;
  return '#/' + v.place + '/' + v.tab;
}
