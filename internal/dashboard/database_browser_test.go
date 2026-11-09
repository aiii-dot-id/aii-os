//go:build !windows

package dashboard

import "testing"

func TestDatabaseSettingsDistinguishPreferenceFromActive(t *testing.T) {
	probe := `
import { S } from '/state.js';
import { frames } from '/ws.js';
import { acceptSettingsConfig } from '/views/settings.js';
import { drawStorage } from '/views/settings/technical/forms.js';
import { run } from '/__harness.js';
run(async assert => {
  S.providersLoaded=true; S.providers=[];
  S.config={llm:{},database:{preferred:'',active:'sqlite',can_export:true,dictionary_bytes:0},restart_required:[]};
  const st=document.body.appendChild(Object.assign(document.createElement('div'),{id:'technical-stack'}));
  S.view='technical'; S.renderTechnical=()=>drawStorage(st); const renderSettings=S.renderTechnical; renderSettings();
  assert(document.querySelector('#db-active').textContent==='Standard','active format missing');
  assert(!document.querySelector('#cfg-db-levels').checked && !document.querySelector('#cfg-db-dictionary').checked,'optimization defaults are not off');
  assert(document.querySelector('#db-dictionary-bytes').textContent==='0 bytes','actual dictionary absent from readback');
  const form=document.querySelector('form[action="/database/export"]');
  assert(form && form.method==='post','export is not a same-origin POST: a link on another page could trigger it');
  assert(form.querySelector('button[type="submit"]'),'the export form has no button to press');
  const response=await fetch(form.action,{method:'POST'});
  assert(await response.text()==='export-fixture','same-origin export POST is blocked by uiCSP');
  document.querySelector('#cfg-db-format').value='zstd';
  document.querySelector('[data-save="technical"]').click();
  const request=frames.at(-1);
  assert(request.type==='config_set' && request.config['identity.db_format']==='zstd','wrong preference wire');
  assert(document.querySelector('#db-active').textContent==='Standard','save pretended conversion happened');
  S.config.database.preferred='zstd'; S.config.restart_required=['identity.db_format'];
  assert(acceptSettingsConfig(request.request_id),'reply not accepted'); renderSettings();
  const feedback=document.querySelector('[data-said="technical"]');
  assert(feedback && feedback.classList.contains('good') && feedback.textContent.includes('next normal start'),'preference readback failed');
  assert(document.querySelector('#db-active').textContent==='Standard','acknowledgment changed active state');
  S.config.database={preferred:'zstd',active:'zstd',can_export:true}; renderSettings();
  assert(document.querySelector('#db-active').textContent==='Compressed','observed activation not rendered');
  document.querySelector('#cfg-db-levels').checked=true;
  document.querySelector('#cfg-db-dictionary').checked=true;
  document.querySelector('[data-save="technical"]').click();
  const optimized=frames.at(-1);
  assert(optimized.config['identity.db_optimize_levels']===true && optimized.config['identity.db_learn_dictionary']===true,'optimization choices not sent');
  S.config.database.optimize_levels=true; S.config.database.learn_dictionary=true;
  assert(acceptSettingsConfig(optimized.request_id),'optimization readback not accepted'); renderSettings();
  assert(document.querySelector('#cfg-db-levels').checked && document.querySelector('#cfg-db-dictionary').checked,'choices lost after readback');
  assert(document.querySelector('#db-dictionary-bytes').textContent==='not known','missing dictionary observation fabricated zero');
  S.config.database.dictionary_bytes=1234; renderSettings();
  assert(document.querySelector('#db-dictionary-bytes').textContent===(1234).toLocaleString()+' bytes','observed dictionary not shown');
  document.querySelector('#cfg-db-dictionary').checked=false;
  document.querySelector('[data-save="technical"]').click();
  const stopped=frames.at(-1); S.config.database.learn_dictionary=false;
  assert(stopped.config['identity.db_optimize_levels']===true && stopped.config['identity.db_learn_dictionary']===false && acceptSettingsConfig(stopped.request_id),'learning cannot be disabled independently'); renderSettings();
  assert(document.querySelector('#db-dictionary-bytes').textContent===(1234).toLocaleString()+' bytes','disabling learning pretended a dictionary was erased');
  document.querySelector('#cfg-db-format').value=''; document.querySelector('[data-save="technical"]').click();
  const cleared=frames.at(-1); S.config.database.preferred='';
  assert(cleared.config['identity.db_format']==='' && acceptSettingsConfig(cleared.request_id),'keep-current is not explicit clear');
  S.config.database.can_export=false; S.config.database.notice='<img src=x onerror=alert(1)> refused';
  S.config.database.recovery=['/private/<candidate>']; renderSettings();
  assert(!document.querySelector('form[action="/database/export"]'),'SAFE export offered');
  assert(!st.querySelector('img'),'notice was not escaped');
  assert(st.textContent.includes('/private/<candidate>'),'recovery path invisible');
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
