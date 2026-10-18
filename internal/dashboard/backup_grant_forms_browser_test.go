//go:build !windows

package dashboard

import (
	"strconv"
	"strings"
	"testing"
)

func TestBackupFormReadbackPreservesEdits(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames, setConnected } from './ws.js';
import { renderSettings } from './views/settings.js';
import * as backup from './views/backups.js';
import { assert, run } from './__harness.js';
run(() => {
  const el = id => document.getElementById(id);
  S.view='settings'; S.identityExists=true; S.providersLoaded=true; S.config={llm:{}};
  const view = {enabled:true,backup_keep:8,secrets_ok:true,export_ok:true,snapshots:[]};
  backup.applyBackups({action:'status',view});
  renderSettings(); document.querySelector('[data-sec="backups"]').click();
  el('bk-keep').value='14'; el('bk-enabled').checked=false;
  el('bk-pass').value='fixture secret phrase'; el('bk-again').value='fixture secret phrase';
  el('bk-pass').focus(); el('bk-pass').setSelectionRange(2,5);
  backup.applyBackups({action:'status',view:{...view,chain:'fresh readback'}});
  assert(el('bk-keep').value==='14' && !el('bk-enabled').checked,'backup readback discarded preferences');
  assert(el('bk-pass').value==='fixture secret phrase' && el('bk-again').value==='fixture secret phrase','backup readback discarded passphrases');
  assert(document.activeElement===el('bk-pass') && el('bk-pass').selectionStart===2,'backup readback lost focus/caret');
  assert(!el('settings-stack').innerHTML.includes('fixture secret phrase'),'secret in markup');
  assert(el('settings-stack').textContent.includes('fresh readback'),'editor froze live status');
  el('bk-save').click(); const save=frames.filter(f=>f.type==='config_set').at(-1);
  assert(save.config['maintenance.backup_keep']===14 && save.config['maintenance.enabled']===false,'wrong preferences submitted');
  el('bk-keep').value='21';
  assert(!backup.acceptBackupPreferences('other-client'),'foreign reply consumed save');
  S.config.maintenance={enabled:false,backup_keep:14};
  assert(backup.acceptBackupPreferences(save.request_id),'matching save unclaimed');
  assert(el('bk-keep').value==='21','save A ate newer edit B');
  el('bk-save').click(); const next=frames.filter(f=>f.type==='config_set').at(-1);
  S.config.maintenance.backup_keep=21; backup.acceptBackupPreferences(next.request_id);
  backup.applyBackups({action:'status',view:{...view,backup_keep:30}});
  assert(el('bk-keep').value==='30','acknowledged draft hid later preferences');
  el('bk-keep').value='33'; el('bk-save').click();
  backup.rejectBackups(frames.at(-1).request_id,'fixture persistence refusal');
  assert(el('bk-keep').value==='33' && el('settings-stack').textContent.includes('fixture persistence refusal'),'refusal lost draft/result');
  el('bk-save').click(); backup.acceptBackupPreferences(frames.at(-1).request_id);
  assert(el('bk-keep').value==='33' && el('settings-stack').textContent.includes('could not be verified'),'mismatched readback claimed save');
  setConnected(false); const before=frames.length; el('bk-save').click();
  assert(frames.length===before && !el('bk-save').disabled && el('bk-keep').value==='33','failed send left pending or lost draft');
  setConnected(true); el('bk-save').click(); const lost=frames.at(-1);
  backup.backupsConnectionLost();
  assert(!backup.acceptBackupPreferences(lost.request_id),'late lost-socket reply accepted');
  assert(el('bk-keep').value==='33' && el('bk-pass').value==='' && el('bk-again').value==='','disconnect lost preferences or kept secrets');
  assert(el('settings-stack').textContent.includes('completion is unknown'),'disconnect hid uncertainty');
  const after=frames.length; backup.backupsReconnected();
  assert(frames.length===after+1 && frames.at(-1).backups.action==='status','reconnect must read status, never replay preferences');
  el('bk-pass').value='navigation secret';
  document.querySelector('[data-sec="sandbox"]').click(); document.querySelector('[data-sec="backups"]').click();
  assert(el('bk-pass').value==='' && el('bk-keep').value==='33','navigation retained secret or discarded preferences');
});
</script>`
	runPageInEngines(t, page, settingsDraftModules(t))
}

func TestSandboxFormReadbackPreservesEdits(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { frames, setConnected } from './ws.js';
import * as sandbox from './sandbox.js';
import { assert, run } from './__harness.js';
run(() => {
  S.view='settings'; S.identityExists=true; S.providersLoaded=true; S.config={llm:{}};
  S.sandbox={root:'/identity',extra_roots:['/existing']};
  renderSettings(); document.querySelector('[data-sec="sandbox"]').click();
  const input=document.getElementById('sbx-add'); input.value='/my/project'; input.focus(); input.setSelectionRange(3,7);
  S.sandbox={root:'/identity',extra_roots:['/existing','/another-client']}; renderSettings();
  assert(document.getElementById('sbx-add').value==='/my/project','sandbox readback discarded root draft');
  assert(document.activeElement===input && input.selectionStart===3,'sandbox readback lost focus/caret');
  assert(document.getElementById('settings-stack').textContent.includes('/another-client'),'draft hid current grants');
  const el=id=>document.getElementById(id);
  el('sbx-grant').click(); const added=frames.at(-1);
  assert(added.roots.join('|')==='/existing|/another-client|/my/project','grant omitted current roots');
  assert(el('sbx-grant').disabled,'pending grant not disabled');
  input.value='/newer/project';
  assert(!sandbox.acceptSandbox('another-client'),'foreign grant reply accepted');
  S.sandbox.extra_roots=added.roots; assert(sandbox.acceptSandbox(added.request_id),'grant reply not claimed'); renderSettings();
  assert(input.value==='/newer/project','older grant erased newer input');
  el('sbx-grant').click(); const newer=frames.at(-1); S.sandbox.extra_roots=newer.roots;
  sandbox.acceptSandbox(newer.request_id); renderSettings();
  assert(input.value==='','confirmed grant input not cleared');
  input.value='/refused'; el('sbx-grant').click(); sandbox.rejectSandbox(frames.at(-1).request_id,'fixture root refusal');
  assert(input.value==='/refused' && el('settings-stack').textContent.includes('fixture root refusal'),'refusal lost input/reason');
  el('sbx-grant').click(); const lost=frames.at(-1); sandbox.sandboxConnectionLost();
  assert(!sandbox.acceptSandbox(lost.request_id) && input.value==='/refused','late reply erased lost-socket draft');
  assert(el('settings-stack').textContent.includes('completion is unknown'),'lost grant hid uncertainty');
  setConnected(false); const before=frames.length; el('sbx-grant').click();
  assert(frames.length===before && !el('sbx-grant').disabled && input.value==='/refused','offline grant stuck or lost draft');
  setConnected(true); document.querySelector('[data-rmroot]').click(); const removed=frames.at(-1);
  assert(removed.roots.length===newer.roots.length-1,'remove did not submit current list');
  sandbox.acceptSandbox(removed.request_id); renderSettings();
  assert(input.value==='/refused','removal consumed unrelated add draft');
});
</script>`
	modules := settingsDraftModules(t)
	data, err := staticFS.ReadFile("static/sandbox.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/sandbox.js"] = data
	runPageInEngines(t, page, modules)
}

