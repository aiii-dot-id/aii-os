//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestInteractionViewDirtyReadbackAndReconnect(t *testing.T) {
	page := `<!doctype html><script type="module">
import { S } from './state.js';
import { sent } from './ws.js';
import './interaction-view.js';
import { assert, run } from './__harness.js';
run(() => {
 let hidden=false; Object.defineProperty(document,'visibilityState',{configurable:true,get:()=>hidden?'hidden':'visible'});
 const reply=(req,text)=>S.interactionPage({version:1,source:req.interaction_query.source,cursor:req.interaction_query.cursor,incarnation:'host',fingerprint:text,rows:[{id:text,sequence:'1'}]},req.request_id,{fingerprint:text,has_older:false,has_newer:false});
 S.interactionChanged(); assert(sent.length===1,'one initial request');
 const old=sent[0]; S.interactionChanged();S.interactionChanged();assert(sent.length===1,'invalidations must coalesce behind read');
 reply(old,'before');assert(sent.length===2,'dirty invalidation lost during read');
 reply(sent[1],'transient');assert(sent.length===3,'recorded change was not reread');
 reply(sent[2],'after');assert(S.pages.get('recorded').fingerprint==='after','stale snapshot won');
 hidden=true; S.interactionChanged();assert(sent.length===3,'hidden page polled history');
 hidden=false;S.interactionWake();assert(sent.length===4,'wake did not refresh');
 const retired=sent[3];S.interactionLost();S.interactionChanged();const current=sent.at(-1);
 reply(retired,'retired');assert(S.pages.get('recorded').fingerprint==='after','retired response accepted');
 reply(current,'current');reply(sent.at(-1),'current-transient');
 assert(S.pages.get('recorded').fingerprint==='current','reconnect failed to replace facts');
});</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/state.js":      []byte(`export const S={};`),
		"/ws.js":         []byte(`export const sent=[];export function wsReady(){return true;}export function send(x){sent.push(x);return x.request_id;}`),
		"/views/chat.js": []byte(`import {S} from '../state.js';export function renderInteractionPages(pages){S.pages=pages;}`),
	})
}

func TestInteractionRendererDoesNotInventRows(t *testing.T) {
	page := `<!doctype html><div id="thread"><div id="thread-inner"></div></div><textarea id="msg-input"></textarea><button id="send-btn"></button>
<script type="module">
import { S } from './state.js';import { renderInteractionPages, sendChat, toolEventLive } from './views/chat.js';
import { assert,run } from './__harness.js';
run(()=>{
 const inner=document.getElementById('thread-inner');
 const page={rows:[{id:'op',sequence:'1',kind:'message',role:'operator',content:'same text',details:{}},{id:'ai',sequence:'2',kind:'message',role:'resident',content:'same text',details:{}}]};
 const pages=new Map([['recorded',page],['transient',{rows:[]}]]);
 renderInteractionPages(pages,()=>{},false);
 assert(inner.querySelectorAll('[data-turn-id]').length===2,'equal text occurrences were deduplicated');
 assert(sendChat('unacknowledged input'),'local send failed');
 assert(inner.querySelectorAll('[data-turn-id]').length===2,'local input invented history');
 toolEventLive('read','live arguments');assert(inner.querySelectorAll('[data-turn-id]').length===2,'live response invented history');
 S.chatConnectionLost();assert(document.getElementById('chat-pending').textContent.includes('unconfirmed'),'loss concealed pending input');
 S.chatAcknowledged('local-1','accepted');assert(document.querySelector('[data-request-id="local-1"]'),'lost-socket ack retired an unconfirmed message');
 assert(sendChat('a new submission'),'second send failed');S.chatAcknowledged('local-2','accepted');assert(document.querySelector('[data-request-id="local-2"]'),'admission pretended to be recording');
 S.chatReceipt('local-2',{ref:{identity:'fixture',source:'recorded',incarnation:'host',id:'actual'}});assert(!document.querySelector('[data-request-id="local-2"]'),'real recording did not retire pending notice');
 const beforeActivity=inner.children.length;
 Object.defineProperty(document,'visibilityState',{configurable:true,value:'hidden'});
 toolEventLive('read','hidden arguments');renderInteractionPages(pages,()=>{},false);assert(inner.children.length===beforeActivity,'hidden activity allocated live DOM');
 Object.defineProperty(document,'visibilityState',{configurable:true,value:'visible'});
 renderInteractionPages(pages,()=>{},false);assert(inner.querySelectorAll('[data-turn-id]').length===2,'refresh duplicated records');
});</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/app.js":                []byte(`export function toast() {}`),
		"/ws.js":                 []byte(`let n=0;export function wsReady(){return true;}export function send(){return 'local-'+(++n);}export function query(){}`),
		"/presence.js":           []byte(`import {S} from './state.js';export function setThinking(on){S.thinking=on;}export function toolPulse(){}`),
		"/views/model-picker.js": []byte(`export function fillModelPicker(){}`),
	})
}

