//go:build !windows

package dashboard

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestATurnsWorkFoldsIntoOneReceiptBesideItsReply(t *testing.T) {
	page := `<!doctype html><link rel="stylesheet" href="/layout.css">
<div id="thread" style="height:250px;width:380px;overflow:auto"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import {S} from '/state.js';import {renderInteractionPages,toggleThinkingDots,sendChat} from '/views/chat.js';import {foldTurns,displayInteractions} from '/interaction-display.js';import {run} from '/__harness.js';
run(async assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;
 const at=s=>'2026-09-26T15:'+s+'Z';
 const rows=[
  {id:'q',sequence:'1',kind:'message',role:'operator',content:'Review the tree',turn_id:'t1',recorded_at:at('52:49'),details:{}},
  {id:'c1',sequence:'2',kind:'tool_call',role:'system',turn_id:'t1',recorded_at:at('53:37'),content:'{}',details:{tool:'shell'}},
  {id:'r1',related_id:'c1',sequence:'3',kind:'tool_result',role:'system',turn_id:'t1',recorded_at:at('53:40'),outcome:'succeeded',content:'ok',details:{tool:'shell'}},
  {id:'n1',sequence:'4',kind:'notice',role:'system',turn_id:'t1',recorded_at:at('55:29'),content:'Work plan updated',details:{}},
  {id:'steer',sequence:'5',kind:'message',role:'operator',content:'Also check the tests',turn_id:'t1',recorded_at:at('55:50'),details:{}},
  {id:'c2',sequence:'6',kind:'tool_call',role:'system',turn_id:'t1',recorded_at:at('56:07'),content:'{}',details:{tool:'pl_id_example_planning_plan'}},
  {id:'r2',related_id:'c2',sequence:'7',kind:'tool_result',role:'system',turn_id:'t1',recorded_at:at('56:09'),outcome:'failed',content:'refused',details:{tool:'pl_id_example_planning_plan'}},
  {id:'spawn',sequence:'8',kind:'notice',role:'system',recorded_at:at('57:00'),content:'Work queued: sub-agent',details:{}},
  {id:'s1',sequence:'9',kind:'tool_call',role:'system',turn_id:'sub',recorded_at:at('57:10'),content:'{}',details:{tool:'read'}},
  {id:'sr1',related_id:'s1',sequence:'10',kind:'tool_result',role:'system',turn_id:'sub',recorded_at:at('57:12'),outcome:'succeeded',content:'ok',details:{tool:'read'}},
  {id:'c3',sequence:'11',kind:'tool_call',role:'system',turn_id:'t1',recorded_at:at('59:28'),content:'{}',details:{tool:'pl_id_aiii_codequality_scan'}},
  {id:'r3',related_id:'c3',sequence:'12',kind:'tool_result',role:'system',turn_id:'t1',recorded_at:at('59:30'),outcome:'succeeded',content:'ok',details:{tool:'pl_id_aiii_codequality_scan'}},
  {id:'a',sequence:'13',kind:'message',role:'resident',content:'The findings describe a sample.',turn_id:'t1',recorded_at:'2026-09-26T16:11:04Z',details:{}},
  {id:'cut',related_id:'a',sequence:'14',kind:'notice',role:'system',turn_id:'t1',recorded_at:'2026-09-26T16:11:05Z',content:'[the reply above is incomplete — stopped by the operator]',details:{}},
  {id:'n2',sequence:'15',kind:'notice',role:'system',turn_id:'t1',recorded_at:'2026-09-26T16:11:06Z',content:'Work result delivered',details:{}},
  {id:'legacy',sequence:'16',kind:'legacy',role:'system',content:'Resuming per plan',details:{}},
  {id:'q2',sequence:'17',kind:'message',role:'operator',content:'',content_ref:{id:'q2',source:'recorded',incarnation:'disk',sha256:'abc',bytes:__DETAIL_BYTES__},turn_id:'t2',recorded_at:'2026-09-27T09:31:00Z',details:{}},
  {id:'c4',sequence:'18',kind:'tool_call',role:'system',turn_id:'t3',recorded_at:'2026-09-27T09:40:00Z',content:'{}',details:{tool:'recall'}},
 ];
 const p={identity:'fixture',incarnation:'disk',rows,has_older:false,has_newer:false};
 const pages=new Map([['recorded',p],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]);
 const shape=()=>foldTurns(displayInteractions(pages).get('recorded'),p).map(x=>x.receipt?'R['+x.items.map(e=>e.anchor.row.id).join('+')+']':x.anchor.row.id).join();
 assert(shape()==='q,R[c1+n1],steer,R[c2+spawn+s1+c3],a,cut,R[n2],legacy,q2,R[c4]','fold: '+shape());
 const second=foldTurns(displayInteractions(pages).get('recorded'),p)[3];
 assert(second.steps===3&&second.failed===1&&second.replied&&second.notes===1&&second.turns.join()==='t1,sub','second stretch: '+JSON.stringify([second.steps,second.failed,second.replied,second.notes,second.turns]));
 renderInteractionPages(pages,()=>{},false);
 const inner=document.getElementById('thread-inner');
 const keys=()=>Array.from(inner.querySelectorAll('[data-record-key]')).map(el=>JSON.parse(el.dataset.recordKey)[3]).join();
 assert(keys()==='q,c1,r1,n1,steer,c2,r2,spawn,s1,sr1,c3,r3,a,cut,n2,legacy,q2,c4','drawn order is not recorded order: '+keys());
 const top=Array.from(inner.children).filter(el=>el.dataset.interactionKey);
 assert(top.length===10,'thread rows: '+top.length+' (want 10)');
 const loose=top.filter(el=>!el.classList.contains('turn-receipt')&&el.querySelector(':scope > .tool-ev')).map(el=>el.dataset.turnId);
 assert(loose.join()==='cut,legacy','compact rows loose in the thread: '+loose.join()+' (only the reply\'s own notice and the imported line)');
 const receipts=()=>inner.querySelectorAll('.turn-receipt'), line=i=>receipts()[i].querySelector('.receipt > summary').textContent;
 assert(line(0)==='1 step','a stretch a steer message follows claimed a span: '+line(0));
 assert(line(1)==='Worked 15 min · 3 steps · 1 failed','second stretch: '+line(1));
 assert(line(2)==='1 recorded note','the note after the reply: '+line(2));
 assert(line(3)==='1 step · 1 unfinished','work at rest with no result: '+line(3));
 assert(receipts()[1].classList.contains('trouble')&&!receipts()[0].classList.contains('trouble')&&receipts()[3].classList.contains('partial'),'marks: '+Array.from(receipts()).map(e=>e.className).join('|'));
 assert(!receipts()[1].classList.contains('record-failed')&&!inner.querySelector('[data-turn-id="c3"]').classList.contains('record-failed')&&inner.querySelector('[data-turn-id="c3"] .tool-outcome').textContent==='Done','a failed stretch painted its succeeded step as failed');
 assert(receipts()[1].nextElementSibling?.dataset.turnId==='a','the second stretch is not beside the reply');
 assert(!Array.from(inner.querySelectorAll('details.receipt, details.receipt-all')).some(d=>d.open),'a receipt is open by default');
 const names=Array.from(receipts()[1].querySelectorAll('.receipt-groups .g-name')).map(e=>e.textContent);
 assert(names.join('|')==='pl_id_example_planning_plan|Work queued: sub-agent|read|pl_id_aiii_codequality_scan','groups: '+names.join('|'));
 assert(receipts()[1].querySelector('.g-count.bad').textContent.includes('1 failed'),'the failed group is not marked');
 const before=inner.querySelector('[data-turn-id="c2"]');
 let mutations=0;const observer=new MutationObserver(list=>mutations+=list.length);observer.observe(inner,{subtree:true,childList:true,attributes:true,characterData:true});
 renderInteractionPages(pages,()=>{},false);await Promise.resolve();observer.disconnect();
 assert(mutations===0,'identical snapshot did DOM work: '+mutations);
 assert(inner.querySelector('[data-turn-id="c2"]')===before,'a step inside a receipt was remounted');
 const box=receipts()[1].querySelector('.receipt'), every=receipts()[1].querySelector('.receipt-all');
 box.querySelector('summary').click();assert(box.open&&!every.open,'the first click did not stop at the groups');
 renderInteractionPages(pages,()=>{},false);assert(box.open&&!every.open,'a re-render changed what was open');
 every.querySelector('summary').click();assert(every.open&&!every.querySelector('.tool-ev').open,'the second click opened more than the list');
 box.querySelector('summary').click();assert(!box.open,'the receipt did not close');
 const thread=document.getElementById('thread');
 p.has_older=true;p.rows=rows.slice(5);renderInteractionPages(pages,()=>{},false,'latest');
 const cut=receipts()[0];assert(cut.querySelector('.receipt-label').textContent==='3 steps in view · 1 failed','the cut stretch: '+cut.querySelector('.receipt-label').textContent);
 cut.querySelector('.receipt').open=true;cut.querySelector('.receipt-all').open=true;
 const step=inner.querySelector('[data-turn-id="c3"]');thread.scrollTop=step.offsetTop-thread.offsetTop;
 const stepBefore=step.getBoundingClientRect().top, keyBefore=cut.dataset.interactionKey;
 const older=[{id:'c0',sequence:'0',kind:'tool_call',role:'system',turn_id:'t1',recorded_at:at('55:55'),content:'{}',details:{tool:'grep'}},{id:'r0',related_id:'c0',sequence:'0.5',kind:'tool_result',role:'system',turn_id:'t1',recorded_at:at('55:56'),outcome:'succeeded',content:'ok',details:{tool:'grep'}}];
 p.rows=[...rows.slice(0,5),...older,...rows.slice(5)];p.has_older=false;renderInteractionPages(pages,()=>{},false,'hold');
 assert(receipts()[1]===cut,'a moved window rebuilt the open receipt: receipts '+receipts().length+' keys '+Array.from(receipts()).map(e=>e.dataset.interactionKey.slice(0,40)).join(' | ')+' cut connected '+cut.isConnected);
 assert(cut.dataset.interactionKey!==keyBefore,'the carried receipt kept its old key');
 assert(cut.querySelector('.receipt').open&&cut.querySelector('.receipt-all').open,'the carried receipt lost its open state');
 assert(inner.querySelector('[data-turn-id="c3"]')===step,'a record inside the carried receipt was remounted');
 assert(Math.abs(step.getBoundingClientRect().top-stepBefore)<1,'a moved window jumped the reader off the step: '+(step.getBoundingClientRect().top-stepBefore));
 assert(line(1)==='Worked 15 min · 4 steps · 1 failed','the carried receipt did not take the new record: '+line(1));
 cut.querySelector('.receipt').open=false;cut.querySelector('.receipt-all').open=false;p.rows=rows;renderInteractionPages(pages,()=>{},false,'latest');
 thread.scrollTop=0;thread.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}));const readerAt=thread.scrollTop;
 let touched=[];const watcher=new MutationObserver(list=>list.forEach(m=>{const dots=[...m.addedNodes,...m.removedNodes].every(n=>n.classList?.contains('thinking-row'));if(!(m.type==='childList'&&m.target===inner&&dots))touched.push(m.target);}));
 watcher.observe(inner,{subtree:true,childList:true,attributes:true,characterData:true});
 S.thinking=true;toggleThinkingDots(true);await Promise.resolve();
 assert(line(3)==='Working · 1 step'&&receipts()[3].classList.contains('live'),'running work: '+line(3));
 assert(touched.length&&touched.every(n=>receipts()[3].contains(n))&&thread.scrollTop===readerAt,'a thinking toggle rendered or scrolled: '+touched.length+' mutations outside the dots, scrollTop '+thread.scrollTop);
 watcher.disconnect();
 S.thinking=false;toggleThinkingDots(false);assert(line(3)==='1 step · 1 unfinished'&&!receipts()[3].classList.contains('live'),'stale Working after the turn ended: '+line(3));
 p.has_newer=true;S.thinking=true;renderInteractionPages(pages,()=>{},false);
 assert(line(3)==='1 step in view'&&!receipts()[3].classList.contains('live')&&!receipts()[3].classList.contains('partial'),'earlier history claimed the running turn or marked it: '+line(3)+' '+receipts()[3].className);
 p.has_newer=false;S.thinking=false;renderInteractionPages(pages,()=>{},false);
 assert(sendChat('a new question'),'send refused');
 assert(line(3)==='1 step · 1 unfinished'&&!receipts()[3].classList.contains('live'),'a send made finished work say Working: '+line(3));
 S.thinking=false;toggleThinkingDots(false);
 p.has_newer=false;p.has_older=true;S.thinking=false;p.rows=rows.slice(1);renderInteractionPages(pages,()=>{},false);
 assert(line(0)==='1 step in view','a stretch cut by the window claimed a whole turn: '+line(0));
 p.rows=rows;p.has_older=false;S.thinking=true;
 pages.get('transient').rows=[{id:'sc',sequence:'1',kind:'tool_call',role:'system',turn_id:'safe',content:'{}',details:{tool:'read'}}];
 const tail=()=>Array.from(receipts()).at(-1).querySelector('.receipt > summary').textContent;
 S.cont={mode:'safe'};renderInteractionPages(pages,()=>{},false);
 assert(line(3)==='1 step · 1 unfinished'&&tail()==='Working · 1 step · not saved','SAFE: '+line(3)+' | '+tail());
 S.cont={mode:'normal'};renderInteractionPages(pages,()=>{},false);
 assert(line(3)==='Working · 1 step'&&tail()==='1 step · 1 unfinished · not saved','normal mode with a kept row: '+line(3)+' | '+tail());
 pages.get('transient').rows=[];S.thinking=false;S.cont=null;renderInteractionPages(pages,()=>{},false);
 renderInteractionPages(pages,()=>{},false,'latest');
 const message=inner.querySelector('[data-turn-id="q2"]'),body=message.querySelector('.body');
 for(let i=0;i<200&&!body.textContent;i++)await new Promise(r=>setTimeout(r,5));
 assert(body.textContent.startsWith('Just before sunset, a student recorded the temperature.'),'message text was not loaded inline: '+body.textContent.slice(0,60));
 assert(!message.querySelector('.long-card'),'the old full-text card remains');
 assert(!message.querySelector('.copyb').hidden&&message.querySelector('.copyb').dataset.copyText===body.textContent,'loaded words are not copyable');
 const threadBox=thread.getBoundingClientRect(),textBox=body.getBoundingClientRect();
 assert(textBox.left>=threadBox.left-1&&textBox.right<=threadBox.right+1&&body.scrollWidth<=body.clientWidth+1,'the inline text does not fit the thread');

});</script>`
	text := "Just before sunset, a student recorded the temperature. "
	for i := 0; i < 40; i++ {
		text += "The student found that the soil was warmer than the air, and wrote a long line about it without a break. "
	}
	record, _ := json.Marshal(map[string]any{"id": "q2", "kind": "message", "role": "operator", "content": text, "turn_id": "t2"})
	page = strings.ReplaceAll(page, "__DETAIL_BYTES__", strconv.Itoa(len(record)))
	chunk, _ := json.Marshal(map[string]any{"version": 1, "id": "q2", "source": "recorded", "size": len(record), "offset": 0, "sha256": "abc", "incarnation": "disk", "data_b64": base64.StdEncoding.EncodeToString(record), "bytes": len(record), "eof": true})
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	modules := chatModuleRig(t)
	modules["/interaction/detail"] = chunk
	modules["/layout.css"] = css
	runPageInEngines(t, page, modules)
}

