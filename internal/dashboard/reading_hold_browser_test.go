//go:build !windows

package dashboard

import "testing"

// THE READER'S ROW STAYS WHERE IT WAS AFTER A RELOAD, THOUGH A DISCLOSURE ABOVE
// IT FILLS LATER. A reply's Thinking, opened before the reload, is opened again as
// the reading is restored and reads its text afterwards; the text pushes every row
// below it down. The row the reader was at is put back at its offset when that
// happens (and at any later growth), until the person's own hand moves. Seen on a
// real host: Chat came back 26 px short at 1280, five rows short at 390.
func TestTheReadersRowIsHeldWhileADisclosureAboveItFills(t *testing.T) {
	page := quietChatMarkup + `<script type="module">
import {S} from '/state.js';
import {renderInteractionPages} from '/views/chat.js';
import {run,until} from '/__harness.js';
S.view='chat';S.stats={name:'Fixture'};S.interactionFollowing=()=>false;
const t=document.getElementById('thread'),inner=document.getElementById('thread-inner');
const rows=[];for(let i=1;i<=40;i++)rows.push({id:'r'+i,sequence:String(i),kind:'message',role:i%2?'operator':'resident',content:'Row '+i+' of the conversation.',details:{},turn_id:'turn'+i});
const pages=new Map([['recorded',{identity:'fixture',incarnation:'disk',rows}],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]);
const releases=[];const realFetch=window.fetch;
window.fetch=(url,options={})=>{
 if(!String(url).startsWith('/interaction/thinking'))return realFetch(url,options);
 const turn=new URL(url,location.href).searchParams.get('turn');
 return new Promise(resolve=>{releases.push(()=>resolve({ok:true,json:async()=>({version:1,turn,recorded:true,text:'a line of thinking\n'.repeat(40),part:1,parts:1,models:'m',created_at:'2026-11-05T00:00:00Z'})}));});
};
run(async assert=>{
 renderInteractionPages(pages,()=>{},false,'latest');
 const keyOf=id=>document.querySelector('[data-turn-id="'+id+'"]')?.dataset.interactionKey;
 const reading={key:keyOf('r30'),offset:-10,following:false,open:[{key:keyOf('r4'),indices:[0]}]};
 assert(reading.key&&reading.open[0].key,'the rows have no keys');
 const offset=()=>{const a=document.querySelector('[data-turn-id="r30"]');return Math.round(a.getBoundingClientRect().top-t.getBoundingClientRect().top);};
 renderInteractionPages(pages,()=>{},false,'restore',reading);
 await until(()=>releases.length===1,2000);
 assert(releases.length===1,'the opened Thinking was not asked for');
 assert(Math.abs(offset()-(-10))<=1,'before the thinking arrived the row is not at its offset: '+offset());
 const before=inner.scrollHeight;
 releases[0]();
 await until(()=>inner.scrollHeight>before+200,3000);
 // waits by design: the row held after the fill is the claim, past its layout and the hold's apply.
 await new Promise(r=>setTimeout(r,80));
 assert(inner.scrollHeight>before+200,'the thinking did not fill: '+inner.scrollHeight+' from '+before);
 assert(Math.abs(offset()-(-10))<=1,'the thinking above pushed the reader\'s row: now at '+offset()+', was at -10');
 // A SCRIPTED WHEEL IS NOT THE PERSON'S HAND: the row is still held through more
 // growth above. THEIR OWN WHEEL ENDS THE HOLD (a trusted one, where the engine's
 // driver gives it: WebKit), and growth above afterwards does not pull them back.
 const grow=()=>{const pad=document.createElement('div');pad.style.height='300px';document.querySelector('[data-turn-id="r5"]').append(pad);};
 window.dispatchEvent(new WheelEvent('wheel',{deltaY:0,bubbles:true}));
 grow();
 // waits by design: staying held is the claim, past the growth's layout and the hold's next apply.
 await new Promise(r=>setTimeout(r,120));
 assert(Math.abs(offset()-(-10))<=1,'a scripted wheel ended the hold: the row is at '+offset());
 if(window.__wheel){
  const b=t.getBoundingClientRect();
  await window.__wheel(b.left+b.width/2,b.top+b.height/2,-200);
  await until(()=>Math.abs(offset()-(-10))>150,2000);
  const mine=offset();grow();
  // waits by design: not being pulled back is the claim, past the growth's layout and any apply.
  await new Promise(r=>setTimeout(r,200));
  assert(Math.abs(mine-(-10))>150&&Math.abs(offset()-(-10))>150,'after the person\'s own scroll the old bookmark pulled the row back to it: at '+offset()+', from '+mine);
 }
 // THE HOLD ENDS WHEN THE READING IS GIVEN UP (the thread does that when the person
 // jumps or reads on; the person's own wheel ends it too, through keep.js, which a
 // page cannot fake: only a trusted event counts). After it, a later growth above
 // is theirs to see: the old offset does not pull them back.
 S.cancelReadingRestore();
 const second=document.querySelector('[data-turn-id="r8"] details');
 assert(second&&!second.open,'the second reply has no closed Thinking to open');
 second.open=true;
 await until(()=>releases.length===2,2000);
 assert(releases.length===2,'the second Thinking was not asked for');
 const grown=inner.scrollHeight;
 releases[1]();
 await until(()=>inner.scrollHeight>grown+200,3000);
 // waits by design: not being pulled back is the claim, past the growth's layout and any apply.
 await new Promise(r=>setTimeout(r,80));
 assert(offset()>200,'after the reading was given up the old offset still pulled the reader back: at '+offset());
});</script>`
	// The page's own sheet: it turns the browser's anchoring off in the thread,
	// so that the thread's hold is the one hand on its scroll (and the control
	// fails for that reason, not for the browser's help).
	modules := chatModuleRig(t)
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	modules["/layout.css"] = css
	runPageInEngines(t, page, modules)
}
