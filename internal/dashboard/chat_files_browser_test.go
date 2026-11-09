//go:build !windows

package dashboard

import "testing"

// A REPLY WITH A FILE KEEPS ITS LINES, AND CODE IS LITERAL. The words around a
// file line are drawn as every reply's are: headings, lists and fenced code as
// lines of their own, not their typed marks. A file or a picture written inside
// fenced code is an example: it stays as typed, and nothing is asked for. A
// fence that never closes is not a fence, as the words read it, so a file line
// after it is a file.
func TestAReplyWithAFileKeepsItsLinesAndCodeIsLiteral(t *testing.T) {
	page := `<!doctype html><link rel="stylesheet" href="/layout.css">
<div id="thread" style="height:600px;width:420px;overflow:auto"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import {S} from '/state.js';import {renderInteractionPages} from '/views/chat.js';import {run} from '/__harness.js';
run(async assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;
 const asked=[];const seen=window.fetch;window.fetch=(...a)=>{asked.push(String(a[0]));return seen(...a);};
 const EXAMPLE='![example](projects/example.png)\n[example.docx](projects/example.docx)';
 const FENCE='` + "```" + `';
 const withFile='# The report\n- first\n- second\nHere it is:\n[report.docx](projects/report.docx)\n1. one\n2. two\n'+FENCE+'\n'+EXAMPLE+'\n'+FENCE+'\nAnd **done**.';
 const onlyCode='Write it like this:\n'+FENCE+'\n'+EXAMPLE+'\n'+FENCE;
 const open='A fence left open:\n'+FENCE+'\n[real.docx](projects/real.docx)';
 const lines='[report.docx](projects/report.docx)\nPlease review.\n[a.docx](projects/a.docx)\n[b.docx](projects/b.docx)\nThanks.';
 const at=s=>'2026-10-12T10:'+s+'Z';
 const say=(id,content,n)=>({id,sequence:String(n),kind:'message',role:'resident',content,turn_id:'t'+n,recorded_at:at('0'+n+':00'),details:{}});
 const pages=new Map([['recorded',{identity:'fixture',incarnation:'disk',rows:[say('a',withFile,1),say('b',onlyCode,2),say('c',open,3),say('d',lines,4)],has_older:false,has_newer:false}],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]);
 renderInteractionPages(pages,()=>{},false);
 const body=id=>document.querySelector('[data-turn-id="'+id+'"] .body');
 const a=body('a');
 assert(a.querySelector('.in-words__heading')&&a.querySelector('.in-words__heading').textContent==='The report','the heading around a file is not a line of its own: '+a.innerHTML);
 assert([...a.querySelectorAll('ul > li')].map(l=>l.textContent).join()==='first,second','the list around a file went back to its typed marks: '+a.innerHTML);
 assert([...a.querySelectorAll('ol > li')].map(l=>l.textContent).join()==='one,two','the numbered list around a file went back to its typed marks: '+a.innerHTML);
 assert(!/(^|\n)- first/.test(a.textContent),'a typed bullet still stands: '+a.textContent);
 assert(a.querySelector('strong')&&a.querySelector('strong').textContent==='done','the words after the file lost their marks: '+a.innerHTML);
 const chips=[...a.querySelectorAll('a.file-chip')].map(c=>c.textContent);
 assert(chips.join()==='report.docx','the real file is not the one chip: '+chips.join());
 const code=a.querySelector('pre.in-words__pre code');
 assert(code&&code.textContent===EXAMPLE,'the example in code is not as typed beside a real file: '+(code&&code.textContent));
 assert(!a.querySelector('figure,img'),'an example in code was drawn as a picture: '+a.innerHTML);
 const b=body('b');
 assert(!b.querySelector('a.file-chip,figure,img'),'an example in code became a file: '+b.innerHTML);
 assert(b.dataset.shown===undefined,'a reply with only an example in code was read as showing a file');
 assert((b.querySelector('pre.in-words__pre code')||{}).textContent===EXAMPLE,'the example in code is not as typed: '+b.innerHTML);
 assert([...body('c').querySelectorAll('a.file-chip')].map(c=>c.textContent).join()==='real.docx','a file after a fence that never closes is not a file: '+body('c').innerHTML);
 document.getElementById('thread').scrollTop=0;
 // A file is a line of its own: what follows it starts below it, and no line stands empty before it.
 const d=body('d'),[report,first,second]=d.querySelectorAll('a.file-chip');
 const rectOf=words=>{const walk=document.createTreeWalker(d,NodeFilter.SHOW_TEXT);for(let n;(n=walk.nextNode());){const at=n.data.indexOf(words);if(at>=0){const r=document.createRange();r.setStart(n,at);r.setEnd(n,at+words.length);return r.getBoundingClientRect();}}return null;};
 const review=rectOf('Please review.'),thanks=rectOf('Thanks.'),box=e=>e.getBoundingClientRect();
 assert(review&&review.top>=box(report).bottom-1,'the words after a file stand on its line: chip ends '+box(report).bottom+', words start '+(review&&review.top));
 assert(box(second).top>=box(first).bottom-1,'two files stand on one line: '+box(first).bottom+' then '+box(second).top);
 assert(thanks&&thanks.top>=box(second).bottom-1,'the words after the last file stand on its line');
 const beside=body('a').querySelector('a.file-chip');
 const before=document.createRange();before.selectNodeContents(body('a'));before.setEndBefore(beside.parentElement);
 const lastText=[...before.getClientRects()].filter(r=>r.height>0).at(-1);
 assert(lastText&&box(beside).top>=lastText.bottom-1,'a file stands on the line before it: '+box(beside).top+' after '+(lastText&&lastText.bottom));
 assert(lastText&&box(beside).top-lastText.bottom<parseFloat(getComputedStyle(body('a')).fontSize)/2,'an empty line stands before a file: '+(box(beside).top-lastText.bottom));
 // waits by design: that nothing is asked for takes time to show.
 await new Promise(r=>setTimeout(r,300));
 assert(!document.querySelector('[data-file*="example"]'),'a picture was waiting to be asked for from code');
 assert(!asked.some(u=>u.includes('example')),'an example in code was asked for: '+asked.join(' '));
});
</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

// A REPLY SHOWS FILES OF THE IDENTITY'S FOLDER, AND NOTHING ELSE. A line of
// the identity's reply that is only a markdown image or link whose target
// is a path is drawn from the page's own /w route: an image as an image, a
// document as a chip that downloads it. An address is text, a line that
// carries more than the file is text, and the same line typed by the
// operator is text: the page draws files the identity names, never an
// address a reply could carry data to. A file that came with a message is
// its chip beside the message.
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
 // A picture's address waits in data-file until it comes into view and is given a ticket.
 const sources=Array.from(inner.querySelectorAll('img,a')).map(e=>e.getAttribute('src')||e.dataset.file||e.getAttribute('href'));
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
 // A PDF's, a sound's or a video's chip says the same, from what the model takes and what this host sends of the kind.
 window.fetch=(f=>async(url,init)=>String(url).includes('brief.pdf')?new Response(JSON.stringify({path:'uploads/brief.pdf',name:'brief.pdf',bytes:9,image:false}),{status:200,headers:{'Content-Type':'application/json'}})
  :String(url).includes('note.ogg')?new Response(JSON.stringify({path:'uploads/note.ogg',name:'note.ogg',bytes:9,image:false}),{status:200,headers:{'Content-Type':'application/json'}}):f(url,init))(window.fetch);
 for(const [takes,text] of [[[],'to the folder only: nobody has said this model takes PDFs'],[[{kind:'pdf',state:'no',types:['application/pdf']}],'to the folder only: this model does not take PDFs'],
   [[{kind:'pdf',state:'yes',types:['application/pdf']}],'the model will be shown it'],[[{kind:'pdf',state:'yes',not_sent:true}],'to the folder only: this host does not send application/pdf in this provider\'s dialect']]){
  S.config={llm:{images_shown:'yes',capabilities:{takes}}};
  takeFiles([new File(['%PDF-1.7'],'brief.pdf',{type:'application/pdf'})]);
  await until(()=>chips().length===1&&chips()[0].textContent.includes(text),'the PDF\'s chip says '+text+': '+(chips()[0]&&chips()[0].textContent));
  chips()[0].querySelector('.attach-drop').click();
 }
 S.config={llm:{capabilities:{takes:[{kind:'audio',state:'yes',types:['audio/wav','audio/mpeg']}]}}};
 takeFiles([new File(['OggS'],'note.ogg',{type:'audio/ogg'})]);
 await until(()=>chips().length===1&&chips()[0].textContent.includes('this host does not send audio/ogg in this provider\'s dialect'),'the sound\'s chip of a type the dialect does not write: '+(chips()[0]&&chips()[0].textContent));
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

// A waiting picture follows configuration delivered over the socket: a late
// capability answer, a model change, and changed bounds all update its chip
// before Send, without another upload or an operator touching the chip.
func TestAWaitingPicturesChipFollowsSocketCapabilityChanges(t *testing.T) {
	page := `<!doctype html>
<div id="thread"><div id="thread-inner"></div></div>
<div id="composer-wrap"><div id="attach-chips" hidden></div><textarea id="msg-input"></textarea><button id="attach-btn">+</button><input type="file" id="attach-input" multiple hidden><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import {S} from '/state.js';import {connect} from '/ws.js';import {takeFiles} from '/views/attach.js';import {run} from '/__harness.js';
run(async assert=>{
 const sockets=[];
 window.WebSocket=class {
  constructor(){this.readyState=0;this.frames=[];sockets.push(this);}
  send(bytes){this.frames.push(JSON.parse(bytes));}
  recv(frame){this.onmessage({data:JSON.stringify(frame)});}
 };
 S.view='chat';S.stats={name:'Fixture'};S.identityExists=true;
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 const config=llm=>socket.recv({type:'config',config:{llm}});
 config({resolved_model:'unknown'});
 const realFetch=window.fetch;let uploads=0;
 window.fetch=async(url,init)=>{
  if(String(url).startsWith('/upload?')){uploads++;return new Response(JSON.stringify({path:'uploads/proof.png',name:'proof.png',bytes:9,image:true}),{status:200,headers:{'Content-Type':'application/json'}});}
  return realFetch(url,init);
 };
 const chip=()=>document.querySelector('#attach-chips .attach-chip');
 takeFiles([new File(['\x89PNG....'],'proof.png',{type:'image/png'})]);
 for(let i=0;i<400&&(!chip()||!chip().textContent.includes('9 bytes'));i++)await new Promise(r=>requestAnimationFrame(r));
 assert(chip()&&chip().textContent.includes('to the folder only'),'the uploaded picture did not start with unknown support');
 config({resolved_model:'unknown',images_shown:'yes'});
 assert(chip().textContent.includes('the model will be shown it'),'a late capability answer did not update the waiting chip: '+chip().textContent);
 config({resolved_model:'text-only',images_shown:'no'});
 assert(chip().textContent.includes('to the folder only')&&!chip().textContent.includes('the model will be shown it'),'a model that takes no images left the waiting chip promising one: '+chip().textContent);
 config({resolved_model:'bounded',images_shown:'yes',image_max_bytes:8});
 assert(chip().textContent.includes('larger than one picture this model is shown (8 bytes)'),'a smaller bound did not update the waiting chip: '+chip().textContent);
 config({resolved_model:'bounded',images_shown:'yes',image_max_bytes:16});
 assert(chip().textContent.includes('the model will be shown it'),'a larger bound did not update the waiting chip: '+chip().textContent);
 const drop=chip().querySelector('.attach-drop');drop.focus();
 assert(document.activeElement===drop,'rig: the Remove button did not take focus');
 socket.recv({type:'status',stats:{name:'Fixture'}});
 assert(chip().querySelector('.attach-drop')===drop&&document.activeElement===drop,'an unrelated status push rebuilt the waiting chip or lost Remove focus');
 assert(uploads===1&&document.querySelectorAll('#attach-chips .attach-chip').length===1,'capability updates changed the attachment or uploaded it again');
});</script>`
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	chat := chatModuleRig(t)
	modules := wsPageModules(ws, chat["/state.js"])
	for _, path := range []string{"/views/chat.js", "/views/attach.js", "/util.js", "/presence.js"} {
		modules[path] = chat[path]
	}
	runPageInEngines(t, page, modules)
}