func TestAnOlderPageThatOnlyDeepensTheTopReceiptAsksForTheNext(t *testing.T) {
	page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';import {renderInteractionPages} from '/views/chat.js';import {run} from '/__harness.js';
run(async assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;
 const at=n=>'2026-09-26T15:'+String(Math.floor(n/60)%60).padStart(2,'0')+':'+String(n%60).padStart(2,'0')+'Z';
 const pairs=(a,b)=>{const out=[];for(let n=a;n<=b;n++)out.push({id:'c'+n,sequence:String(2*n),kind:'tool_call',role:'system',turn_id:'t1',recorded_at:at(n),content:'{}',details:{tool:'read'}},{id:'r'+n,related_id:'c'+n,sequence:String(2*n+1),kind:'tool_result',role:'system',turn_id:'t1',recorded_at:at(n),outcome:'succeeded',content:'ok',details:{tool:'read'}});return out;};
 const reply={id:'a',sequence:'200',kind:'message',role:'resident',content:'Done.',turn_id:'t1',recorded_at:at(100),details:{}};
 const question={id:'q',sequence:'39',kind:'message',role:'operator',content:'Read the tree',turn_id:'t1',recorded_at:at(19),details:{}};
 const tick=()=>new Promise(r=>setTimeout(r,0));
 const asked=[];const navigate=(s,a)=>asked.push(s+':'+a);
 const p={identity:'fixture',incarnation:'disk',rows:[...pairs(50,69),reply],has_older:true,has_newer:false};
 const pages=new Map([['recorded',p],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]);
 renderInteractionPages(pages,navigate,false,'latest');await tick();
 assert(asked.length===0,'the latest window asked for older history: '+asked.join());
 const inner=document.getElementById('thread-inner'), top=()=>Array.from(inner.children).find(el=>el.dataset.interactionKey);
 const first=top();
 p.rows=[...pairs(30,49),...p.rows];renderInteractionPages(pages,navigate,false,'hold');await tick();
 assert(top()===first&&first.querySelector('.receipt-label').textContent==='40 steps in view','fixture: the older page did not fold into the top receipt: '+first.querySelector('.receipt-label').textContent);
 assert(asked.join()==='recorded:older','an older page that drew nothing new waited for another gesture: '+asked.join());
 renderInteractionPages(pages,navigate,false,'hold');await tick();
 assert(asked.length===1,'a refresh of the same window paged: '+asked.join());
 first.querySelector('.receipt').open=true;first.querySelector('.receipt-all').open=true;
 const h=document.getElementById('thread').scrollHeight;
 p.rows=[...pairs(25,29),...p.rows];renderInteractionPages(pages,navigate,false,'hold');await tick();
 assert(document.getElementById('thread').scrollHeight>h,'expanded fixture added no space');
 assert(asked.length===1,'expanded receipt caused automatic history traversal');
 first.querySelector('.receipt').open=false;first.querySelector('.receipt-all').open=false;

 p.rows=[question,...pairs(20,24),...p.rows];renderInteractionPages(pages,navigate,false,'hold');await tick();
 assert(top()!==first&&top().dataset.turnId==='q','the question was not drawn at the top');
 assert(asked.length===1,'paging continued after something new was drawn: '+asked.join());
 p.rows=[...pairs(50,69),reply];p.has_older=false;renderInteractionPages(pages,navigate,false,'latest');await tick();
 p.rows=[...pairs(30,49),...p.rows];renderInteractionPages(pages,navigate,false,'hold');await tick();
 assert(asked.length===1,'paging went past the start of the record: '+asked.join());
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestExpandedReceiptStopsPagingBeforeEvictingTheReader(t *testing.T) {
	page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';import {frames} from '/ws.js';import '/interaction-view.js';import {run} from '/__harness.js';
run(async assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;S.view='chat';S.connected=true;
 const rows=[{id:'q',sequence:'1',kind:'message',role:'operator',content:'Read the tree',turn_id:'t',details:{}}];
 for(let n=1;n<=300;n++)rows.push({id:'c'+n,sequence:String(2*n),kind:'tool_call',role:'system',turn_id:'t',content:'{}',details:{tool:'read'}},{id:'r'+n,sequence:String(2*n+1),kind:'tool_result',role:'system',turn_id:'t',related_id:'c'+n,content:'result '+n,outcome:'succeeded',details:{tool:'read'}});
 rows.push({id:'a',sequence:'1000',kind:'message',role:'resident',content:'Done',turn_id:'t',details:{}});
 let index=0;const served=[];
 async function pump(){
  for(let round=0;round<30;round++){
   await Promise.resolve();if(index>=frames.length)return;
   const f=frames[index++];if(f.query!=='interaction')continue;
   const source=f.interaction_query.source,w=f.interaction_window;
   let chosen=source==='recorded'?rows:[];
   if(w.mode==='older'){const anchor=rows.find(r=>r.id===w.anchor_id);chosen=chosen.filter(r=>BigInt(r.sequence)<BigInt(anchor.sequence));}
   chosen=chosen.slice(-50);served.push(w.mode);
   const first=chosen[0],last=chosen.at(-1);
   S.interactionPage({version:1,identity:'fixture',source,incarnation:source==='recorded'?'disk':'memory',rows:chosen,cursor:'',fingerprint:f.request_id},f.request_id,
    {fingerprint:f.request_id,has_older:!!first&&Number(first.sequence)>1,has_newer:!!last&&Number(last.sequence)<1000});
  }
  throw Error('unbounded reader');
 }
 S.interactionChanged();await pump();
 const receipt=document.querySelector('.receipt');receipt.open=true;receipt.querySelector('.receipt-all').open=true;
 const anchor=document.querySelector('[data-turn-id="c280"]'), thread=document.getElementById('thread');thread.scrollTop=0;
 const count=served.length;thread.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}));await pump();
 assert(served.length===count+1,'one gesture traversed '+(served.length-count)+' pages of expanded work');
 assert(anchor.isConnected,'automatic traversal evicted the reading anchor');
 assert(document.querySelectorAll('[data-record-key]').length<=200,'retention bound was removed');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}
