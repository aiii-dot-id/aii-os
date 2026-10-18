//go:build !windows

package dashboard

import "testing"

func TestAReplyShowsFilesOfTheIdentitysFolderAndNothingElse(t *testing.T) {
	page := `<!doctype html><link rel="stylesheet" href="/layout.css">
<div id="thread" style="height:600px;width:420px;overflow:auto"><div id="thread-inner"></div></div>
<div id="composer-wrap"><div id="attach-chips" hidden></div><textarea id="msg-input"></textarea><button id="attach-btn">+</button><input type="file" id="attach-input" multiple hidden><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import {S} from '/state.js';import {renderInteractionPages} from '/views/chat.js';import {shownLine} from '/views/attach.js';import {run} from '/__harness.js';
run(async assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;
 const at=s=>'2026-10-12T10:'+s+'Z';
 const shows='Here is the proof.\n![the page proof](projects/reports/proof.png)\n[report.docx](projects/reports/report.docx)\n[second](uploads/report (2).docx)\n![elsewhere](https://elsewhere.example/a.png)\n[a link](//elsewhere.example/x)\n[data](data:text/html,x)\nmid-sentence ![no](projects/no.png) stays text\n![on a drive](C:\\Users\\someone\\proof.png)';
 const rows=[
  {id:'q',sequence:'1',kind:'message',role:'operator',content:'read this\n![mine](projects/typed-by-me.png)',turn_id:'t1',recorded_at:at('00:00'),details:{}},
  {id:'f1',related_id:'q',sequence:'2',kind:'notice',role:'system',turn_id:'t1',recorded_at:at('00:01'),content:'uploads/letter.docx',details:{reason_code:'attached'}},
  {id:'f2',related_id:'q',sequence:'3',kind:'notice',role:'system',turn_id:'t1',recorded_at:at('00:02'),content:'uploads/logo.png',details:{reason_code:'attached'}},
  {id:'a',sequence:'4',kind:'message',role:'resident',content:shows,turn_id:'t1',recorded_at:at('01:00'),details:{}},
  {id:'p1',related_id:'q',sequence:'5',kind:'notice',role:'system',turn_id:'t1',recorded_at:at('01:01'),content:'uploads/logo.png',details:{reason_code:'shown'}},
  {id:'p2',related_id:'q',sequence:'6',kind:'notice',role:'system',turn_id:'t1',recorded_at:at('01:02'),content:'uploads/big.png, its 9000000 bytes are past this model\'s bound of 5242880 for one picture',details:{reason_code:'not_shown'}},
  {id:'p3',sequence:'7',kind:'notice',role:'system',turn_id:'t1',recorded_at:at('01:03'),content:'the model\'s provider refused the request that carried this turn\'s picture(s) (HTTP 400); it was sent again without them, and the model was not shown them. The provider said: fixture.',details:{reason_code:'pictures_refused'}},
 ];
 const p={identity:'fixture',incarnation:'disk',rows,has_older:false,has_newer:false};
 const pages=new Map([['recorded',p],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]);
 renderInteractionPages(pages,()=>{},false);
 const inner=document.getElementById('thread-inner');
 const reply=inner.querySelector('[data-turn-id="a"] .body');
 const sources=Array.from(inner.querySelectorAll('img,a')).map(e=>e.getAttribute('src')||e.getAttribute('href'));
 assert(sources.every(s=>s.startsWith('/w?p=')),'the page drew something that is not a file of the folder: '+sources.join(' '));
 assert(!inner.innerHTML.includes('elsewhere.example/a.png"')&&!sources.some(s=>s.includes('elsewhere')||s.includes('data:')),'an address was drawn');
 const drawn=Array.from(reply.querySelectorAll('figure.shown-image,figure .shown-missing')).length;
 assert(reply.querySelectorAll('figure').length===2,'images drawn in the reply: '+reply.querySelectorAll('figure').length+' (want the page proof and the one on a drive) '+drawn);
 const first=reply.querySelector('figure');
 assert(first.textContent.includes('the page proof'),'the image lost its caption: '+first.textContent);
 const chip=reply.querySelector('a.file-chip');
 assert(chip&&chip.getAttribute('href')==='/w?p='+encodeURIComponent('projects/reports/report.docx')+'&download=1'&&chip.textContent==='report.docx','the document is not a download chip: '+(chip&&chip.outerHTML));
 const realFetch=window.fetch;
 window.fetch=async()=>({ok:false,status:415,text:async()=>'.py is not a kind of file handed over from the chat: it stays in the identity\'s folder at tools/run.py.'});
 chip.click();await new Promise(r=>setTimeout(r,50));
 assert(chip.nextElementSibling&&chip.nextElementSibling.textContent.includes('stays in the identity\'s folder at tools/run.py'),'a press that handed over nothing gave no reason: '+(chip.nextElementSibling&&chip.nextElementSibling.outerHTML));
 window.fetch=async()=>({ok:false,status:404,text:async()=>'404 page not found'});
 chip.click();await new Promise(r=>setTimeout(r,50));
 assert(chip.nextElementSibling.textContent.trim()==='not available at projects/reports/report.docx','a file that is gone: '+chip.nextElementSibling.textContent);
 window.fetch=realFetch;
 const second=Array.from(reply.querySelectorAll('a.file-chip')).find(a=>a.textContent==='second');
 assert(second&&second.getAttribute('href')==='/w?p='+encodeURIComponent('uploads/report (2).docx')+'&download=1','a path that holds parentheses is not a chip: '+reply.innerHTML);
 for(const kept of ['Here is the proof.','![elsewhere](https://elsewhere.example/a.png)','[a link](//elsewhere.example/x)','[data](data:text/html,x)','mid-sentence ![no](projects/no.png) stays text'])
  assert(reply.textContent.includes(kept),'the reply lost text it should keep as typed: '+kept);
 const mine=inner.querySelector('[data-turn-id="q"] .body');
 assert(!mine.querySelector('img,a')&&mine.textContent.includes('![mine](projects/typed-by-me.png)'),'the operator\'s own words were drawn as a file');
 const names=Array.from(inner.querySelectorAll('.tname')).map(e=>e.textContent);
 assert(names.includes('shown to the model: uploads/logo.png'),'the row that a picture was shown is not drawn so: '+names.join(' | '));
 assert(names.some(n=>n.startsWith('not shown to the model: uploads/big.png, its 9000000 bytes')),'the row that a picture was not shown is not drawn so: '+names.join(' | '));
 const cut=names.find(n=>n.startsWith('the model\'s provider refused the request'));
 assert(cut&&cut.endsWith(' …')&&cut.length<=102,'a notice cut to its line does not say it is cut: '+cut);
 const attached=Array.from(inner.querySelectorAll('.attached-file'));
 assert(attached.length===2,'files that came with the message: '+attached.length);
 assert(attached[0].querySelector('a.file-chip').getAttribute('href')==='/w?p='+encodeURIComponent('uploads/letter.docx')+'&download=1','the attached document is not its chip');
 assert(!attached[0].querySelector('img'),'a document was given a picture');
 assert(shownLine('![x](projects/a.png)').path==='projects/a.png'&&shownLine('  [n](uploads/b.docx)  ').image===false&&!shownLine('![x](http://h/a.png)')&&!shownLine('![x](javascript:alert(1))')&&!shownLine('text ![x](a.png)')&&shownLine('[a](uploads/a(1).txt)').path==='uploads/a(1).txt'&&shownLine('![p](uploads/proof (2).png)').path==='uploads/proof (2).png'&&!shownLine('[a](x) and [b](y)')&&!shownLine('[a](x) then words (aside)')&&shownLine('[n](uploads/a).txt)').path==='uploads/a).txt'&&shownLine('[n](uploads/a(.txt)').path==='uploads/a(.txt','shownLine reads a line wrongly');
 const figure=reply.querySelector('figure');
 renderInteractionPages(pages,()=>{},false);
 assert(reply.querySelector('figure')===figure,'an unchanged reply was rebuilt');
 rows[3]={...rows[3],content:'only words now'};
 renderInteractionPages(new Map([['recorded',{...p,rows}],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]),()=>{},false);
 const again=inner.querySelector('[data-turn-id="a"] .body');
 assert(!again.querySelector('figure,a')&&again.textContent==='only words now','a reply that shows nothing kept a file drawn: '+again.innerHTML);
});</script>`
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	modules := chatModuleRig(t)
	modules["/layout.css"] = css
	runPageInEngines(t, page, modules)
}