func TestBackupActionsRetireSecretsAndIgnoreAbandonedReplies(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames, setConnected } from './ws.js';
import { renderSettings } from './views/settings.js';
import * as backup from './views/backups.js';
import { assert, run } from './__harness.js';
run(() => {
 const el=id=>document.getElementById(id), last=()=>frames.at(-1);
 const nav=name=>document.querySelector('[data-sec="'+name+'"]').click();
 S.view='settings'; S.identityExists=true; S.providersLoaded=true; S.config={llm:{}};
 const view={enabled:true,backup_keep:8,secrets_ok:true,export_ok:true,snapshots:[{name:'one',record:3},{name:'two',record:5}]};
 backup.applyBackups({action:'status',view}); renderSettings(); nav('backups');
 let downloads=0; HTMLAnchorElement.prototype.click=function(){downloads++;};
 let writes=0; Storage.prototype.setItem=function(){writes++;};
 const create=()=>{el('bk-pass').value='fixture escrow phrase';el('bk-again').value='fixture escrow phrase';el('bk-create').click();return last();};
 const made=create();
 backup.applyBackups({action:'escrow_create',file_b64:btoa('sealed')},'foreign');
 assert(downloads===0 && backup.backupsHolding().busy==='escrow_create','foreign reply ended create');
 nav('sandbox'); nav('backups');
 backup.applyBackups({action:'escrow_create',file_b64:btoa('sealed')},made.request_id);
 assert(downloads===0 && !backup.backupsHolding().holding && !el('bk-pass').value,'abandoned reply revived escrow');
 setConnected(false);create();
 assert(!backup.backupsHolding().busy && !backup.backupsHolding().holding && el('settings-stack').textContent.includes('Not connected'),'offline create stuck');
 setConnected(true);const fresh=create();
 backup.applyBackups({action:'escrow_create',file_b64:btoa('sealed'),file_name:'fixture.age'},fresh.request_id);
 assert(downloads===1 && backup.backupsHolding().holding && el('bk-file2'),'valid create did not advance');
 const pick=()=>{const dt=new DataTransfer();dt.items.add(new File(['sealed'],'fixture.age'));el('bk-file2').files=dt.files;};
 pick(); const file=el('bk-file2');backup.applyBackups({action:'status',view});
 assert(el('bk-file2')===file && file.files[0].name==='fixture.age','status discarded picked file');
 const reads=[];window.FileReader=class {readAsArrayBuffer(){reads.push(this);}abort(){this.aborted=true;}};
 setConnected(false);el('bk-check2').click();const unsent=reads.pop();unsent.result=new Uint8Array([1]).buffer;unsent.onload();
 assert(backup.backupsHolding().holding && backup.backupsHolding().step===2 && !el('bk-check2').disabled,'failed check send stranded step two');
 setConnected(true);
 el('bk-check2').click(); const reading=reads[0], onload=reading.onload;
 assert(reads.length===1,'file not read');el('bk-cancel2').click();const before=frames.length;
 reading.result=new Uint8Array([1,2]).buffer;onload();
 assert(reading.aborted && frames.length===before && !backup.backupsHolding().holding,'cancelled file callback sent secret');
 const again=create();backup.applyBackups({action:'escrow_create',file_b64:btoa('sealed')},again.request_id);
 pick();el('bk-check2').click();const r=reads[1];r.result=new Uint8Array([3]).buffer;r.onload();const check=last();
 el('bk-cancel2').click();
 backup.applyBackups({action:'escrow_check',view:{...view,encrypting:true}},check.request_id);
 assert(!backup.backupsHolding().holding && !el('settings-stack').textContent.includes('Checked:'),'late check revived cancelled step');
 const escrow=create();backup.applyBackups({action:'escrow_create',file_b64:btoa('sealed')},escrow.request_id);
 el('bk-take').click();const taking=last();el('bk-cancel2').click();
 assert(backup.backupsHolding().busy==='take','escrow cancel retired a snapshot request');
 backup.applyBackups({action:'take',view},taking.request_id);
 const choose=name=>{document.querySelector('[data-restore="'+name+'"]').click();return last();};
 const plan=(name)=>({source:'snapshot',name,restored_to:3,live_through:5,set_aside:2,witnessed:-1,confirm_text:'Fixture',aside_under:'/aside'});
 const chosen=choose('one');backup.applyBackups({action:'restore_plan',plan:plan('one')},chosen.request_id);
 el('bk-take').click();const takingWithPlan=last();el('bk-noplan').click();
 assert(backup.backupsHolding().busy==='take','plan cancel retired a snapshot request');
 backup.applyBackups({action:'take',view},takingWithPlan.request_id);
 const chosenAgain=choose('one');backup.applyBackups({action:'restore_plan',plan:plan('one')},chosenAgain.request_id);
 el('bk-confirm').value='Fixture';el('bk-confirm').dispatchEvent(new Event('input'));
 backup.applyBackups({action:'status',view});
 assert(el('bk-confirm').value==='Fixture' && !el('bk-restore').disabled,'same-plan readback lost confirmation');
 const other=choose('two');backup.applyBackups({action:'restore_plan',plan:plan('two')},other.request_id);
 assert(!el('bk-confirm').value && el('bk-restore').disabled,'confirmation crossed plan boundary');
 el('bk-confirm').value='Fixture';el('bk-confirm').dispatchEvent(new Event('input'));el('bk-restore').click();const restoring=last();
 assert(el('bk-restore').disabled && el('bk-noplan').disabled,'restore can be repeated or falsely cancelled while pending');
 backup.backupsConnectionLost();const count=frames.filter(f=>f.backups?.action==='restore').length;
 backup.applyBackups({action:'restore',restarting:true},restoring.request_id);backup.backupsReconnected();
 assert(!document.querySelector('[data-restarting]') && !el('bk-confirm'),'late restore revived plan');
 assert(el('settings-stack').textContent.includes('completion is unknown'),'lost restore hid uncertainty');
 assert(frames.filter(f=>f.backups?.action==='restore').length===count,'restore replayed at reconnect');
 assert(writes===0 && !el('settings-stack').innerHTML.includes('fixture escrow phrase'),'secret persisted or serialized');
});
</script>`
	runPageInEngines(t, page, settingsDraftModules(t))
}

func TestBackupAndSandboxFormsUseSocketRepliesAndDisconnect(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
 const sockets=[];
 window.setTimeout=()=>1;window.clearTimeout=()=>{};
 window.WebSocket=class {
   constructor(){this.readyState=0;this.frames=[];sockets.push(this);}
   send(bytes){this.frames.push(JSON.parse(bytes));}
   recv(frame){this.onmessage({data:JSON.stringify(frame)});}
 };
 S.view='settings';S.identityExists=true;S.providersLoaded=true;S.config={llm:{}};
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 const el=id=>document.getElementById(id), nav=name=>document.querySelector('[data-sec="'+name+'"]').click();
 const last=()=>socket.frames.at(-1);
 renderSettings();nav('backups');
 const view={enabled:true,backup_keep:8,secrets_ok:true,export_ok:true,snapshots:[]};
 socket.recv({type:'backups',backups:{action:'status',view}});
 el('bk-keep').value='20';el('bk-save').click();const save=last();
 socket.recv({type:'config',request_id:'foreign',config:{llm:{},maintenance:{enabled:true,backup_keep:20}}});
 assert(el('bk-save').disabled,'unrelated config cleared pending preferences');
 socket.recv({type:'config',request_id:save.request_id,config:{llm:{},maintenance:{enabled:true,backup_keep:20}}});
 assert(!el('bk-save').disabled && el('settings-stack').textContent.includes('Preferences saved'),'socket config reply not routed');
 el('bk-take').click();const take=last();
 socket.recv({type:'backups',request_id:'foreign',backups:{action:'take',view}});
 assert(el('bk-take').disabled,'foreign backup reply consumed action');
 socket.recv({type:'error',request_id:take.request_id,message:'fixture owner refused'});
 assert(!el('bk-take').disabled && el('settings-stack').textContent.includes('fixture owner refused'),'socket action error not routed');
 el('bk-take').click();const taken=last();
 socket.recv({type:'backups',request_id:taken.request_id,backups:{action:'take',view}});
 assert(!el('bk-take').disabled && el('settings-stack').textContent.includes('taken and proved'),'socket action receipt not routed');
 el('bk-keep').value='25';el('bk-save').click();const prefs=last();
 socket.recv({type:'error',request_id:prefs.request_id,message:'fixture config refused'});
 assert(!el('bk-save').disabled && el('bk-keep').value==='25','socket preference refusal not routed');
 nav('sandbox');socket.recv({type:'sandbox',sandbox:{root:'/home',extra_roots:[]}});
 el('sbx-add').value='/project';el('sbx-grant').click();const grant=last();
 socket.recv({type:'sandbox',request_id:'foreign',sandbox:{root:'/home',extra_roots:['/foreign']}});
 assert(el('sbx-grant').disabled && el('sbx-add').value==='/project','foreign sandbox reply consumed grant');
 socket.recv({type:'sandbox',request_id:grant.request_id,sandbox:{root:'/home',extra_roots:['/project']}});
 assert(!el('sbx-grant').disabled && el('sbx-add').value==='','socket sandbox receipt not routed');
 el('sbx-add').value='/refused';el('sbx-grant').click();const refuse=last();
 socket.recv({type:'error',request_id:refuse.request_id,message:'fixture grant refused'});
 assert(!el('sbx-grant').disabled && el('settings-stack').textContent.includes('fixture grant refused'),'sandbox error not routed');
 el('sbx-grant').click();nav('backups');el('bk-save').click();el('bk-pass').value='disconnect secret';
 const before=socket.frames.length;socket.readyState=3;socket.onclose();
 assert(el('bk-pass').value==='' && !el('bk-save').disabled && el('bk-keep').value==='25','socket loss did not retire backups');
 nav('sandbox');assert(!el('sbx-grant').disabled && el('sbx-add').value==='/refused','socket loss did not retire grant');
 assert(socket.frames.length===before,'socket loss replayed action');
});
</script>`
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(ws, []byte(`export const S={providers:[]};`))
	for _, file := range []string{"views/settings.js", "util.js"} {
		data, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+file] = data
	}
	runPageInEngines(t, page, modules)
}

