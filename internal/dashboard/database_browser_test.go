//go:build !windows

package dashboard

import "testing"

func TestDatabaseSettingsDistinguishPreferenceFromActive(t *testing.T) {
	probe := `
import { S } from '/state.js';
import { frames } from '/ws.js';
import { renderSettings, acceptSettingsConfig } from '/views/settings.js';
import { run } from '/__harness.js';
run(async assert => {
  S.providersLoaded=true; S.providers=[];
  S.config={llm:{},database:{preferred:'',active:'sqlite',can_export:true},restart_required:[]};
  renderSettings(); document.querySelector('[data-sec="storage"]').click();
  assert(document.querySelector('#db-active').textContent==='sqlite','active format missing');
  // A POST, not a link: the authentication cookie is SameSite=Lax, which a
  // browser sends on a cross-site GET navigation, so a link on another page
  // could make this identity write its database into Downloads. uiCSP admits
  // form-action 'self' for this one form and no external destination
  // (review; this replaces the anchor an earlier reading of the
  // policy chose).
  const form=document.querySelector('form[action="/database/export"]');
  assert(form && form.method==='post','export is not a same-origin POST: a link on another page could trigger it');
  assert(form.querySelector('button[type="submit"]'),'the export form has no button to press');
  const response=await fetch(form.action,{method:'POST'});
  assert(await response.text()==='export-fixture','same-origin export POST is blocked by uiCSP');
  document.querySelector('#cfg-db-format').value='zstd';
  document.querySelector('[data-save="storage"]').click();
  const request=frames.at(-1);
  assert(request.type==='config_set' && request.config['identity.db_format']==='zstd','wrong preference wire');
  assert(document.querySelector('#db-active').textContent==='sqlite','save pretended conversion happened');
  S.config.database.preferred='zstd'; S.config.restart_required=['identity.db_format'];
  assert(acceptSettingsConfig(request.request_id),'reply not accepted'); renderSettings();
  const feedback=document.querySelector('[data-said="storage"]');
  assert(feedback && feedback.classList.contains('good') && feedback.textContent.includes('next normal startup'),'preference readback failed');
  assert(document.querySelector('#db-active').textContent==='sqlite','acknowledgment changed active state');
  S.config.database={preferred:'zstd',active:'zstd',can_export:true}; renderSettings();
  assert(document.querySelector('#db-active').textContent==='zstd','observed activation not rendered');
  document.querySelector('#cfg-db-format').value=''; document.querySelector('[data-save="storage"]').click();
  const cleared=frames.at(-1); S.config.database.preferred='';
  assert(cleared.config['identity.db_format']==='' && acceptSettingsConfig(cleared.request_id),'keep-current is not explicit clear');
  S.config.database.can_export=false; S.config.database.notice='<img src=x onerror=alert(1)> refused';
  S.config.database.recovery=['/private/<candidate>']; renderSettings();
  assert(!document.querySelector('form[action="/database/export"]'),'SAFE export offered');
  assert(!document.querySelector('#settings-stack img'),'notice was not escaped');
  assert(document.querySelector('#settings-stack').textContent.includes('/private/<candidate>'),'recovery path invisible');
});`
	modules := substrateModules(t)
	modules["/probe.js"] = []byte(probe)
	modules["/database/export"] = []byte("export-fixture")
	util, err := staticFS.ReadFile("static/util.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/util.js"] = util
	runPageInEnginesWithHeaders(t, substratePageMarkup+`<script type="module" src="/probe.js"></script>`, modules,
		map[string]string{"Content-Security-Policy": uiCSP})
}
