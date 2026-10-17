//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestAnOverlayUpdateWaitsForUnsavedWork(t *testing.T) {
	page := formRecoveryMarkup + `<script type="module">
import {S} from '/state.js';import {frames} from '/ws.js';import {renderPlugins} from '/views/plugins.js';
import {acceptSettingsConfig} from '/views/settings.js';import {contractPending,acceptContractSave} from '/views/projects.js';
import {onOverlayChanged} from '/overlay.js';import {report,until} from '/__harness.js';
const step=sessionStorage.getItem('dr08'), at=s=>sessionStorage.setItem('dr08',s);
const notice=()=>document.getElementById('overlay-update');
let ticks=0;const every=window.setInterval;window.setInterval=(fn,ms)=>every(()=>{ticks++;fn();},ms);
const twoChecks=async()=>{const from=ticks;await until(()=>ticks>=from+2,10000);return ticks>=from+2;};
const stored=()=>[sessionStorage,localStorage].some(s=>Object.keys(s).some(k=>String(s.getItem(k)).includes('dummy')));
const held=['setting held','key pending','contract held','contract pending'];
(async()=>{
 if(stored())return report('FAIL a typed value was written to storage');
 if(held.includes(step))return report('FAIL the page reloaded while it held work: '+step);
 if(step===null){` + pluginFormSetup + `
  type(limit(),'19');type(key(),'dummy-held-key');
  at('setting held');onOverlayChanged(1,['/app.js']);
  if(!notice()||notice().getAttribute('role')!=='status')return report('FAIL no notice while a setting was unsaved');
  onOverlayChanged(2,['/views/home.js']);
  if(document.querySelectorAll('#overlay-update').length!==1)return report('FAIL two updates made two notices');
  if(!await twoChecks())return report('FAIL the deferred reload never re-checked');
  if(limit().value!=='19'||key().value!=='dummy-held-key'||stored())return report('FAIL the held work was touched or stored');
  save().click();const kept=frames.at(-1);
  if(kept.type!=='plugin_key_set')return report('FAIL the key was not sent first: '+kept.type);
  at('key pending');if(!await twoChecks())return report('FAIL no re-check while the key save was owed');
  S.config.plugins.installed[0].settings[1].key_kept=true;acceptSettingsConfig(kept.request_id);renderPlugins();
  const settings=frames.at(-1);if(settings.type!=='config_set')return report('FAIL the settings did not follow the kept key');
  S.config.plugins.installed[0].settings[0].value=19;
  at('setting saved');acceptSettingsConfig(settings.request_id);renderPlugins();
  return;
 }
 if(step==='setting saved'){
  if(notice())return report('FAIL a reloaded page kept the notice');
  S.contractDraft={id:'p1',outcome:'dummy outcome',acceptance:'',constraints:'',parent:null};
  at('contract held');onOverlayChanged(1,['/views/projects.js']);
  if(!notice())return report('FAIL no notice while a contract was unsaved');
  if(!await twoChecks())return report('FAIL the deferred reload never re-checked');
  contractPending.arm({draft:null},'r-contract');S.contractDraft=null;
  at('contract pending');if(!await twoChecks())return report('FAIL no re-check while the contract save was owed');
  at('contract saved');acceptContractSave('r-contract');
  return;
 }
 if(step==='contract saved'){
  S.focusDraft={id:'p1',val:'dummy focus'};onOverlayChanged(1,['/app.js']);
  const now=notice()&&[...notice().querySelectorAll('button')].find(b=>/Reload now/.test(b.textContent));
  if(!now)return report('FAIL no way to reload now');
  at('chose reload');now.click();
  return;
 }
 if(step==='chose reload'){at('control');onOverlayChanged(1,['/app.js']);return;}
 if(step==='control')return report(notice()?'FAIL a reloaded page kept the notice':'OK');
 report('FAIL an unknown step: '+step);
})().catch(e=>report('FAIL '+(e&&e.message||e)));
</script>`
	runPageInEngines(t, page, formRecoveryModules())
}

func TestEveryFormOwnerHoldsTheReloadForItsWork(t *testing.T) {
	for _, owner := range []string{
		"static/views/settings.js", "static/views/projects.js", "static/views/work.js",
		"static/views/backups.js", "static/signin.js", "static/firstboot-restore.js",
	} {
		src, err := staticFS.ReadFile(owner)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "holdReloadWhile(() =>") {
			t.Errorf("%s holds work a reload would erase and does not report it (holdReloadWhile)", owner)
		}
	}
}