func TestInteractionProjectionJoinsOnlyRecordedExecutionRelationships(t *testing.T) {
	page := `<!doctype html><script type="module">
import {displayInteractions} from '/interaction-display.js';
import {run} from '/__harness.js';
run(assert=>{
 const call=(id,seq)=>({id,sequence:String(seq),kind:'tool_call',role:'system',turn_id:'turn',source:{kind:'tool',id},details:{tool:'read'},content:'same arguments'});
 const result=(id,parent,seq,outcome='succeeded')=>({id,related_id:parent,sequence:String(seq),kind:'tool_result',role:'system',turn_id:'turn',source:{kind:'tool',id:parent},outcome,details:{tool:'read'},content:'same output'});
 const message={id:'message',sequence:'3',kind:'message',role:'resident',content:'still working',details:{}};
 const pages=(recorded,transient=[])=>new Map([['recorded',{identity:'identity',incarnation:'disk',rows:recorded}],['transient',{identity:'identity',incarnation:'process',rows:transient}]]);
 let p=pages([call('a',1),call('b',2),message,result('ar','a',4),result('br','b',5)]);
 let rows=displayInteractions(p).get('recorded');
 assert(rows.length===3 && rows[0].records.map(e=>e.row.id).join()==='a,ar' && rows[1].records.map(e=>e.row.id).join()==='b,br','equal content merged distinct executions or separate results survived');
 assert(rows[2].anchor.row.id==='message','interleaved speech lost its recorded position');
 assert(rows.flatMap(g=>g.records).length===5,'projection lost an occurrence');
 assert(!displayInteractions(pages([call('a',1)])).get('recorded')[0].outcome,'missing result invented a running/terminal outcome');
 let partial=displayInteractions(pages([result('ar','a',4)])).get('recorded')[0];
 assert(partial.missingStart && partial.outcome==='succeeded','result-only page was hidden or guessed');
 const duplicate=result('ar','a',4);p=pages([call('a',1),duplicate,duplicate]);
 assert(displayInteractions(p).get('recorded')[0].records.length===2,'duplicate delivery added a fact');
 const safe={...result('safe','a',1),source:undefined,details:{tool:'read',origin:'safe_tool'}};
 p=pages([call('a',1)],[safe]);rows=displayInteractions(p);
 assert(rows.get('recorded').length===1 && rows.get('transient').length===0 && rows.get('recorded')[0].transient,'explicit durable-to-SAFE link was not qualified/grouped');
 p.get('transient').stale=true;rows=displayInteractions(p);
 assert(rows.get('recorded')[0].records.length===1 && rows.get('transient').length===1,'stale source was joined');
 p.get('transient').stale=false;p.get('transient').identity='different';rows=displayInteractions(p);
 assert(rows.get('recorded')[0].records.length===1 && rows.get('transient').length===1,'different identities were joined');
 rows=displayInteractions(pages([call('a',1),result('r1','a',2),result('r2','a',3,'failed')])).get('recorded');
 assert(rows[0].conflict && rows[0].outcome==='conflict' && rows[0].records.length===3,'conflict silently chose success');
 assert(displayInteractions(pages([result('r','a',1,'')])).get('recorded')[0].outcome==='unknown','result with missing outcome became an attempt');
});</script>`
	runPageInEngines(t, page, nil)
}

const quietChatMarkup = `<!doctype html><link rel="stylesheet" href="/layout.css">
<div id="thread" style="height:250px;width:380px;overflow:auto"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button><button id="mic">Mic</button>
<div id="effort"><button id="effort-btn"><b id="effort-val"></b></button><div id="effort-menu" hidden></div></div>
</div><button id="jump-latest" hidden>Latest</button>`

