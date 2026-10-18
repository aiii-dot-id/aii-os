//go:build !windows

package dashboard

import "testing"

const thinkingRig = `
import {S} from '/state.js';
import {renderInteractionPages} from '/views/chat.js';
import {run,until} from '/__harness.js';
S.view='chat';S.stats={name:'Fixture'};
const inner=document.getElementById('thread-inner');
const row=(id,seq,role,text)=>({id,sequence:String(seq),kind:'message',role,content:text,details:{}});
const pages=(recorded,transient=[])=>new Map([['recorded',{identity:'fixture',incarnation:'disk',rows:recorded}],['transient',{identity:'fixture',incarnation:'memory',rows:transient}]]);
const draw=p=>renderInteractionPages(p,()=>{},false,'latest',null);
const owner=id=>document.querySelector('[data-turn-id="'+id+'"]');
const details=id=>owner(id).querySelector('.message-context');
const thinking=id=>owner(id).querySelector('.record-thinking');
const settle=(ms=40)=>new Promise(r=>setTimeout(r,ms));
const toggle=async id=>{details(id).querySelector(':scope > summary').click();await settle();};
const calls=[],kept=new Map();let fail=false;
const realFetch=window.fetch;
window.fetch=(url,options={})=>{
 if(!String(url).startsWith('/interaction/thinking'))return realFetch(url,options);
 const u=new URL(url,location.href),turn=u.searchParams.get('turn'),part=Number(u.searchParams.get('part'));calls.push({turn,part});
 if(fail)return Promise.resolve({ok:false,json:async()=>({})});
 const rec=kept.get(turn);
 if(!rec)return Promise.resolve({ok:true,json:async()=>({version:1,turn,recorded:false})});
 return Promise.resolve({ok:true,json:async()=>({version:1,turn,recorded:true,models:rec.models,created_at:rec.at,may_be_summary:!!rec.summary,part,parts:rec.parts.length,text:rec.parts[part-1]})});
};
`

func TestAReplysThinkingIsShownOnlyWhenItsDetailsAreOpened(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + thinkingRig + `
run(async assert=>{
 kept.set('2',{models:'glm',at:'2026-10-05T10:00:00Z',parts:['First the ledger head.\nThen the two dates.']});
 kept.set('4',{models:'claude (may be a summary)',at:'2026-10-05T10:01:00Z',summary:true,parts:['Weighed the two readings.']});
 const p=pages([row('q1',1,'operator','first question'),row('a1',2,'resident','first answer'),row('q2',3,'operator','second question'),row('a2',4,'resident','second answer'),row('a3',5,'resident','third answer')],[row('t1',2,'resident','an answer not saved')]);
 draw(p);await settle();
 assert(calls.length===0,'thinking was read before any details were opened');
 assert(!inner.querySelector('.record-thinking'),'the stream shows thinking nobody asked for');
 await toggle('q1');
 assert(details('q1').open&&calls.length===0,'an operator message asked for thinking');
 await toggle('t1');
 assert(details('t1').open&&calls.length===0,'a reply that was not saved asked for thinking under a turn that is not its own');
 await toggle('a1');await until(()=>thinking('a1'));
 assert(calls.length===1&&calls[0].turn==='2'&&calls[0].part===1,'the reply did not ask for its own turn, once: '+JSON.stringify(calls));
 const box=thinking('a1');
 assert(!box.open,'thinking is not collapsed');
 assert(box.querySelector('summary').textContent==='Thinking','raw thinking is labelled '+box.querySelector('summary').textContent);
 assert(!box.querySelector('.thinking-note'),'raw thinking carries the note of a summary');
 assert(box.querySelector('pre').textContent==='First the ledger head.\nThen the two dates.','the text shown is not the record');
 assert(box.querySelector('.interaction-meta').textContent==='glm · 2026-10-05T10:00:00Z','the model and time are not named: '+box.querySelector('.interaction-meta').textContent);
 assert(!box.querySelector('.detail-read'),'a single part offers more');
 await toggle('a1');await toggle('a1');draw(p);await settle();
 assert(details('a1').open&&calls.length===1&&thinking('a1'),'closing, reopening or redrawing asked again or lost the thinking');
 await toggle('a2');await until(()=>thinking('a2'));
 assert(thinking('a2').querySelector('summary').textContent==='Thinking · may be a summary','a provider\'s version is not marked');
 assert(thinking('a2').querySelector('.thinking-note')?.textContent.includes('may leave things out'),'the mark is not explained');
 assert(calls.length===2&&calls[1].turn==='4','the second reply asked for another turn');
 await toggle('a3');await until(()=>calls.length===3);await settle();
 assert(calls[2].turn==='5'&&!thinking('a3'),'a reply with no thinking recorded shows a thinking box');
 assert(inner.querySelectorAll('.record-thinking').length===2,'thinking appeared somewhere it was not asked for');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestLongThinkingReadsInPartsAndAFailedReadIsAskedAgain(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + thinkingRig + `
run(async assert=>{
 kept.set('2',{models:'glm',at:'t',parts:['part one. ','part two. ','part three.']});
 const p=pages([row('q1',1,'operator','question'),row('a1',2,'resident','answer')]);draw(p);await settle();
 fail=true;await toggle('a1');await until(()=>calls.length===1);await settle();
 assert(!thinking('a1'),'a failed read showed a thinking box');
 fail=false;await toggle('a1');await toggle('a1');await until(()=>thinking('a1'));
 assert(calls.length===2,'a failed read was not asked again at the next opening: '+calls.length);
 const box=thinking('a1'),more=()=>box.querySelector('.detail-read button');
 assert(more()?.textContent==='Read more (part 2 of 3)','the next part is not offered: '+more()?.textContent);
 assert(box.querySelector('pre').textContent==='part one. ','more than the first part was read unasked');
 more().click();await until(()=>box.querySelector('pre').textContent==='part one. part two. ');
 assert(more()?.textContent==='Read more (part 3 of 3)','the last part is not offered');
 fail=true;more().click();await until(()=>box.querySelector('.detail-read span').textContent!==''&&!more().disabled);
 assert(box.querySelector('pre').textContent==='part one. part two. ','a failed part changed the text');
 fail=false;more().click();await until(()=>box.querySelector('pre').textContent==='part one. part two. part three.');
 assert(!box.querySelector('.detail-read'),'the reader stayed after the last part');
 assert(calls.filter(c=>c.part===1).length===2&&calls.at(-1).part===3,'parts were asked out of order: '+JSON.stringify(calls));
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}
