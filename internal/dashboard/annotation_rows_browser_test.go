//go:build !windows

package dashboard

import "testing"

func TestAnAnnotationAboutAMessageStaysWithItsMessage(t *testing.T) {
	page := `<!doctype html><link rel="stylesheet" href="/layout.css">
<div id="thread" style="height:400px;width:380px;overflow:auto"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import {S} from '/state.js';import {renderInteractionPages} from '/views/chat.js';import {foldTurns,displayInteractions} from '/interaction-display.js';import {run} from '/__harness.js';
run(async assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;
 const at=s=>'2026-09-27T10:'+s+'Z';
 const rows=[
  {id:'q',sequence:'1',kind:'message',role:'operator',content:'Check the logs',turn_id:'t1',recorded_at:at('00:00'),details:{},annotations:{voice:{session:'v1',sequence:4}}},
  {id:'voice',related_id:'q',sequence:'2',kind:'annotation',role:'system',turn_id:'t1',recorded_at:at('00:01'),content:'',details:{annotation:{kind:'voice',key:'v1/4',payload:{session:'v1',sequence:4}}}},
  {id:'c1',sequence:'3',kind:'tool_call',role:'system',turn_id:'t1',recorded_at:at('00:05'),content:'{}',details:{tool:'shell'}},
  {id:'r1',related_id:'c1',sequence:'4',kind:'tool_result',role:'system',turn_id:'t1',recorded_at:at('00:07'),outcome:'succeeded',content:'ok',details:{tool:'shell'}},
  {id:'speaker',related_id:'q',sequence:'5',kind:'annotation',role:'system',turn_id:'t1',recorded_at:at('00:08'),content:'',details:{annotation:{kind:'speaker',key:'v1/4',payload:{}}}},
  {id:'c2',sequence:'6',kind:'tool_call',role:'system',turn_id:'t1',recorded_at:at('00:10'),content:'{}',details:{tool:'read'}},
  {id:'r2',related_id:'c2',sequence:'7',kind:'tool_result',role:'system',turn_id:'t1',recorded_at:at('00:11'),outcome:'succeeded',content:'ok',details:{tool:'read'}},
  {id:'a',sequence:'8',kind:'message',role:'resident',content:'The logs are clean.',turn_id:'t1',recorded_at:at('01:00'),details:{}},
  {id:'away',related_id:'gone',sequence:'9',kind:'annotation',role:'system',turn_id:'t1',recorded_at:at('01:01'),content:'',details:{annotation:{kind:'speaker',key:'x',payload:{}}}},
  {id:'n',sequence:'10',kind:'annotation',role:'system',turn_id:'t1',recorded_at:at('01:02'),content:'A substrate note about no row',details:{}},
 ];
 const p={identity:'fixture',incarnation:'disk',rows,has_older:false,has_newer:false};
 const pages=new Map([['recorded',p],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]);
 const shape=()=>foldTurns(displayInteractions(pages).get('recorded'),p).map(x=>x.receipt?'R['+x.items.map(e=>e.anchor.row.id).join('+')+']':x.anchor.row.id).join();
 assert(shape()==='q,R[c1+c2],a,R[n]','fold: '+shape()+' — the speaker note split the stretch, or an annotation about a row became an item');
 const q=displayInteractions(pages).get('recorded').find(x=>x.anchor.row.id==='q');
 assert(q.records.map(e=>e.row.id).join()==='q,voice,speaker','the message does not carry its annotations: '+q.records.map(e=>e.row.id).join());
 renderInteractionPages(pages,()=>{},false);
 const inner=document.getElementById('thread-inner');
 assert(!inner.textContent.includes('Recorded annotation'),'an empty annotation line is drawn in the thread');
 const top=Array.from(inner.children).filter(el=>el.dataset.interactionKey);
 assert(top.length===4,'thread rows: '+top.length+' (want 4: the message, one receipt, the reply, the note)');
 const message=inner.querySelector('[data-turn-id="q"]');
 assert(message.dataset.voiceRef==='v1/4','the message lost its own voice fact');
 const details=message.querySelector('.message-context');
 assert(details&&!details.open,'the message has no closed details');
 const kinds=Array.from(details.querySelectorAll('.record-detail .interaction-meta')).map(e=>e.textContent.split(' · ')[0]);
 assert(kinds.join()==='message,annotation,annotation','the message\'s details: '+kinds.join());
});</script>`
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	modules := chatModuleRig(t)
	modules["/layout.css"] = css
	runPageInEngines(t, page, modules)
}