func TestInteractionQuietViewPreservesFactsControlsAndDOM(t *testing.T) {
	page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';import {renderInteractionPages,renderComposer} from '/views/chat.js';import {frames} from '/ws.js';import {run} from '/__harness.js';
run(async assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;S.config={llm:{effort_choice:{levels:['low','high'],in_force:'high'}}};renderComposer();
 const call={id:'call',sequence:'1',kind:'tool_call',role:'system',content:'read arguments',details:{tool:'read'}};
 const msg={id:'message',sequence:'2',kind:'message',role:'resident',content:'The answer',details:{}};
 const result={id:'result',related_id:'call',sequence:'3',kind:'tool_result',role:'system',outcome:'succeeded',content:'retained output',details:{tool:'read',truncated:true}};
 const pages=new Map([['recorded',{identity:'fixture',incarnation:'disk',rows:[call,msg]}],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]);
 renderInteractionPages(pages,()=>{},false);
 const tool=document.querySelector('.tool-ev'), summary=tool.querySelector('summary'), mic=document.getElementById('mic'), effort=document.getElementById('effort-btn'), input=document.getElementById('msg-input');
 assert(!tool.open && document.querySelectorAll('.tool-ev').length===1,'Tool summary is not compact');
 tool.open=true;summary.focus();input.value='draft';
 const body=tool.querySelector('.record-content'), node=body.firstChild, selection=getSelection(), range=document.createRange();range.selectNodeContents(node);selection.removeAllRanges();selection.addRange(range);
 pages.get('recorded').rows.push(result);renderInteractionPages(pages,()=>{},false);
 assert(document.querySelector('.tool-ev')===tool && tool.open,'result remounted or closed the disclosure');
 assert(document.activeElement===summary && selection.toString()==='read arguments','result lost focus/selection');
 assert(document.querySelectorAll('.tool-ev').length===1 && tool.querySelector('.tool-outcome').textContent==='Done','attempt/result still render as a pair');
 assert(JSON.parse(tool.parentElement.dataset.records).map(r=>r.id).join()==='call,result','inspection lost the raw occurrences');
 assert(tool.textContent.includes('retained output') && tool.textContent.includes('"truncated": true'),'retained detail or truncation declaration lost');
 const count=frames.length;let mutations=0;const observer=new MutationObserver(list=>mutations+=list.length);observer.observe(document.getElementById('thread-inner'),{subtree:true,childList:true,attributes:true,characterData:true});
 renderInteractionPages(pages,()=>{},false);await Promise.resolve();observer.disconnect();
 assert(mutations===0,'identical snapshot did needless DOM work: '+mutations);
 summary.click();assert(!tool.open,'Tool summary did not collapse the detail');
 summary.click();assert(tool.open,'Tool summary did not open the detail');
 assert(mic===document.getElementById('mic') && effort===document.getElementById('effort-btn') && input.value==='draft','disclosure remounted composer controls or lost draft');
 assert(document.getElementById('effort-val').textContent==='High' && frames.length===count,'disclosure changed effort or sent a command');
 assert(!document.getElementById('interaction-activity'),'parallel activity stream survived');
 const safe={id:'safe-result',related_id:'call',kind:'tool_result',outcome:'failed',details:{tool:'read',origin:'safe_tool'}};
 pages.get('recorded').rows=[call,msg];pages.get('transient').rows=[safe];renderInteractionPages(pages,()=>{},false);
 assert(document.querySelector('.record-qualification').textContent.includes('not saved') && document.querySelector('.tool-outcome').textContent==='Failed','collapsed view hid unsaved failure');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionReceiptReconciliationRefusesAmbiguousConfirmations(t *testing.T) {
	page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';import {sendChat,renderInteractionPages} from '/views/chat.js';import {frames} from '/ws.js';import {run} from '/__harness.js';
run(assert=>{
 renderInteractionPages(new Map([['recorded',{identity:'fixture',incarnation:'disk',rows:[]}],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]),()=>{},false);
 const send=()=>{assert(sendChat('same words'),'send refused');return 'req-'+frames.length;};
 const pending=id=>document.querySelector('[data-request-id="'+id+'"]');
 const ref=(source='recorded')=>({identity:'fixture',source,incarnation:source==='recorded'?'disk':'memory',id:'occurrence'});
 let id=send();S.chatAcknowledged(id,'accepted');assert(pending(id),'admission erased pending text');S.chatReceipt(id,{ref:ref()});assert(!pending(id),'durable receipt did not retire input');assert(!document.getElementById('chat-notice'),'successful recording displayed an attention banner');S.chatAcknowledged(id,'accepted');assert(!pending(id),'late ack revived input');
 id=send();S.chatReceipt(id,{ref:ref('transient')});assert(!pending(id) && document.getElementById('chat-notices').textContent.includes('Not saved'),'SAFE claimed durability');
 id=send();const failed={ref:ref('transient'),error:{code:'CHAT_RECORDING_FAILED',message:'append failed'}};S.chatReceipt(id,failed);const el=pending(id);assert(el && el.textContent.includes('transient copy, not saved') && el.textContent.includes('same words'),'failed recording lost its text/ref');
 const before=el.textContent;S.chatAcknowledged(id,'accepted');S.chatReceipt(id,{error:{message:'append failed',code:'CHAT_RECORDING_FAILED'},ref:{id:'occurrence',incarnation:'memory',source:'transient',identity:'fixture'}});assert(el.textContent===before,'late ack or reordered duplicate changed the terminal receipt');
 S.chatReceipt(id,{ref:ref()});assert(el.textContent.includes('Conflicting'),'contradictory confirmation chose success');
 const malformed=[{}, {ref:null}, {ref:ref(),error:{code:'CHAT_RECORDING_FAILED',message:'uncertain'}}, {ref:ref('other')}, {ref:{...ref(),identity:'other'}}, {error:{code:'new',message:'unknown'}}, {ref:ref('transient'),error:{code:'CHAT_NOT_RECORDED',message:'never invoked'}}];
 for(const r of malformed){id=send();S.chatReceipt(id,r);assert(pending(id)?.textContent.includes('Confirmation unavailable'),'malformed receipt supplied confirmation: '+JSON.stringify(r));pending(id).querySelector('button:last-child').click();}
 id=send();S.chatConnectionLost();S.chatReceipt(id,{ref:ref()});assert(pending(id)?.textContent.includes('unconfirmed'),'retired socket receipt claimed delivery');
 const refused=send();S.chatAcknowledged(refused,'refused');S.chatReceipt(refused,{error:{code:'CHAT_NOT_RECORDED',message:'wait cancelled'}});assert(pending(refused)?.textContent.includes('Not recorded'),'pre-record refusal lost its precise meaning');
 for(const node of Array.from(document.querySelectorAll('[data-request-id]')))Array.from(node.querySelectorAll('button')).find(b=>b.textContent==='Dismiss').click();
 S.chatReceipt(id,{ref:ref()});assert(!pending(id),'dismissed input revived');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionScrollWindowFencesJumpsAndBoundsRetention(t *testing.T) {
	page := `<!doctype html><script type="module">
import {S} from '/state.js';import {sent} from '/ws.js';import '/interaction-view.js';import {run} from '/__harness.js';
run(assert=>{
 const row=n=>({id:'r'+n,sequence:String(n),kind:'message',content:'row '+n});
 const reply=(req,rows=[],extra={},meta={})=>S.interactionPage({version:1,identity:'identity',source:req.interaction_query.source,cursor:req.interaction_query.cursor,incarnation:req.interaction_query.source,fingerprint:'f',rows,...extra},req.request_id,{fingerprint:'f',has_older:true,has_newer:false,...meta});
 S.interactionChanged();reply(sent.at(-1),[row(300)]);reply(sent.at(-1));
 const cleanCount=sent.length;
 let layouts=0;S.interactionVisible=source=>{layouts++;return source==='recorded'?{anchor_id:'r300',end_id:'r300'}:null;};
 S.interactionScroll();S.interactionScroll();assert(sent.length===cleanCount && layouts===0,'fresh scrolling fetched history or scanned row layout');
 delete S.interactionVisible;
 S.interactionChanged();const refreshRead=sent.at(-1);
 S.navigate('recorded','older');assert(sent.at(-1)===refreshRead,'gesture opened a parallel read');
 reply(refreshRead,[row(300)]);const old=sent.at(-1);
 assert(old!==refreshRead && old.interaction_window.mode==='older','gesture was lost behind readback');
 assert(old.interaction_window.anchor_id==='r300','older read lacks exact anchor');
 S.interactionLatest();S.navigate('recorded','older',true);reply(old,[row(299)]);
 assert(S.pages.get('recorded').rows[0].id==='r300','obsolete older reply became latest');
 assert(sent.at(-1).interaction_window.mode==='latest','jump did not take next read slot');
 reply(sent.at(-1),[row(300)]);reply(sent.at(-1));
 for(let end=300;end>50;end-=50){
  S.navigate('recorded','older');reply(sent.at(-1),Array.from({length:50},(_,i)=>row(end-50+i)));
  const rows=S.pages.get('recorded').rows;
  assert(rows.length<=200,'unbounded retained window');
  for(let i=1;i<rows.length;i++)assert(BigInt(rows[i].sequence)===BigInt(rows[i-1].sequence)+1n,'window has gaps/duplicates');
 }
 assert(S.pages.get('recorded').has_newer,'trim hid the route back down');
 S.navigate('recorded','newer');const newer=sent.at(-1);assert(newer.interaction_window.anchor_id==='r249','newer anchor did not track trimmed edge');
 reply(newer,[row(250),row(251)],{}, {has_newer:true});
 S.interactionLost();S.interactionChanged();const resumed=sent.at(-1);
 assert(resumed.interaction_window.mode==='range' && resumed.interaction_window.anchor_id==='r52','reconnect discarded reading interval');
 S.interactionError('INTERACTION_SOURCE_CHANGED','owner replaced',resumed.request_id);
 if(sent.at(-1).interaction_query.source==='transient')reply(sent.at(-1));
 assert(sent.at(-1).interaction_window.mode==='latest','retired source did not reset');
 reply(sent.at(-1),[row(1)],{incarnation:'replacement'});
 assert(S.pages.get('recorded').reset,'replacement source was not qualified');
 if(sent.at(-1).interaction_query.source==='transient')reply(sent.at(-1));
 const count=sent.length;S.view='settings';S.interactionChanged();assert(sent.length===count,'inactive Chat read history');
 S.view='chat';S.interactionWake();assert(sent.length===count+1,'Chat return did not refresh');
});</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/state.js":      []byte(`export const S={};`),
		"/ws.js":         []byte(`export const sent=[];export function wsReady(){return true;}export function send(r){sent.push(r);return r.request_id;}`),
		"/views/chat.js": []byte(`import {S} from '/state.js';export function renderInteractionPages(pages,navigate){S.pages=pages;S.navigate=navigate;}`),
	})
}

func TestInteractionDisclosureAndReadbackKeepTheActiveMicrophoneAndEffort(t *testing.T) {
	markup := strings.Replace(quietChatMarkup, `<button id="mic">Mic</button>`, strings.TrimPrefix(micMarkup, "<!doctype html>"), 1)
	page := markup + `<script type="module">
import {S} from '/state.js';import {renderInteractionPages,renderComposer} from '/views/chat.js';
import {wireMic,render,bindTransport} from '/voice.js';import {run} from '/__harness.js';
run(async assert=>{
 let opens=0,stops=0,closes=0,node=null;const frames=[];
 bindTransport(frame=>frames.push(frame),()=>{});
 const track=new EventTarget();Object.assign(track,{kind:'audio',label:'fixture microphone',readyState:'live',getSettings:()=>({deviceId:'fixture',sampleRate:16000}),stop(){stops++;this.readyState='ended'}});
 const stream={getTracks:()=>[track],getAudioTracks:()=>[track]};
 if(!navigator.mediaDevices)Object.defineProperty(navigator,'mediaDevices',{value:{},configurable:true});
 navigator.mediaDevices.getUserMedia=async()=>{opens++;return stream};
 window.AudioContext=class{constructor(){this.sampleRate=16000;this.destination={}}createMediaStreamSource(){return{connect(){},disconnect(){}}}createScriptProcessor(){node={connect(){},disconnect(){},onaudioprocess:null};return node}close(){closes++}};
 const until=async(fn,label)=>{for(let i=0;i<200&&!fn();i++)await new Promise(r=>setTimeout(r,0));assert(fn(),label)};
 S.stats={name:'Fixture',voice_state:'cloud',voice_listen:'interactive',voice_speak:'off'};S.connected=true;S.identityExists=true;
 S.config={llm:{effort_choice:{levels:['low','high'],in_force:'high'}}};renderComposer();wireMic();render();
 const mic=document.getElementById('mic'), effort=document.getElementById('effort-btn'), input=document.getElementById('msg-input');
 mic.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));mic.dispatchEvent(new PointerEvent('pointerup',{bubbles:true}));
 await until(()=>node?.onaudioprocess,'capture did not start');
 assert(mic.classList.contains('live')&&opens===1,'fixture microphone did not latch');
 input.value='keep my draft';const pages=new Map([['recorded',{identity:'fixture',incarnation:'disk',rows:[{id:'a',kind:'tool_call',details:{tool:'read'}}]}],['transient',{rows:[]}]]);
 renderInteractionPages(pages,()=>{},false);
 const tool=document.querySelector('.tool-ev');tool.querySelector('summary').click();assert(tool.open,'tool did not open');
 pages.get('recorded').rows.push({id:'b',related_id:'a',kind:'tool_result',outcome:'succeeded',details:{tool:'read'}});renderInteractionPages(pages,()=>{},false);
 tool.querySelector('summary').click();assert(!tool.open,'tool did not close');
 assert(document.getElementById('mic')===mic && mic.classList.contains('live') && opens===1 && stops===0 && closes===0,'rendering disrupted or reacquired capture');
 assert(document.getElementById('effort-btn')===effort && document.getElementById('effort-val').textContent==='High' && input.value==='keep my draft','rendering changed effort or composer');
 node.onaudioprocess({inputBuffer:{getChannelData:()=>new Float32Array(4800).fill(0.25)}});
 mic.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));
 await until(()=>frames.length===1,'capture no longer sent');
 assert(stops===1 && closes===1 && !mic.classList.contains('live'),'capture did not release on stop');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionNaturalScrollPreservesReadingAnchor(t *testing.T) {
	page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';import {renderInteractionPages} from '/views/chat.js';import {run} from '/__harness.js';
run(async assert=>{
 const row=n=>({id:'r'+n,sequence:String(n),kind:'message',role:'operator',content:'Message '+n,details:{}});
 const p={identity:'fixture',incarnation:'disk',rows:Array.from({length:30},(_,n)=>row(n+50)),has_older:true,has_newer:false};
 const pages=new Map([['recorded',p],['transient',{rows:[]}]]),requests=[];
 const nav=(source,action)=>requests.push([source,action]);
 renderInteractionPages(pages,nav,false);
 const t=document.getElementById('thread'),input=document.getElementById('msg-input');input.value='unsent';
 t.scrollTop=0;const anchor=document.querySelector('[data-turn-id="r50"]'),top=anchor.getBoundingClientRect().top;
 t.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}));
 assert(requests.length===1 && requests[0][1]==='older','upward gesture did not load earlier');
 p.rows=[...Array.from({length:30},(_,n)=>row(n+20)),...p.rows];renderInteractionPages(pages,nav,false,'hold');
 assert(Math.abs(anchor.getBoundingClientRect().top-top)<1 && anchor===document.querySelector('[data-turn-id="r50"]'),'prepend moved or remounted reading anchor');
 assert(!Array.from(document.querySelectorAll('.interaction-controls button')).some(b=>!b.hidden),'history exposes paging controls');
 assert(input.value==='unsent','scrollback changed draft');
 const before=t.scrollTop;let latest=0;S.interactionLatest=()=>latest++;
 document.getElementById('jump-latest').click();
 assert(latest===1 && t.scrollTop===before,'arrow scrolled old data before readback');
 p.rows=[row(500)];renderInteractionPages(pages,nav,false,'latest');
 assert(t.scrollHeight-t.scrollTop-t.clientHeight<1,'fresh readback did not reach latest');
 p.has_older=true;renderInteractionPages(pages,nav,false);
 t.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}));
 assert(requests.length===2,'short page cannot reach earlier history');
 p.rows=Array.from({length:30},(_,n)=>row(n));
 pages.set('transient',{identity:'fixture',incarnation:'memory',rows:Array.from({length:30},(_,n)=>row(n+1000)),has_older:true});
 renderInteractionPages(pages,nav,false,'latest');
 const band=document.querySelector('[data-turn-id="r1000"]');
 t.scrollTop+=band.getBoundingClientRect().top-t.getBoundingClientRect().top;
 assert(t.scrollTop>140,'fixture did not place transient boundary inside the scrollback');
 t.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}));
 assert(requests.length===3 && requests[2][0]==='transient' && requests[2][1]==='older','transient scrollback stranded behind recorded history');

});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionReloadRestoresReadingAndUnsentDraft(t *testing.T) {
	for _, retired := range []bool{false, true} {
		name := "same_source"
		if retired {
			name = "retired_source"
		}
		t.Run(name, func(t *testing.T) {
			prefix := "const retire=false;"
			if retired {
				prefix = "const retire=true;"
			}
			page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';import {frames} from '/ws.js';import '/interaction-view.js';
import {restoreDraft} from '/overlay.js';import {run} from '/__harness.js';
` + prefix + `
run(async assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;S.view='chat';S.connected=true;
 restoreDraft();
 const again=sessionStorage.getItem('reload-probe')==='1';
 const incarnation=again&&retire?'new-disk':'disk';
 const rows=Array.from({length:again?350:300},(_,i)=>({id:'m'+(i+1),sequence:String(i+1),kind:'message',role:'operator',content:'Private fixture message '+(i+1)+' — enough text to make this row measurable.',details:{}}));
 let index=0;const modes=[];
 const pump=()=>{
  while(index<frames.length){const f=frames[index++];if(f.query!=='interaction')continue;
   const q=f.interaction_query,w=f.interaction_window;modes.push(w.mode);
   if(w.mode!=='latest'&&w.incarnation!==incarnation&&q.source==='recorded'){
    S.interactionError('INTERACTION_SOURCE_CHANGED','retired source',f.request_id);continue;
   }
   let chosen=q.source==='recorded'?rows:[];
   const seq=id=>Number(id.slice(1));
   if(w.mode==='older')chosen=chosen.filter(r=>Number(r.sequence)<seq(w.anchor_id));
   if(w.mode==='newer')chosen=chosen.filter(r=>Number(r.sequence)>seq(w.anchor_id));
   if(w.mode==='range')chosen=chosen.filter(r=>Number(r.sequence)>=seq(w.anchor_id)&&Number(r.sequence)<=seq(w.end_id));
   if(q.cursor)chosen=chosen.filter(r=>Number(r.sequence)<Number(q.cursor));
   const more=chosen.length>50;chosen=w.mode==='newer'?chosen.slice(0,50):chosen.slice(-50);
   const first=chosen[0],last=chosen.at(-1);
   S.interactionPage({version:1,identity:'fixture',source:q.source,incarnation:q.source==='recorded'?incarnation:'memory',rows:chosen,cursor:q.cursor,fingerprint:f.request_id,
    next_cursor:w.mode==='range'&&more?first.sequence:''},f.request_id,
    {fingerprint:f.request_id,has_older:!!first&&Number(first.sequence)>1,has_newer:!!last&&Number(last.sequence)<rows.length});
  }
 };
 S.interactionChanged();pump();
 const thread=document.getElementById('thread'), input=document.getElementById('msg-input');
 if(!again){
  for(let i=0;i<3;i++){thread.scrollTop=0;thread.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}));pump();}
  const anchor=document.querySelector('[data-turn-id="m160"]');assert(anchor,'fixture did not load 200-row window');
  const disclosure=anchor.querySelector('details');disclosure.open=true;
  thread.scrollTop+=anchor.getBoundingClientRect().top-thread.getBoundingClientRect().top-17;
  input.value='Unsent words across reload';input.dispatchEvent(new Event('input',{bubbles:true}));
  sessionStorage.setItem('expected-offset',String(anchor.getBoundingClientRect().top-thread.getBoundingClientRect().top));
  sessionStorage.setItem('reload-probe','1');location.reload();
  return new Promise(()=>{});
 }
 assert(input.value==='Unsent words across reload','ordinary reload erased draft');
 const saved=sessionStorage.getItem('aii.history.position')||'';
 assert(!saved.includes('Private fixture message'),'bookmark cached transcript content');
 assert(modes[0]==='range','reload did not request bookmarked range: '+modes.join());
 if(retire){
  assert(modes.includes('latest')&&document.getElementById('thread-inner').textContent.includes('History source changed'),'retired source was not visibly reset');
 }else{
  assert(!modes.includes('newer'),'bookmark restore started an unwanted live catch-up');
  const anchor=document.querySelector('[data-turn-id="m160"]');assert(anchor,'reload discarded reading window');
  const offset=anchor.getBoundingClientRect().top-thread.getBoundingClientRect().top;
  assert(Math.abs(offset-Number(sessionStorage.getItem('expected-offset')))<2,'reload moved reading anchor: '+offset);
  assert(anchor.querySelector('details').open,'reload closed disclosure');
 }
 input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));
 assert(!sessionStorage.getItem('aii.draft'),'sent draft remained in reload storage');
});</script>`
			runPageInEngines(t, page, chatModuleRig(t))
		})
	}
}

func TestInteractionBookmarkWaitsForChatToBecomeVisible(t *testing.T) {
	page := `<!doctype html><script type="module">
