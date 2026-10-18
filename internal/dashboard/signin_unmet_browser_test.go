//go:build !windows

package dashboard

import (
	"bytes"
	"testing"
)

func TestAnEntryThatCannotOfferSignInSaysWhyOnThePage(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderProviderOptions } from './firstboot.js';
import { renderSettings } from './views/settings.js';
import { run } from './__harness.js';
run(assert => {
  const why = 'credential_options.file pins this entry to a credential file kept elsewhere <b>';
  const pinned = {name:'ChatGPT (mine)',api_type:'openai',endpoint:'https://fixture.example',models:['m1'],default_model:'m1',credential:'codex',sign_in_unmet:why,preselect:true};
  const keyed = {name:'By key',api_type:'openai',endpoint:'https://fixture.example',models:['m1'],default_model:'m1',has_key:true};
  S.providers=[pinned, keyed];
  renderProviderOptions();
  document.getElementById('fb-provider').value='0';
  document.getElementById('fb-provider').onchange();
  let said=document.querySelector('#fb-signin [data-signin-unmet]');
  assert(said && said.textContent.includes('Sign-in is not offered for this entry: ' + why), 'the birth form says nothing of an entry that cannot offer sign-in: ' + document.getElementById('fb-signin').innerHTML);
  assert(!said.querySelector('b'), 'the reason was put on the page as markup');
  assert(!document.getElementById('fb-signin-start'), 'the birth form offers a button the entry cannot have');
  document.getElementById('fb-provider').value='1';
  document.getElementById('fb-provider').onchange();
  assert(!document.querySelector('#fb-signin [data-signin-unmet]'), 'the birth form speaks of sign-in for an entry that carries a key');

  S.identityExists=true; S.providersLoaded=true;
  S.config={llm:{provider:pinned.name,model:'m1'}};
  renderSettings();
  document.querySelector('[data-sec="providers"]').click();
  document.querySelector('[data-prov-row="0"]').click();
  said=document.querySelector('#settings-stack [data-signin-unmet]');
  assert(said && said.textContent.includes(why), 'the provider editor says nothing of an entry that cannot offer sign-in');
  assert(!document.querySelector('#settings-stack [data-prov-signin]'), 'the editor offers a button the entry cannot have');
  delete pinned.sign_in_unmet; pinned.can_sign_in=true; renderSettings();
  assert(!document.querySelector('#settings-stack [data-signin-unmet]') && document.querySelector('#settings-stack [data-prov-signin]'), 'an entry that can sign in still says it cannot, or offers nothing');
  document.querySelector('[data-prov-row="1"]').click();
  assert(!document.querySelector('#settings-stack [data-signin-unmet]'), 'the editor speaks of sign-in for an entry that carries a key');
});
</script>`
	modules := substrateModules(t)
	passes := []byte(`export const esc = value => String(value ?? '');`)
	if !bytes.Contains(modules["/util.js"], passes) {
		t.Fatal("rig: the harness's util.js no longer has the escaper this test replaces")
	}
	modules["/util.js"] = bytes.Replace(modules["/util.js"], passes,
		[]byte(`export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');`), 1)
	runPageInEngines(t, page, modules)
}
