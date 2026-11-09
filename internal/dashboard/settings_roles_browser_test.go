//go:build !windows

package dashboard

import (
	"testing"
)

// HELPERS LISTS WHAT THE IDENTITY IS TOLD OF EACH ROLE.
// Read-only: each configured role by name, sorted as the host sends them,
// with its provider entry, model and the operator's description as text;
// a role with no description says so;
// no role says where one is set. The panel's one control is unchanged.
func TestTheAgencyCardListsTheRolesTheIdentityIsToldOf(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div><div id="technical-stack"></div>
<script type="module">
import { S } from './state.js';
import './views/settings.js';
import { drawHelpers } from './views/settings/technical/forms.js';
import { assert, run } from './__harness.js';
run(() => {
  S.config = ` + settingsConfig + `;
  S.config.agency.roles = [
    { name: 'critic', provider: 'cloud', model: 'm1', description: 'Reads a draft and lists its <b>faults</b>.' },
    { name: 'judge', provider: 'local', model: 'm2' },
  ];
  S.providersLoaded = true; S.view = 'technical';
  const st = document.getElementById('technical-stack');
  drawHelpers(st);
  const rows = [...st.querySelectorAll('[data-role]')];
  assert(rows.map(r => r.dataset.role).join(',') === 'critic,judge', 'the roles: ' + rows.map(r => r.dataset.role).join(','));
  assert(rows[0].textContent === 'critic · cloud · m1Reads a draft and lists its <b>faults</b>.', 'the first role: ' + rows[0].textContent);
  assert(!rows[0].querySelector('b'), 'a description became markup');
  assert(rows[1].textContent === 'judge · local · m2No description.', 'a role with none: ' + rows[1].textContent);
  assert(!st.querySelector('[data-roles] input, [data-roles] textarea'), 'the list is read-only');
  assert(!document.getElementById('cfg-plr') && !st.querySelector('[data-save]'), 'a switch or a Save is back on a list with nothing to set');
});
</script>`
	runPageInEngines(t, page, settingsPageModules())
}
