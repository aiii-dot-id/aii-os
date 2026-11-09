//go:build !windows

package dashboard

import "testing"

// STOPPING A SIGN-IN SAYS THE SAME APPROVED WORDS EVERYWHERE. The Model
// page's stop button says the approved words; first run and the
// connected accounts said "Cancel sign-in" on their own. Both now say the
// Model page's words, the accounts list through its strings rather than a
// literal, and this reads the accounts button as drawn.
const signInStopWordsPage = substratePageMarkup + `<script type="module">
import { profilesHTML } from './views/plugins/accounts.js';
import WM from './strings/settings-model.js';
import WF from './strings/firstrun.js';
import WP from './strings/settings-permissions.js';
import { assert, run } from './__harness.js';
run(() => {
  assert(WF.signInCancel === WM.signInCancel,
    'first run says ' + JSON.stringify(WF.signInCancel) + ', the Model page ' + JSON.stringify(WM.signInCancel));
  const box = document.createElement('div');
  box.innerHTML = profilesHTML({ auth_profiles: [{ name: 'manual', scheme: 'oauth2', provider: 'manual-service', scopes: [], hosts: ['api.example:443'],
    state: 'disconnected', signin: { status: 'pending', manual: true, url: 'https://authority.example/authorize' } }],
    providers: [{ name: 'manual-service', services: [], hosts: ['api.example:443'], sign_in: 'manual' }] });
  const stop = box.querySelector('[data-profile-cancel-signin="manual"]');
  assert(stop, 'a pending account sign-in offers no way to stop it');
  assert(stop.textContent === WM.signInCancel && WP.accountCancelSignIn === WM.signInCancel,
    'the accounts stop button says ' + JSON.stringify(stop.textContent) + ', the Model page ' + JSON.stringify(WM.signInCancel));
});
</script>`

func TestStoppingASignInSaysTheSameApprovedWordsEverywhere(t *testing.T) {
	runPageInEngines(t, signInStopWordsPage, substrateModules(t))
}
