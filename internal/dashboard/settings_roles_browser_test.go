//go:build !windows

package dashboard

import (
	"testing"
)

func TestTheAgencyCardListsTheRolesTheIdentityIsToldOf(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.config = ` + settingsConfig + `;
  S.config.agency.roles = [
    { name: 'critic', provider: 'cloud', model: 'm1', description: 'Reads a draft and lists its <b>faults</b>.' },
    { name: 'judge', provider: 'local', model: 'm2' },
  ];
  S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  document.querySelector('[data-sec="agency"]').click();
  const st = document.getElementById('settings-stack');
  const rows = [...st.querySelectorAll('[data-role]')];
  assert(rows.map(r => r.dataset.role).join(',') === 'critic,judge', 'the roles: ' + rows.map(r => r.dataset.role).join(','));
  assert(rows[0].textContent === 'critic cloud / m1Reads a draft and lists its <b>faults</b>.', 'the first role: ' + rows[0].textContent);
  assert(!rows[0].querySelector('.role-desc b'), 'a description became markup');
  assert(rows[1].textContent === 'judge local / m2no description: the identity is shown the name alone', 'a role with none: ' + rows[1].textContent);
  assert(!st.querySelector('[data-roles] input, [data-roles] textarea'), 'the list is read-only');
  assert(document.getElementById('cfg-plr'), 'the routing switch is gone');
  assert(/told of each/.test(st.querySelector('h3').title), 'the card’s sentence does not say what it shows: ' + st.querySelector('h3').title);

  S.config.agency.roles = undefined;
  renderSettings();
  const none = st.querySelector('[data-roles]');
  assert(none && /^No role is configured\./.test(none.textContent) && /agency\.roles/.test(none.textContent), 'no role: ' + (none && none.textContent));
});
</script>`
	runPageInEngines(t, page, settingsPageModules())
}
