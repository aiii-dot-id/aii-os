//go:build !windows

package dashboard

import "testing"

func TestSpeechHasOneSaveAndVerifiesSemanticReadback(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptSettingsConfig, rejectSettingsConfig } from './views/settings.js';
import { echoMode } from './voice-echo.js';
import { assert, run } from './__harness.js';
run(async()=>{
 const el=id=>document.getElementById(id);
 const edit=(id,v)=>{el(id).value=v;el(id).dispatchEvent(new Event('change',{bubbles:true}));};
 const save=()=>{document.querySelector('[data-save="speech"]').click();return frames.filter(f=>f.type==='config_set').at(-1);};
 S.identityExists=true;S.providersLoaded=true;S.providers=[];
 S.config={llm:{},speech:{stt:{provider:'',model:'',language:'',monthly_minutes:0},tts:{provider:'',model:'',voice:'',monthly_characters:0},services:[],speakers:{mode:'all',uids:[],unidentified:'deliver',revision:1},mode:{listen:'interactive',speak:'auto',revision:1}}};
 S.openSettings('speech');
 assert(document.querySelectorAll('#speech-form [data-save]').length===1,'Speech has multiple Save buttons');
 edit('sp-speakers-mode','only');edit('sp-speakers-uids','speaker-z, speaker-a speaker-z');
 edit('sp-mode-speak','');edit('sp-echo-mode','combined');
 assert(echoMode()==='browser','selection took effect before Save');
 renderSettings();
 assert(el('sp-echo-mode').value==='combined'&&el('sp-speakers-uids').value.includes('speaker-z'),'broadcast discarded edits');
 const first=save();
 assert(first.config['speech.mode'].speak===''&&first.config['speech.speakers'].mode==='only','one transaction did not include both policies');
 assert(!acceptSettingsConfig('foreign'),'foreign ack consumed Save');
 assert(echoMode()==='browser','unconfirmed save changed echo');
 S.config.speech.speakers={mode:'only',uids:['speaker-a','speaker-z'],unidentified:'withhold',revision:2,withheld_finals:4};
 S.config.speech.mode={listen:'interactive',speak:'auto',revision:2,name:'interactive',set:true};
 assert(acceptSettingsConfig(first.request_id),'Save reply not claimed');renderSettings();
 assert(document.querySelector('[data-said="speech"].good'),'semantic save rejected');
 assert(echoMode()==='combined','echo not persisted');
 const reloaded=await import('./voice-echo.js?reload=1');
 assert(reloaded.echoMode()==='combined','fresh module lost saved echo');
 edit('sp-mode-speak','off');edit('sp-echo-mode','native');
 const second=save();
 acceptSettingsConfig(second.request_id);renderSettings();
 assert(document.querySelector('[data-said="speech"].bad'),'genuine mismatch falsely accepted');
 assert(el('sp-mode-speak').value==='off'&&el('sp-echo-mode').value==='native'&&echoMode()==='combined','failure erased edits or saved echo');
 const third=save();S.config.speech.mode.speak='off';
 edit('sp-echo-mode','neither');
 acceptSettingsConfig(third.request_id);renderSettings();
 assert(echoMode()==='native'&&el('sp-echo-mode').value==='neither','late ack overwrote a newer echo edit');
 const fourth=save();rejectSettingsConfig('fixture persistence refused',fourth.request_id);renderSettings();
 assert(echoMode()==='native'&&el('sp-echo-mode').value==='neither','refusal changed saved echo');
 const fifth=save();
 const original=Storage.prototype.setItem;Storage.prototype.setItem=function(){throw new Error('storage blocked');};
 acceptSettingsConfig(fifth.request_id);renderSettings();Storage.prototype.setItem=original;
 assert(document.querySelector('[data-said="speech"].bad').textContent.includes('could not save echo'),'storage failure hidden');
 assert(echoMode()==='native','failed browser write changed preference');
});</script>`
	runPageInEngines(t, page, settingsDraftModules(t))
}

func TestSpeechSavePreparesBothServicesBeforeOneConfiguration(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { acceptProviderSave, rejectProviderSave, acceptSettingsConfig, renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(()=>{
 const el=id=>document.getElementById(id);
 const action=()=>frames.filter(f=>f.type!=='query').at(-1);
 const configs=()=>frames.filter(f=>f.type==='config_set');
 const svc=(name,dir)=>({name,added:false,has_key:false,speech:{[dir]:{}}});
 S.identityExists=true;S.providersLoaded=true;S.providers=[];
 S.config={llm:{},speech:{stt:{provider:'Heard',model:'',language:'',monthly_minutes:0},tts:{provider:'Spoken',model:'',voice:'',monthly_characters:0},services:[svc('Heard','stt'),svc('Spoken','tts')]}};
 S.openSettings('speech');
 el('sp-key-stt').value='fixture-heard';el('sp-key-tts').value='fixture-spoken';
 document.querySelector('[data-save="speech"]').click();
 assert(action().type==='speech_service'&&action().provider==='Heard'&&configs().length===0,'first provider preparation missing');
 S.providers.push({name:'Heard',has_key:true});acceptProviderSave(action().request_id);
 assert(action().provider==='Spoken'&&configs().length===0,'configuration was partially sent before second service');
 rejectProviderSave('second service refused',action().request_id);
 assert(configs().length===0&&el('settings-stack').textContent.includes('second service refused'),'service refusal hidden or configuration partially saved');
 assert(el('sp-key-tts').value==='fixture-spoken','refusal erased retry credentials');
 document.querySelector('[data-save="speech"]').click();acceptProviderSave(action().request_id);
 S.providers.push({name:'Spoken',has_key:true});acceptProviderSave(action().request_id);
 assert(configs().length===1,'successful preparation did not send exactly one configuration');
 const request=configs()[0];
 assert(request.config['speech.stt.provider']==='Heard'&&request.config['speech.tts.provider']==='Spoken','lost one direction');
 for(const [key,value] of Object.entries(request.config)){
  const p=key.split('.');if(p.length===2)S.config.speech[p[1]]=structuredClone(value);else S.config.speech[p[1]][p[2]]=value;
 }
 S.config.speech.mode.speak='auto';S.config.speech.speakers.uids=[];
 acceptSettingsConfig(request.request_id);renderSettings();
 assert(document.querySelector('[data-said="speech"].good'),'combined save not confirmed');
 assert(el('sp-key-stt').value===''&&el('sp-key-tts').value==='','confirmed credentials retained in form');
});</script>`
	runPageInEngines(t, page, settingsDraftModules(t))
}
