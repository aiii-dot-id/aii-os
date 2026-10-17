//go:build !windows

package dashboard

import "testing"

const formRecoveryMarkup = `<!doctype html><div id="plugins-stack"></div><div id="settings-stack"></div>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span><div id="memory-stack"></div>`

func formRecoveryModules() map[string][]byte {
	return map[string][]byte{
		"/ws.js":  []byte(`export const frames=[];let online=true;export function connected(on){online=on;}export function wsReady(){return online;}export function send(f){if(!online)return '';f.request_id='r'+(frames.length+1);frames.push(f);return f.request_id;}export function query(q,more){return send({type:'query',query:q,...more});}`),
		"/app.js": []byte(`export function go(){};export function toast(){}`),
	}
}

const pluginFormSetup = `
S.view='plugins';S.providers=[];S.providersLoaded=true;
S.config={secrets_ok:true,llm:{},dashboard:{},prompt:{},witness:{},logs:{},agency:{},restart_required:[],plugins:{autoload:'T1',installed:[
 {id:'example.plugin',version:'1',tier:'T1',mode:'supervised',variant:'test',capabilities:['ring4.kv'],grants:{kv:false},settings:[
 {key:'limit',type:'number',title:'Limit',value:8,default:5},
 {key:'api_key',type:'secret',title:'API key',key_host:'api.example.test:443',key_kept:false}]}]}};
const st=document.getElementById('plugins-stack');
const key=()=>st.querySelector('[data-pkey-input]');
const limit=()=>st.querySelector('[data-pset-key="limit"]');
const save=()=>st.querySelector('[data-save="plugin:example.plugin"]');
const said=()=>st.querySelector('[data-said="plugin:example.plugin"]')?.textContent||'';
const type=(el,v)=>{el.value=v;el.dispatchEvent(new Event('input',{bubbles:true}));};
renderPlugins();
`

func TestPluginSaveConfirmsKeysBeforeSettingsAndKeepsUnsentEdits(t *testing.T) {
	page := formRecoveryMarkup + `<script type="module">
import {S} from '/state.js';import {frames,connected} from '/ws.js';import {renderPlugins} from '/views/plugins.js';
import {acceptSettingsConfig,rejectSettingsConfig,settingsConnectionLost} from '/views/settings.js';import {run} from '/__harness.js';
run(assert=>{` + pluginFormSetup + `
 const grant=st.querySelector('[data-grant-field="kv"]');grant.checked=true;grant.dispatchEvent(new Event('change',{bubbles:true}));
 type(limit(),'19');type(key(),'dummy-first-key');connected(false);save().click();
 assert(frames.length===0&&key().value==='dummy-first-key'&&limit().value==='19','offline Save discarded unsent edits');
 connected(true);save().click();const first=frames.at(-1);
 assert(first.type==='plugin_key_set'&&frames.length===1&&!key().value,'key was not sent once, before settings');
 assert(!acceptSettingsConfig('unrelated'),'unrelated acknowledgement advanced the save');
 assert(rejectSettingsConfig('key refused',first.request_id),'key refusal was not claimed');renderPlugins();
 assert(!frames.some(f=>f.type==='config_set')&&/Key save failed/.test(said())&&limit().value==='19','failed key claimed success, sent settings or lost edits');
 type(key(),'dummy-retry');save().click();const retry=frames.at(-1);
 acceptSettingsConfig(retry.request_id);renderPlugins();
 assert(/not confirmed/.test(said())&&!frames.some(f=>f.type==='config_set'),'missing key readback advanced Save');
 type(key(),'dummy-confirmed');save().click();const kept=frames.at(-1);
 S.config.plugins.installed[0].settings[1].key_kept=true;
 acceptSettingsConfig(kept.request_id);renderPlugins();const settings=frames.at(-1);
 assert(settings.type==='config_set'&&settings.config['plugins.settings.example.plugin.limit']===19,'confirmed key did not send captured settings');
 type(limit(),'23');type(key(),'dummy-newer-key');S.config.plugins.installed[0].settings[0].value=19;
 acceptSettingsConfig(settings.request_id);renderPlugins();
 assert(limit().value==='23'&&/Newer edits remain unsaved/.test(said()),'settings acknowledgement ate a newer edit');
 assert(st.querySelector('[data-grant-field="kv"]').checked,'saving settings cleared an unsaved grant');
 assert(key().value==='dummy-newer-key','older acknowledgement discarded a newer pasted key');
 type(key(),'dummy-uncertain');save().click();const before=frames.length;settingsConnectionLost();renderPlugins();
 assert(/Connection lost/.test(said())&&frames.length===before&&!key().value,'disconnect invented success or replayed a key');
});</script>`
	runPageInEngines(t, page, formRecoveryModules())
}

func TestPluginMultiKeyFailureDeclaresPartialCompletion(t *testing.T) {
	page := formRecoveryMarkup + `<script type="module">
import {S} from '/state.js';import {frames} from '/ws.js';import {renderPlugins} from '/views/plugins.js';
import {acceptSettingsConfig,rejectSettingsConfig} from '/views/settings.js';import {run} from '/__harness.js';
run(assert=>{` + pluginFormSetup + `
 S.config.plugins.installed[0].settings.push({key:'second_key',type:'secret',title:'Second key',key_host:'api.example.test:443',key_kept:false});renderPlugins();
 const fields=st.querySelectorAll('[data-pkey-input]');type(fields[0],'dummy-one');type(fields[1],'dummy-two');save().click();
 assert(frames.length===1,'keys were sent concurrently');
 S.config.plugins.installed[0].settings[1].key_kept=true;acceptSettingsConfig(frames[0].request_id);renderPlugins();
 assert(frames.length===2&&frames[1].plugin_key.key==='second_key','second key did not follow first acknowledgement');
 rejectSettingsConfig('second refused',frames[1].request_id);renderPlugins();
 assert(/Earlier keys were saved/.test(said())&&/remaining settings were not sent/.test(said())&&!frames.some(f=>f.type==='config_set'),'partial save was described as success or rollback');
});</script>`
	runPageInEngines(t, page, formRecoveryModules())
}

