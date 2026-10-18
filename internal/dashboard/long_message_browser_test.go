//go:build !windows

package dashboard

import "testing"

const longMessageRig = `
import {S} from '/state.js';
import {renderInteractionPages} from '/views/chat.js';
import {run,until} from '/__harness.js';
S.view='chat';S.stats={name:'Fixture'};
const t=document.getElementById('thread'),inner=document.getElementById('thread-inner');
const data=new Map(),calls=[];let pending=[],delay=false,bad=false,active=0,peak=0,chunkSize=997;
const long=(id,text,role='resident',source='recorded')=>{
 const record={id,kind:'message',role,content:text};const bytes=new TextEncoder().encode(JSON.stringify(record));data.set(id,bytes);
 return {...record,content:'',sequence:String(data.size),details:{},content_ref:{id,source,incarnation:source==='recorded'?'disk':'memory',sha256:id.padEnd(64,'a').slice(0,64),bytes:bytes.length}};
};
const plain=(id,text)=>({id,sequence:'1',kind:'message',role:'operator',content:text,details:{}});
const pages=(recorded,transient=[])=>new Map([['recorded',{identity:'fixture',incarnation:'disk',rows:recorded}],['transient',{identity:'fixture',incarnation:'memory',rows:transient}]]);
const draw=(p,position='',reading=null)=>renderInteractionPages(p,()=>{},false,position,reading);
const body=id=>document.querySelector('[data-turn-id="'+id+'"] .record-message > .body');
const owner=id=>document.querySelector('[data-turn-id="'+id+'"]');
const wheel=delta=>t.dispatchEvent(new WheelEvent('wheel',{deltaY:delta,bubbles:true}));
const realFetch=window.fetch;
window.fetch=(url,options={})=>{
 if(!String(url).startsWith('/interaction/detail'))return realFetch(url,options);
 const u=new URL(url,location.href),id=u.searchParams.get('id'),offset=Number(u.searchParams.get('offset'));calls.push({id,offset});active++;peak=Math.max(peak,active);
 return new Promise((resolve,reject)=>{
  let done=false;const finish=fn=>{if(done)return;done=true;active--;fn();};
  const deliver=()=>finish(()=>{const raw=data.get(id),bytes=raw.slice(offset,offset+chunkSize);resolve({ok:true,json:async()=>({version:1,id,source:u.searchParams.get('source'),offset,incarnation:u.searchParams.get('incarnation'),sha256:bad?'wrong':u.searchParams.get('sha256'),size:raw.length,bytes:bytes.length,data_b64:btoa(String.fromCharCode(...bytes)),eof:offset+bytes.length===raw.length})});});
  options.signal?.addEventListener('abort',()=>finish(()=>reject(new DOMException('Paused','AbortError'))),{once:true});
  if(delay)pending.push(deliver);else deliver();
 });
};
const words='A conversation should be readable. 日本語 🌱 '.repeat(240);
`