func TestThePlusTakesAFileAndTheMessageCarriesIt(t *testing.T) {
	page := `<!doctype html>
<div id="thread"><div id="thread-inner"></div></div>
<div id="composer-wrap"><div id="attach-chips" hidden></div><textarea id="msg-input"></textarea><button id="attach-btn">+</button><input type="file" id="attach-input" multiple hidden><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import {S} from '/state.js';import {sendChat} from '/views/chat.js';import {takeFiles} from '/views/attach.js';import {frames} from '/ws.js';import {toasts} from '/app.js';import {run} from '/__harness.js';
run(async assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;
 const asked=[];
 window.fetch=async(url,init)=>{asked.push([url,init&&init.method]);
  if(String(url).includes('tool.exe'))return new Response('.exe is not a kind of file taken here. Taken: .docx .png',{status:415});
  if(String(url).includes('proof.png'))return new Response(JSON.stringify({path:'uploads/proof.png',name:'proof.png',bytes:9,image:true}),{status:200,headers:{'Content-Type':'application/json'}});
  if(String(url).includes('kept.txt'))return new Response(JSON.stringify({path:'uploads/kept (2).txt',name:'kept (2).txt',bytes:5,image:false}),{status:200,headers:{'Content-Type':'application/json'}});
  return new Response(JSON.stringify({path:'uploads/notes.txt',name:'notes.txt',bytes:5,image:false}),{status:200,headers:{'Content-Type':'application/json'}});};
 const chips=()=>Array.from(document.querySelectorAll('#attach-chips .attach-chip'));
 const until=async(cond,what)=>{for(let i=0;i<400;i++){if(cond())return;await new Promise(r=>requestAnimationFrame(r));}assert(false,'never: '+what);};
 assert(document.getElementById('attach-chips').hidden,'chips shown before any file');
 takeFiles([new File(['hello'],'notes.txt',{type:'text/plain'})]);
 await until(()=>chips().length===1&&chips()[0].textContent.includes('5 bytes'),'the chip of a taken file');
 assert(asked.length===1&&asked[0][0]==='/upload?name=notes.txt'&&asked[0][1]==='POST','the file was not sent to /upload: '+JSON.stringify(asked));
 assert(sendChat('')===false&&frames.length===0,'a message with a file and no words was sent');
 assert(toasts.some(m=>m.includes('Say what to do with the file')),'no reason was said: '+toasts.join('|'));
 assert(sendChat('read this')===true,'the message was not sent');
 assert(JSON.stringify(frames[0])===JSON.stringify({type:'chat',message:'read this',attachments:['uploads/notes.txt']}),'the frame: '+JSON.stringify(frames[0]));
 assert(chips().length===0&&document.getElementById('attach-chips').hidden,'the chips stayed after the message left');
 assert(sendChat('and no file now')===true&&!('attachments' in frames[1]),'a message with no file carries an attachments member: '+JSON.stringify(frames[1]));
 takeFiles([new File(['hello'],'kept.txt',{type:'text/plain'})]);
 await until(()=>chips().length===1&&chips()[0].textContent.includes('kept as kept (2).txt, not kept.txt'),'the chip of a file kept under another name: '+(chips()[0]&&chips()[0].textContent));
 chips()[0].querySelector('.attach-drop').click();
 takeFiles([new File(['\x89PNG....'],'proof.png',{type:'image/png'})]);
 await until(()=>chips().length===1&&chips()[0].textContent.includes('9 bytes'),'the image\'s chip');
 assert(chips()[0].textContent.includes('to the folder only'),'with nobody having said the model takes images, the chip does not say the image stays in the folder: '+chips()[0].textContent);
 chips()[0].querySelector('.attach-drop').click();
 S.config={llm:{images_shown:'yes'}};
 takeFiles([new File(['\x89PNG....'],'proof.png',{type:'image/png'})]);
 await until(()=>chips().length===1&&chips()[0].textContent.includes('the model will be shown it'),'the chip of an image the model will be shown');
 S.thinking=true;S.chipsRedraw();
 assert(chips()[0].textContent.includes('to the folder only while a turn is running'),'a picture waiting while a turn runs still says the model will be shown it: '+chips()[0].textContent);
 S.thinking=false;S.chipsRedraw();
 assert(chips()[0].textContent.includes('the model will be shown it'),'the chip did not follow the turn ending: '+chips()[0].textContent);
 S.config={llm:{images_shown:'yes',image_max_bytes:8,images_max_bytes:1<<20,images_max_count:20,image_max_side:8000}};
 takeFiles([]);chips()[0].querySelector('.attach-drop').click();
 takeFiles([new File(['\x89PNG....'],'proof.png',{type:'image/png'})]);
 await until(()=>chips().length===1&&chips()[0].textContent.includes('to the folder only: larger than one picture this model is shown (8 bytes)'),'the chip of a picture past the model\'s bound: '+(chips()[0]&&chips()[0].textContent));
 chips()[0].querySelector('.attach-drop').click();
 S.config={llm:{images_shown:'yes',image_max_bytes:1<<20,images_max_bytes:1<<20,images_max_count:1,image_max_side:8000}};
 takeFiles([new File(['\x89PNG....'],'proof.png',{type:'image/png'}),new File(['\x89PNG....'],'proof.png',{type:'image/png'})]);
 await until(()=>chips().length===2&&chips()[0].textContent.includes('the model will be shown it')&&chips()[1].textContent.includes('already has the 1 pictures one request takes'),'the second picture past the count one request takes: '+chips().map(c=>c.textContent).join(' | '));
 chips()[1].querySelector('.attach-drop').click();
 chips()[0].querySelector('.attach-drop').click();
 S.config={llm:{images_shown:'yes'}};
 takeFiles([new File(['MZ'],'tool.exe')]);
 await until(()=>chips().length===1&&chips()[0].classList.contains('error'),'the refused file\'s chip');
 assert(chips()[0].textContent.includes('not a kind of file taken here'),'the server\'s sentence is not on the chip: '+chips()[0].textContent);
 assert(sendChat('with the refused one')===false&&frames.length===2,'a message left with a file that was not taken');
 chips()[0].querySelector('.attach-drop').click();
 assert(chips().length===0,'the chip was not removed');
 assert(sendChat('now it goes')===true&&frames.length===3,'the message was still held after the chip was removed');
});</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}