import {S} from '/state.js';import {sent} from '/ws.js';import {run} from '/__harness.js';
run(async assert=>{
 const reading={key:'anchor',offset:17,following:false,open:[]};
 sessionStorage.setItem('aii.history.position',JSON.stringify({version:1,windows:{recorded:{identity:'fixture',incarnation:'disk',anchor_id:'a',end_id:'b'}},reading}));
 S.view='chat';S.interactionFollowing=()=>false;
 await import('/interaction-view.js');S.interactionChanged();
 const request=sent.at(-1);assert(request.interaction_window.mode==='range','bookmark range was not requested');
 S.view='settings';
 S.interactionPage({version:1,identity:'fixture',source:'recorded',incarnation:'disk',cursor:'',fingerprint:'f',rows:[{id:'a',sequence:'1'},{id:'b',sequence:'2'}]},request.request_id,{fingerprint:'f',has_older:false,has_newer:true});
 assert(!S.reading,'hidden Chat restored its DOM');
 S.view='chat';S.interactionWake();
 assert(S.reading?.key==='anchor'&&S.reading.offset===17,'hidden completion discarded the bookmark');
});</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/state.js":      []byte(`export const S={};`),
		"/ws.js":         []byte(`export const sent=[];export function wsReady(){return true;}export function send(r){sent.push(r);return r.request_id;}`),
		"/views/chat.js": []byte(`import {S} from '/state.js';export function renderInteractionPages(p,n,b,pos,reading){if(reading)S.reading=reading;}`),
	})
}