func TestBackupFormsKeepClientLocalDrafts(t *testing.T) {
	child := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import * as backup from './views/backups.js';
S.view='settings';S.identityExists=true;S.providersLoaded=true;S.config={llm:{}};
backup.applyBackups({action:'status',view:{enabled:true,backup_keep:8,secrets_ok:true,export_ok:true,snapshots:[]}});
renderSettings();document.querySelector('[data-sec="backups"]').click();
window.client={read:keep=>backup.applyBackups({action:'status',view:{...S.backups,backup_keep:keep}})};
</script>`
	page := `<!doctype html><script type="module">
import { assert, run } from './__harness.js';
run(async()=>{
 const open=()=>new Promise(resolve=>{const f=document.createElement('iframe');f.onload=()=>resolve(f);f.srcdoc=` + strings.ReplaceAll(strconv.Quote(child), "</script>", "<\\/script>") + `;document.body.appendChild(f);});
 const a=await open(), b=await open();
 assert(a.contentWindow.client && b.contentWindow.client,'client fixtures not ready');
 const input=f=>f.contentDocument.getElementById('bk-keep');
 input(a).value='14';input(b).value='21';
 a.contentWindow.client.read(30);b.contentWindow.client.read(30);
 assert(input(a).value==='14' && input(b).value==='21','shared readback consumed independent client drafts');
 a.contentDocument.getElementById('bk-pass').value='only client A knows';
 a.contentWindow.client.read(31);b.contentWindow.client.read(31);
 assert(a.contentDocument.getElementById('bk-pass').value==='only client A knows' && b.contentDocument.getElementById('bk-pass').value==='','passphrase crossed clients');
});
</script>`
	runPageInEngines(t, page, settingsDraftModules(t))
}

func TestLeavingSettingsClearsBackupSecretsThroughRouter(t *testing.T) {
	page := substratePageMarkup + `<div id="crumb"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { S } from './state.js';
import { go } from './app.js';
import { openBackups } from './views/settings.js';
import { applyBackups, backupsHolding } from './views/backups.js';
import { assert, run } from './__harness.js';
run(()=>{
 S.connected=true;S.identityExists=true;S.providersLoaded=true;S.config={llm:{}};
 applyBackups({action:'status',view:{enabled:true,backup_keep:8,secrets_ok:true,export_ok:true,snapshots:[]}});
 openBackups();go('settings');
 const el=id=>document.getElementById(id);
 el('bk-pass').value='router secret';el('bk-again').value='router secret';el('bk-keep').value='19';
 const old=el('bk-pass');go('chat');
 assert(old.value==='' && !backupsHolding().holding,'router retained secret in hidden Settings DOM');
 go('settings');assert(el('bk-pass').value==='' && el('bk-keep').value==='19','return revived secret or lost preferences');
});
</script>`
	modules := wsPageModules(nil, []byte(`export const S={providers:[]};`))
	for _, file := range []string{"app.js", "views/settings.js", "util.js"} {
		data, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+file] = data
	}
	modules["/ws.js"] = []byte(`export function send(){return 'fixture';}export function query(){}export function connect(){}`)
	modules["/sections.js"] = append(modules["/sections.js"], []byte(`export function initSections(){}export function sectionTitle(v){return v;}`)...)
	modules["/voice.js"] = append(modules["/voice.js"], []byte(`export function wireMic(){}`)...)
	modules["/overlay.js"] = append(modules["/overlay.js"], []byte(`export function restoreDraft(){}`)...)
	modules["/views/chat.js"] = append(modules["/views/chat.js"], []byte(`export function scrollThread(){}`)...)
	runPageInEngines(t, page, modules)
}