func TestAnUnsavedEngineChoiceHoldsTheReload(t *testing.T) {
	page := formRecoveryMarkup + `<script type="module">
import {S} from '/state.js';import {frames,connected} from '/ws.js';
import {renderSettings,acceptSettingsConfig,rejectSettingsConfig,settingsConnectionLost} from '/views/settings.js';
import {onOverlayChanged} from '/overlay.js';import {report,until} from '/__harness.js';
const step=sessionStorage.getItem('dr08-engine'), at=s=>sessionStorage.setItem('dr08-engine',s);
const notice=()=>document.getElementById('overlay-update');
let ticks=0;const every=window.setInterval;window.setInterval=(fn,ms)=>every(()=>{ticks++;fn();},ms);
const twoChecks=async()=>{const from=ticks;await until(()=>ticks>=from+2,10000);return ticks>=from+2;};
const el=id=>document.getElementById(id);
const choose=(id,v)=>{const n=el(id);n.value=v;n.dispatchEvent(new Event('change',{bubbles:true}));};
const svc=name=>({name,added:true,has_key:true,speech:{stt:{},tts:{}}});
const speech=()=>{S.identityExists=true;S.view='settings';S.providersLoaded=true;S.providers=[];
 S.config={llm:{},restart_required:[],speech:{services:[svc('Alpha'),svc('Beta')],stt:{provider:'Alpha',model:'',monthly_minutes:100},tts:{provider:'Alpha',voice:'',monthly_characters:1000}}};
 renderSettings();document.querySelector('[data-sec="speech"]').click();};
const saved=()=>{document.querySelector('[data-save="speech"]').click();return frames.filter(f=>f.type==='config_set').at(-1);};
const hostKeeps=sent=>{for(const [path,v] of Object.entries(sent)){const k=path.split('.');let o=S.config;k.slice(0,-1).forEach(x=>{o=o[x]=o[x]||{};});o[k.at(-1)]=v;}};
const held=['hearing held','speaking held','speaking refused','speaking offline'];
(async()=>{
 if(held.includes(step))return report('FAIL the page reloaded while an engine choice was unsaved: '+step);
 if(step===null){speech();
  choose('sp-provider-stt','Beta');
  at('hearing held');onOverlayChanged(1,['/app.js']);
  if(!notice())return report('FAIL no notice while only the hearing engine was unsaved');
  onOverlayChanged(2,['/views/home.js']);
  if(document.querySelectorAll('#overlay-update').length!==1)return report('FAIL two updates made two notices');
  if(!await twoChecks())return report('FAIL the deferred reload never re-checked');
  if(el('sp-provider-stt').value!=='Beta')return report('FAIL the held choice was touched');
  const req=saved();
  if(!req||req.config['speech.stt.provider']!=='Beta')return report('FAIL the save did not carry the hearing engine');
  hostKeeps(req.config);
  at('hearing saved');acceptSettingsConfig(req.request_id);renderSettings();
  return;
 }
 if(step==='hearing saved'){
  if(notice())return report('FAIL a reloaded page kept the notice');
  speech();choose('sp-provider-tts','Beta');
  at('speaking held');onOverlayChanged(1,['/app.js']);
  if(!notice())return report('FAIL no notice while only the speaking engine was unsaved');
  const req=saved();
  if(!req||req.config['speech.tts.provider']!=='Beta')return report('FAIL the save did not carry the speaking engine');
  at('speaking refused');rejectSettingsConfig('refused for the test',req.request_id);renderSettings();
  if(!await twoChecks())return report('FAIL no re-check after a refused save');
  at('speaking offline');connected(false);settingsConnectionLost();renderSettings();
  if(!await twoChecks())return report('FAIL no re-check after the connection was lost');
  if(el('sp-provider-tts').value!=='Beta')return report('FAIL the refused choice was lost');
  const now=[...notice().querySelectorAll('button')].find(b=>/Reload now/.test(b.textContent));
  if(!now)return report('FAIL no way to reload now');
  at('chose reload');now.click();
  return;
 }
 if(step==='chose reload'){at('control');onOverlayChanged(1,['/app.js']);return;}
 if(step==='control')return report(notice()?'FAIL a reloaded page kept the notice':'OK');
 report('FAIL an unknown step: '+step);
})().catch(e=>report('FAIL '+(e&&e.message||e)));
</script>`
	runPageInEngines(t, page, formRecoveryModules())
}