const scrollReaderProbe = `
import {S} from '/state.js';import {frames} from '/ws.js';import '/interaction-view.js';
import {run} from '/__harness.js';
const thread=document.getElementById('thread');
const row=n=>({id:'m'+n,sequence:String(n),kind:'message',role:'operator',content:'Message '+n+'\nAn ordinary conversation line.\nAnother line to read.',details:{}});
let rows=Array.from({length:300},(_,i)=>row(i+1)),index=0,recordSource='recorded';
const requests=()=>frames.filter(f=>f.query==='interaction');
function pump(){let count=0;while(index<frames.length){
 if(++count>80)throw Error('reader did not settle');
 const f=frames[index++];if(f.query!=='interaction')continue;
 const q=f.interaction_query,w=f.interaction_window;let chosen=q.source===recordSource?rows:[];
 const seq=id=>Number(id.slice(1));
 if(w.mode==='older')chosen=chosen.filter(r=>Number(r.sequence)<seq(w.anchor_id));
 if(w.mode==='newer')chosen=chosen.filter(r=>Number(r.sequence)>seq(w.anchor_id));
 if(w.mode==='range')chosen=chosen.filter(r=>Number(r.sequence)>=seq(w.anchor_id)&&Number(r.sequence)<=seq(w.end_id));
 if(q.cursor)chosen=chosen.filter(r=>Number(r.sequence)<Number(q.cursor));
 const more=chosen.length>50;chosen=w.mode==='newer'?chosen.slice(0,50):chosen.slice(-50);
 const first=chosen[0],last=chosen.at(-1);
 S.interactionPage({version:1,identity:'fixture',source:q.source,incarnation:q.source==='recorded'?'disk':'memory',rows:chosen,cursor:q.cursor,fingerprint:f.request_id,next_cursor:w.mode==='range'&&more?first.sequence:''},f.request_id,
 {fingerprint:f.request_id,has_older:!!first&&Number(first.sequence)>1,has_newer:!!last&&Number(last.sequence)<Number(rows.at(-1).sequence)});
}}
const up=()=>thread.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}));
const item=id=>document.querySelector('[data-turn-id="'+id+'"]');
const offset=el=>el.getBoundingClientRect().top-thread.getBoundingClientRect().top;
const frame=()=>new Promise(resolve=>requestAnimationFrame(resolve));
`