func TestVisibleLongConversationLoadsInlineWithoutOpeningToolDetails(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + longMessageRig + `
run(async assert=>{
 const a=long('operator',words,'operator'),b=long('resident',words+' resident');
 const tool={...long('tool','private tool details','system'),kind:'tool_call',turn_id:'turn',details:{tool:'read'}};
 const toolBytes=new TextEncoder().encode(JSON.stringify({...tool,content:'private tool details',content_ref:undefined}));data.set('tool',toolBytes);tool.content_ref.bytes=toolBytes.length;
 const p=pages([a,plain('spacer','Already visible conversation\n'.repeat(50)),b,tool]);draw(p,'latest');
 await until(()=>body('resident')?.textContent===words+' resident');
 assert(body('resident').textContent===words+' resident','visible resident reply still requires a click');
 assert(!inner.textContent.includes('Long message, kept in full'),'transport placeholder is still shown');
 assert(!calls.some(c=>c.id==='tool'),'conversation hydration fetched tool details');
 assert(!inner.querySelector('.receipt').open,'conversation hydration expanded a receipt');
 assert(peak===1,'visible messages opened parallel automatic reads');
 const copy=owner('resident').querySelector('.copyb');assert(!copy.hidden&&copy.dataset.copyText===words+' resident','loaded reply is not copyable as ordinary speech');
 const before=calls.length;draw(p);await new Promise(r=>requestAnimationFrame(r));
 assert(body('resident').textContent===words+' resident'&&calls.length===before,'ordinary redraw lost or refetched loaded text');
 assert(body('resident').scrollWidth<=body('resident').clientWidth+1,'inline reply overflows horizontally');
 assert(getComputedStyle(body('resident')).overflowY!=='auto','reply has a nested scrolling pane');
 assert(t.scrollHeight-t.clientHeight-t.scrollTop<2,'following was lost when full text arrived');
 assert(!calls.some(c=>c.id==='operator'),'offscreen earlier message was fetched eagerly');
 inner.querySelector('.receipt > summary').click();inner.querySelector('.receipt-all > summary').click();
 const toolRow=owner('tool');toolRow.querySelector('.tool-ev > summary').click();toolRow.querySelector('.detail-read button').click();
 await until(()=>toolRow.querySelector('.detail-read pre'));
 assert(toolRow.querySelector('.detail-read pre')?.textContent.includes('private tool details'),'explicit tool-detail reading no longer works');
 t.scrollTop=0;wheel(-80);await until(()=>body('operator').textContent===words);
 assert(body('operator').textContent===words,'scrolling to the operator message did not load it');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestLongConversationLoadYieldsToVisibilityAndPreservesReading(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + longMessageRig + `
run(async assert=>{
 let hidden=true;Object.defineProperty(document,'visibilityState',{configurable:true,get:()=>hidden?'hidden':'visible'});
 const a=long('held',words);const p=pages([plain('intro','Earlier words\n'.repeat(15)),a]);
 draw(p);assert(calls.length===0,'hidden page fetched text');hidden=false;
 delay=true;draw(p,'latest');await until(()=>calls.length>0);assert(calls.length===1,'visible message did not begin one read');
 S.view='settings';S.refreshMessageText?.();await until(()=>active===0);assert(active===0,'leaving Chat did not cancel its read');
 const count=calls.length;pending.splice(0).forEach(f=>f());await Promise.resolve();assert(calls.length===count,'hidden completion advanced the read');
 S.view='chat';delay=false;draw(p,'latest');t.scrollTop-=40;wheel(-40);
 const anchor=owner('intro'),before=anchor.getBoundingClientRect().top;await until(()=>body('held').textContent===words);
 assert(body('held').textContent===words,'returning to Chat did not resume the paused message');
 assert(Math.abs(anchor.getBoundingClientRect().top-before)<1&&!S.interactionFollowing(),'full text pulled the reader down');
 const stale=long('stale',words);delay=true;draw(pages([stale]),'latest');await until(()=>active===1);
 hidden=true;document.dispatchEvent(new Event('visibilitychange'));await until(()=>active===0);
 const hiddenCalls=calls.length;pending.splice(0).forEach(f=>f());await Promise.resolve();assert(calls.length===hiddenCalls,'hidden tab advanced a partial read');
 hidden=false;document.dispatchEvent(new Event('visibilitychange'));await until(()=>active===1);
 draw(pages([plain('replacement','A new source window')]),'latest');await until(()=>active===0);pending.splice(0).forEach(f=>f());
 assert(!inner.textContent.includes(words)&&body('replacement').textContent==='A new source window','retired read overwrote a replacement window');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestLongConversationFailureIsExplicitAndRetryIsUserDriven(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + longMessageRig + `
run(async assert=>{
 const a=long('broken',words);bad=true;const p=pages([a]);draw(p,'latest');
 await until(()=>owner('broken').querySelector('.message-load button:not([hidden])'));
 const retry=owner('broken').querySelector('.message-load button:not([hidden])');assert(retry,'failed automatic read has no retry');
 assert(!body('broken').textContent&&!inner.textContent.includes('data_b64'),'failed read rendered unverified text or raw JSON');
 const count=calls.length;draw(p);await new Promise(r=>requestAnimationFrame(r));assert(calls.length===count,'failed read retried itself on redraw');
 bad=false;retry.click();await until(()=>body('broken').textContent===words);assert(body('broken').textContent===words,'explicit retry did not recover');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestLongConversationReferenceChangeDiscardsLateText(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + longMessageRig + `
run(async assert=>{
 delay=true;const a=long('same','Old text');draw(pages([a]),'latest');await until(()=>active===1);
 const b=long('same','New text');b.content_ref.sha256='b'.repeat(64);draw(pages([b]),'latest');
 await until(()=>calls.length===2);delay=false;pending.splice(0).forEach(f=>f());
 await until(()=>body('same').textContent==='New text');
 assert(body('same').textContent==='New text','retired reference won over the current message');
 const copy=owner('same').querySelector('.copyb');assert(copy.dataset.copyText==='New text','copy retained retired text');
 draw(pages([plain('same','Now inline')]));await Promise.resolve();
 assert(body('same').textContent==='Now inline'&&copy.dataset.copyText==='Now inline','inline replacement retained fetched text');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestLongConversationReadCeilingAndDeadlineAreExplicit(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + longMessageRig + `
run(async assert=>{
 const huge=long('huge','too big');huge.content_ref.bytes=16*1024*1024+1;draw(pages([huge]),'latest');
 await until(()=>owner('huge').querySelector('.message-load button:not([hidden])'));
 assert(calls.length===0&&owner('huge').querySelector('.message-load button:not([hidden])'),'oversize automatic read was not bounded and explained');
 const realTimeout=window.setTimeout;let expire;
 window.setTimeout=(fn,ms,...args)=>ms===30000?(expire=fn,-1):realTimeout(fn,ms,...args);
 try {
  delay=true;draw(pages([long('slow',words)]),'latest');await until(()=>active===1&&expire);
  expire();await until(()=>owner('slow').querySelector('.message-load button:not([hidden])'));
  assert(active===0&&owner('slow').querySelector('.message-load').textContent.includes('too long'),'stalled read did not terminate with a retry route');
 } finally {window.setTimeout=realTimeout;}
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestLongConversationReloadRestoresAfterTextAndYieldsToInput(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		name, flag := "restore", "false"
		if cancel {
			name, flag = "new_input", "true"
		}
		t.Run(name, func(t *testing.T) {
			page := quietChatMarkup + `<script type="module">` + longMessageRig + `
const cancel=` + flag + `;
run(async assert=>{
 const again=sessionStorage.getItem('long-reload')==='1',p=pages([long('keep',words.repeat(3))]);
 if(!again){
  draw(p,'latest');await until(()=>body('keep').textContent===words.repeat(3));
  assert(body('keep').textContent===words.repeat(3),'initial message did not load');
  t.scrollTop=1200;wheel(-80);const reading=S.interactionBookmark();
  assert(reading.offset < -1000,'fixture did not read inside a long message');
  sessionStorage.setItem('long-reading',JSON.stringify(reading));sessionStorage.setItem('long-reload','1');location.reload();return new Promise(()=>{});
 }
 const reading=JSON.parse(sessionStorage.getItem('long-reading'));delay=true;draw(p,'restore',reading);
 await until(()=>active===1);assert(active===1,'restored message was not selected for reading');
 assert(S.interactionBookmark().offset===reading.offset,'unfinished hydration overwrote the reload bookmark');
 if(cancel)wheel(-80);
 delay=false;pending.splice(0).forEach(f=>f());await until(()=>body('keep').textContent===words.repeat(3));
 assert(body('keep').textContent===words.repeat(3),'restored text did not load');
 const offset=owner('keep').getBoundingClientRect().top-t.getBoundingClientRect().top;
 if(cancel)assert(Math.abs(offset)<2,'delayed hydration overrode newer input: '+offset);
 else assert(Math.abs(offset-reading.offset)<2,'reload lost the offset inside the message: '+offset+' vs '+reading.offset);
 assert(!sessionStorage.getItem('long-reading').includes('A conversation'),'bookmark persisted conversation text');
});</script>`
			runPageInEngines(t, page, chatModuleRig(t))
		})
	}
}

func TestLongConversationExpandedBytesAreBoundedByTheMountedWindow(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + longMessageRig + `
run(async assert=>{
 chunkSize=16384;
 const large=id=>{
  const r=long(id,'Readable '+id);const raw=new TextEncoder().encode(JSON.stringify({id,kind:r.kind,role:r.role,content:'Readable '+id,details:{padding:'x'.repeat(9*1024*1024)}}));
  data.set(id,raw);r.content_ref.bytes=raw.length;return r;
 };
 const a=large('first'),b=large('second');draw(pages([a,b]),'latest');
 await until(()=>body('first').textContent==='Readable first',15000);
 await until(()=>owner('second').querySelector('.message-load button:not([hidden])'));
 assert(body('first').textContent==='Readable first','first bounded record did not load');
 assert(!calls.some(c=>c.id==='second')&&owner('second').querySelector('.message-load button:not([hidden])'),'expanded-record budget did not stop the second read');
 draw(pages([b]),'latest');owner('second').querySelector('.message-load button').click();
 await until(()=>body('second').textContent==='Readable second',15000);
 assert(body('second').textContent==='Readable second','retiring the old window did not release its byte allowance');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

func TestLongConversationReferenceRefreshRestoresItsReadingOffset(t *testing.T) {
	page := quietChatMarkup + `<script type="module">` + longMessageRig + `
run(async assert=>{
 const a=long('updated',words.repeat(3));draw(pages([a]),'latest');await until(()=>body('updated').textContent===words.repeat(3));
 t.scrollTop=1200;wheel(-80);const before=owner('updated').getBoundingClientRect().top-t.getBoundingClientRect().top;
 const b=long('updated',words.repeat(3)+' Updated ending.');b.content_ref.sha256='b'.repeat(64);delay=true;draw(pages([b]));
 await until(()=>active===1);
 assert(Math.abs(S.interactionBookmark().offset-before)<1,'reference refresh lost the offset before its text arrived');
 delay=false;pending.splice(0).forEach(f=>f());await until(()=>body('updated').textContent.endsWith(' Updated ending.'));
 const after=owner('updated').getBoundingClientRect().top-t.getBoundingClientRect().top;
 assert(body('updated').textContent.endsWith(' Updated ending.')&&Math.abs(after-before)<1,'reference refresh moved the reader inside the message');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}