func TestPluginAndAccountDraftsSurviveReadbackAndComposition(t *testing.T) {
	page := formRecoveryMarkup + `<script type="module">
import {S} from '/state.js';import {renderPlugins} from '/views/plugins.js';import {run} from '/__harness.js';
run(assert=>{` + pluginFormSetup + `
 type(limit(),'19');limit().focus();const number=limit();renderPlugins();
 assert(limit()===number&&limit().value==='19'&&document.activeElement===number,'readback lost plugin draft/node/focus');
 st.querySelector('[data-profile-new]').click();let provider=document.getElementById('pf-provider');provider.value='custom';provider.dispatchEvent(new Event('change',{bubbles:true}));
 const name=document.getElementById('pf-name'),secret=document.getElementById('pf-secret');
 type(name,'account draft');type(document.getElementById('pf-client'),'client draft');type(secret,'dummy-profile-secret');name.focus();name.setSelectionRange(2,6);
 renderPlugins();assert(document.getElementById('pf-name')===name&&name.value==='account draft'&&document.activeElement===name&&name.selectionStart===2&&name.selectionEnd===6,'account draft/focus/selection lost');
 assert(document.getElementById('pf-client').value==='client draft'&&document.getElementById('pf-secret').value==='dummy-profile-secret'&&!st.innerHTML.includes('dummy-profile-secret'),'account secret lost or serialized into markup');
 name.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));type(name,'composing');renderPlugins();assert(name.isConnected&&document.activeElement===name,'composition was interrupted');name.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));
 assert(document.getElementById('pf-name').value==='composing','composition commit lost');
 st.querySelector('[data-profile-cancel]').click();st.querySelector('[data-profile-new]').click();
 assert(!document.getElementById('pf-name').value&&!document.getElementById('pf-secret').value,'Cancel resurrected old account edits');
 assert(limit().value==='19','account Cancel discarded a different card');
});</script>`
	runPageInEngines(t, page, formRecoveryModules())
}

func TestNewProjectDraftSurvivesReadbackFailureAndLateAck(t *testing.T) {
	page := formRecoveryMarkup + `<script type="module">
import {S} from '/state.js';import {frames,connected} from '/ws.js';import {renderProjects,rejectCreate,acceptCreate,projectsConnectionLost} from '/views/projects.js';import {run} from '/__harness.js';
run(assert=>{
 S.view='projects';S.identityExists=true;S.stats={name:'Fixture'};S.projects=[];S.viewedProject='';renderProjects();
 const type=(id,v)=>{const e=document.getElementById(id);e.value=v;e.dispatchEvent(new Event('input',{bubbles:true}));};
 type('np-name','New work');type('np-desc','Keep this description');const name=document.getElementById('np-name');name.focus();name.setSelectionRange(1,3);renderProjects();
 assert(document.getElementById('np-name')===name&&name.value==='New work'&&name.selectionStart===1,'focused creation draft lost');
 name.blur();renderProjects();assert(document.getElementById('np-desc').value==='Keep this description','blurred creation draft lost');
 connected(false);document.getElementById('np-create').click();renderProjects();assert(!document.getElementById('np-create').disabled&&document.getElementById('np-name').value==='New work','offline create lost draft or wedged');
 connected(true);document.getElementById('np-create').click();const request=frames.at(-1);renderProjects();assert(document.getElementById('np-create').disabled,'redraw re-enabled pending create');
 rejectCreate(request.request_id);assert(!document.getElementById('np-create').disabled&&document.getElementById('np-desc').value==='Keep this description','refusal lost creation draft');
 document.getElementById('np-create').click();const second=frames.at(-1);type('np-name','Newer draft');acceptCreate(second.request_id,[]);renderProjects();assert(document.getElementById('np-name').value==='Newer draft','old acknowledgement erased newer draft');
 projectsConnectionLost();renderProjects();assert(document.getElementById('np-name').value==='Newer draft','disconnect erased creation draft');
});</script>`
	runPageInEngines(t, page, formRecoveryModules())
}

func TestMemorySearchKeepsFocusAndDoesNotSubmitIMEConfirmation(t *testing.T) {
	page := formRecoveryMarkup + `<script type="module">
import {S} from '/state.js';import {frames} from '/ws.js';import {renderMemory} from '/views/memory.js';import {run} from '/__harness.js';
run(assert=>{
 S.identity={beliefs:[],intentions:[],experiences:[]};renderMemory();const input=document.getElementById('mem-search');input.value='search words';input.dispatchEvent(new Event('input',{bubbles:true}));input.focus();input.setSelectionRange(2,5,'backward');
 S.recall={query:'prior',text:'result arrived'};renderMemory();assert(document.getElementById('mem-search')===input&&document.activeElement===input&&input.selectionStart===2&&input.selectionEnd===5&&input.selectionDirection==='backward','recall reply lost search node/focus/selection');
 input.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));input.value='composition';input.dispatchEvent(new Event('input',{bubbles:true}));renderMemory();input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',isComposing:true,bubbles:true}));input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',keyCode:229,bubbles:true}));
 assert(frames.length===0&&input.isConnected&&document.activeElement===input,'composition was submitted or disrupted');
 input.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));assert(frames.length===1&&frames[0].query==='recall'&&frames[0].q==='composition','ordinary Enter did not recall committed text');
});</script>`
	runPageInEngines(t, page, formRecoveryModules())
}
