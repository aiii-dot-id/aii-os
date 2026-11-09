import { S } from '../state.js';
import { $ } from '../util.js';
import { renderWho } from './identity/who.js';
import { goTo } from './identity/page.js';

S.renderIdentityView = () => { if (S.view === 'identity') renderIdentity(); };

export function renderIdentity(entered = false) {
  renderWho(entered);
  const stack = $('identity-stack');
  if (!stack) return;
  const side = stack.querySelector('.who__side') || stack;
  let part = side.querySelector(':scope > [data-safe-part]');
  if (!S.cont || S.cont.mode !== 'safe') { part?.remove(); return; }
  if (!part) {
    part = document.createElement('section'); part.className = 'who__part'; part.dataset.safePart = '';
    part.innerHTML = '<div class="dim-note" data-safe-restore>This record is frozen. If it cannot be repaired, a snapshot can be put back: <a href="#" data-open-backups>restore from a backup</a>.</div>';
    part.querySelector('[data-open-backups]').onclick = e => { e.preventDefault(); goTo('#/settings/backups'); };
    side.append(part);
  }
}