func TestInteractionUpwardIntentSurvivesUnchangedRefresh(t *testing.T) {
	page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';import {renderInteractionPages} from '/views/chat.js';import {run} from '/__harness.js';
run(assert=>{
 const thread=document.getElementById('thread');
 const p={identity:'fixture',incarnation:'disk',rows:Array.from({length:50},(_,i)=>({id:'m'+i,sequence:String(i+1),kind:'message',role:'operator',content:'Message '+i+'\nA line to read.\nAnother line.',details:{}})),has_older:false,has_newer:false};
 const pages=new Map([['recorded',p],['transient',{rows:[]}]]);
 renderInteractionPages(pages,()=>{},false,'latest');
 thread.scrollTop-=80;thread.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}));
 const before=thread.scrollTop;
 renderInteractionPages(pages,()=>{},true);
 assert(Math.abs(thread.scrollTop-before)<1,'unchanged refresh snapped upward reader by '+(thread.scrollTop-before)+'px');
 assert(!document.getElementById('jump-latest').hidden,'upward reader lost the route to latest');
 p.rows.push({id:'new',sequence:'51',kind:'message',role:'resident',content:'New reply',details:{}});
 renderInteractionPages(pages,()=>{},false);
 assert(Math.abs(thread.scrollTop-before)<1,'new reply overrode upward intent');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionDelayedTailCannotEvictTheReadingAnchor(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + scrollReaderProbe + `
run(async assert=>{
 S.view='chat';S.interactionChanged();pump();
 for(let i=0;i<2;i++){thread.scrollTop=0;up();pump();}
 assert(document.querySelectorAll('[data-record-key]').length===150,'fixture did not load 150 records');
 document.getElementById('jump-latest').click();pump();
 for(let i=0;i<2;i++){thread.scrollTop=0;up();pump();}
 thread.dispatchEvent(new WheelEvent('wheel',{deltaY:80,bubbles:true}));
 thread.scrollTop=thread.scrollHeight;thread.dispatchEvent(new Event('scroll'));
 S.interactionChanged();const outstanding=requests().length;
 const anchor=item('m190');assert(anchor,'missing fixture anchor');
 thread.scrollTop+=offset(anchor)-20;up();const before=offset(anchor);
 assert(requests().length===outstanding,'gesture opened a second request lane');
 pump();await frame();
 assert(anchor.isConnected,'delayed background read evicted the message being read');
 assert(Math.abs(offset(anchor)-before)<1,'delayed background read moved the anchor');
 assert(document.querySelectorAll('[data-record-key]').length===150,'refresh discarded the retained window');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionToolUpdatesRetainConversationWithinTheWindow(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + scrollReaderProbe + `
run(async assert=>{
 rows=Array.from({length:49},(_,i)=>row(i+1));
 const call=n=>({id:'m'+n,sequence:String(n),kind:'tool_call',role:'system',turn_id:'active',content:'{}',details:{tool:'read'}});
 rows.push(call(50));S.interactionChanged();pump();
 const anchor=item('m49'),height=thread.scrollHeight;assert(anchor&&height>thread.clientHeight,'fixture lacks scrollback');
 for(let n=51;n<=100;n++)rows.push(n%2?{id:'m'+n,sequence:String(n),kind:'tool_result',role:'system',turn_id:'active',related_id:'m'+(n-1),outcome:'succeeded',content:'ok',details:{tool:'read'}}:call(n));
 S.interactionChanged();pump();await frame();
 assert(anchor.isConnected,'tool-only network page erased the preceding conversation');
 assert(thread.scrollHeight>=height-1,'tool updates collapsed the scrollable surface');
 assert(document.querySelectorAll('[data-record-key]').length===100,'reader did not retain the contiguous 100-record window');
 assert(thread.scrollHeight-thread.clientHeight-thread.scrollTop<2,'following reader stopped following');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionInputIntentAndUnchangedPosition(t *testing.T) {
	page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';import {renderInteractionPages,toggleThinkingDots} from '/views/chat.js';import {run} from '/__harness.js';
run(assert=>{
 const t=document.getElementById('thread'),p={identity:'fixture',incarnation:'disk',rows:Array.from({length:20},(_,i)=>({id:'m'+i,sequence:String(i+1),kind:'message',role:'operator',content:'Message '+i+'\nA line to read.\nAnother line.',details:{}})),has_older:false,has_newer:false};
 const pages=new Map([['recorded',p],['transient',{rows:[]}]]),draw=position=>renderInteractionPages(pages,()=>{},false,position);
 const touch=(type,y)=>{const e=new Event(type,{bubbles:true});Object.defineProperty(e,'touches',{value:[{clientY:y}]});t.dispatchEvent(e);};
 const gestures=[()=>t.dispatchEvent(new WheelEvent('wheel',{deltaY:-1,bubbles:true})),()=>{touch('touchstart',100);touch('touchmove',110);},()=>t.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true})),()=>t.dispatchEvent(new KeyboardEvent('keydown',{key:' ',shiftKey:true,bubbles:true})),()=>{t.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));t.dispatchEvent(new Event('scroll'));window.dispatchEvent(new PointerEvent('pointerup'));}];
 for(const gesture of gestures){draw('latest');t.scrollTop-=10;gesture();const top=t.scrollTop;draw();assert(!S.interactionFollowing()&&Math.abs(top-t.scrollTop)<1,'input did not hold its reading position');}
 t.dispatchEvent(new WheelEvent('wheel',{deltaY:100,bubbles:true}));t.scrollTop=t.scrollHeight;t.dispatchEvent(new Event('scroll'));
 assert(S.interactionFollowing(),'returning to bottom did not resume following');
 const prop=Object.getOwnPropertyDescriptor(Element.prototype,'scrollTop');let writes=0;
 Object.defineProperty(t,'scrollTop',{configurable:true,get(){return prop.get.call(this)},set(v){writes++;prop.set.call(this,v)}});
 draw();assert(writes===0,'unchanged refresh wrote scrollTop '+writes+' times');
 delete t.scrollTop;
 const details=t.querySelector('[data-turn-id="m19"] details');details.querySelector('summary').click();
 assert(!S.interactionFollowing(),'opening a disclosure retained automatic following');
 const before=t.scrollTop;S.thinking=true;toggleThinkingDots(true);
 assert(Math.abs(t.scrollTop-before)<1,'thinking indicator overrode disclosure reading');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionTailCatchupStaysBoundedAndRefreshesFacts(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + scrollReaderProbe + `
run(async assert=>{
 rows=Array.from({length:50},(_,i)=>row(i+1));S.interactionChanged();pump();
 rows[49]={...rows[49],content:'Updated recorded message'};
 for(let n=51;n<=260;n++)rows.push(row(n));
 S.interactionChanged();pump();await frame();
 let ids=Array.from(document.querySelectorAll('[data-record-key]')).map(el=>Number(JSON.parse(el.dataset.recordKey)[3].slice(1)));
 assert(ids.length===200&&ids[0]===61&&ids.at(-1)===260,'catch-up did not trim only at 200: '+[ids.length,ids[0],ids.at(-1)]);
 assert(ids.every((id,i)=>!i||id===ids[i-1]+1),'catch-up left gaps or duplicates');
 const anchor=item('m260');rows[259]={...rows[259],content:'Corrected latest message'};
 S.interactionChanged();pump();assert(anchor.isConnected&&anchor.textContent.includes('Corrected latest message'),'visible facts were not refreshed in place');
 const count=requests().length;S.interactionScroll();assert(requests().length===count,'settled fresh history polled');
 thread.scrollTop=0;up();pump();
 ids=Array.from(document.querySelectorAll('[data-record-key]')).map(el=>Number(JSON.parse(el.dataset.recordKey)[3].slice(1)));
 assert(ids.length===200&&ids[0]===11&&ids.at(-1)===210,'trimmed older edge was not reachable');
 const kept=item('m30');thread.scrollTop+=offset(kept)-10;up();const before=offset(kept);
 document.getElementById('jump-latest').click();up();pump();await frame();
 assert(kept.isConnected&&Math.abs(offset(kept)-before)<1,'pending latest jump overrode the newer gesture');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionTransientTailAndHiddenReadbackKeepIntent(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + scrollReaderProbe + `
run(async assert=>{
 recordSource='transient';rows=Array.from({length:50},(_,i)=>row(i+1));S.interactionChanged();pump();
 const anchor=item('m49');rows.push(row(51));S.interactionChanged();pump();
 assert(anchor.isConnected&&item('m51'),'transient tail replaced the window');
 thread.scrollTop-=20;up();const before=offset(anchor);
 let hidden=true;Object.defineProperty(document,'visibilityState',{configurable:true,get:()=>hidden?'hidden':'visible'});
 const count=requests().length;rows.push(row(52));S.interactionChanged();assert(requests().length===count,'hidden Chat fetched history');
 hidden=false;S.interactionWake();pump();await frame();
 assert(anchor.isConnected&&Math.abs(offset(anchor)-before)<1,'foreground readback overrode transient reading intent');
 S.interactionLost();S.interactionChanged();pump();await frame();
 assert(anchor.isConnected&&Math.abs(offset(anchor)-before)<1,'reconnect overrode transient reading intent');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestInteractionJumpWaitsForBothSourcesBeforeFollowing(t *testing.T) {
	page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';import {frames} from '/ws.js';import '/interaction-view.js';import {run} from '/__harness.js';
run(assert=>{
 const t=document.getElementById('thread');
 const rows=Array.from({length:30},(_,i)=>({id:'m'+i,sequence:String(i+1),kind:'message',role:'operator',content:'Message '+i+'\nA line of conversation.',details:{}}));
 const reply=()=>{const f=frames.at(-1),source=f.interaction_query.source;S.interactionPage({version:1,identity:'fixture',source,incarnation:source,rows,cursor:'',fingerprint:f.request_id},f.request_id,{fingerprint:f.request_id,has_older:false,has_newer:false});};
 S.interactionChanged();reply();reply();
 t.scrollTop=200;t.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}));const before=t.scrollTop;
 document.getElementById('jump-latest').click();reply();
 assert(t.scrollTop===before,'first source moved the reader before the second source was current');
 reply();assert(t.scrollHeight-t.clientHeight-t.scrollTop<2&&S.interactionFollowing(),'completed jump did not resume following');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}
