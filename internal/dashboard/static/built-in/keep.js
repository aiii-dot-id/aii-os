import { collect, putBack } from '../shell/keep.js';

export async function keepInSection(api, id) {
  let soon = null;
  const say = () => { api.act('place.keep', { id, kept: collect(document.body) }).catch(() => {}); };
  const changed = () => { clearTimeout(soon); soon = setTimeout(say, 120); };
  for (const ev of ['scroll', 'toggle', 'input', 'change']) document.addEventListener(ev, changed, { capture: true, passive: true });
  const kept = await api.act('place.kept', { id }).catch(() => null);
  if (kept?.open) putBack(document.body, kept);
}
