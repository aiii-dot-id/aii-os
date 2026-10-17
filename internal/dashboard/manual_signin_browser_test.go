//go:build !windows

package dashboard

import "testing"

func TestManualSignInInSettingsAndFirstboot(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames, setConnected } from './ws.js';
import { renderProviderOptions } from './firstboot.js';
import { renderSettings } from './views/settings.js';
import { run } from './__harness.js';
run(assert => {
  const provider = {name:'Configured OAuth',api_type:'anthropic',endpoint:'https://fixture.example',models:['m1'],default_model:'m1',can_sign_in:true,preselect:true,
    signin:{status:'pending',manual:true,url:'https://authority.example/authorize'}};
  S.providers=[provider];
  renderProviderOptions();
  assert(!document.querySelector('[data-signin-complete]'), 'firstboot prompted before explicit selection');
  document.getElementById('fb-provider').value='0';
  document.getElementById('fb-provider').onchange();
  let form=document.querySelector('#fb-signin [data-signin-complete]');
  assert(form, 'firstboot lacks the manual completion control');
  let input=form.querySelector('input');
  assert(input.type==='password' && input.autocomplete==='off', 'authorization code is exposed or stored by the form');
  input.value='first-code#first-state';
  setConnected(false); form.requestSubmit();
  assert(input.value==='first-code#first-state', 'disconnected submit discarded the code');
  assert(form.textContent.includes('Not connected'), 'disconnected submit lacks recovery guidance');
  setConnected(true); form.requestSubmit();
  assert(frames.at(-1).type==='provider_signin_complete' && frames.at(-1).provider===provider.name && frames.at(-1).input==='first-code#first-state', 'firstboot did not route the complete code to its provider');
  assert(input.value==='', 'submitted authorization code stayed in the page');
  assert(!frames.some(f=>f.type==='provider_signin'), 'render/reconnect started consent');
  S.identityExists=true; S.providersLoaded=true;
  S.config={llm:{provider:provider.name,model:'m1'}};
  renderSettings();
  document.querySelector('[data-sec="providers"]').click();
  document.querySelector('[data-prov-row="0"]').click();
  form=document.querySelector('#settings-stack [data-signin-complete]');
  assert(form, 'the actual provider editor lacks the manual completion control');
  form.querySelector('input').value='settings-code#settings-state'; form.requestSubmit();
  assert(frames.at(-1).type==='provider_signin_complete' && frames.at(-1).provider===provider.name && frames.at(-1).input==='settings-code#settings-state', 'settings did not route the complete code');
  provider.signin={status:'connected',manual:true}; renderSettings();
  assert(!document.querySelector('#settings-stack [data-signin-complete]'), 'terminal sign-in still offers completion');
  provider.signin={status:'pending',url:'https://authority.example/authorize'}; renderSettings();
  assert(!document.querySelector('#settings-stack [data-signin-complete]'), 'callback consent silently acquired manual completion');
  provider.signin={status:'pending',manual:true,url:'https://authority.example/new-attempt'}; renderSettings();
  form=document.querySelector('#settings-stack [data-signin-complete]');
  assert(form && form.querySelector('input').value==='', 'reconnect restored an old authorization code');
  document.querySelector('[data-prov-signin-cancel]').click();
  assert(frames.at(-1).type==='provider_signin_cancel', 'manual consent cannot be cancelled');
});
</script>`
	runPageInEngines(t, page, substrateModules(t))
}
